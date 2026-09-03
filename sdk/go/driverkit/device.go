// Package driverkit 定义外部 Driver 插件所需的公共契约：Config / State / Event /
// Command / Device / Adapter 与 Descriptor/Capability 类型，以及进程内注册表。
// 外部插件只依赖本包与 sdk/go/model，不 import 任何 internal/*。
//
// 语义定义以 docs/architecture/capability-model.md 为准；本包只使用标准库与
// sdk/go/model，不引入第三方依赖。Core 的 internal/device 是本包的薄兼容层，
// 与本包共享同一个注册表。
package driverkit

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/DeliciousBuding/cloud-path/sdk/go/model"
)

// Command 是一次命令下发。ID 为 server 侧命令行主键（edge 本地命令为 0）。
type Command struct {
	ID   int64
	Cmd  string
	Args string
}

// Event 是设备主动上报的事件（规范化标签由适配器负责）。
type Event struct {
	Type string
	At   time.Time
}

// State 是设备状态快照。Raw 承载适配器自定义语义，原样透传到前端。
type State struct {
	Online    bool
	Raw       map[string]any
	UpdatedAt time.Time
}

// Device 是一台已打开的设备连接。
type Device interface {
	ID() string
	// Snapshot 返回当前状态（含在线判定），必须并发安全。
	Snapshot() State
	// Send 执行命令。适配器负责命令白名单校验。
	Send(ctx context.Context, c Command) error
	// Done 在连接致命错误（拔线/端口死）时关闭，供上层监督重启。
	Done() <-chan struct{}
	Close() error
}

// Config 是设备打开参数（来自 edge 配置文件）。
type Config struct {
	ID    string
	Name  string
	Port  string
	Baud  int
	Extra map[string]string
}

// Adapter 是设备适配器工厂。
type Adapter interface {
	Name() string
	// SupportedCommands 返回命令白名单（server 拒绝未知命令）。
	SupportedCommands() []string
	// Open 打开设备并启动接收循环；onEvent 回调设备事件（适配器保证并发安全调用）。
	Open(ctx context.Context, cfg Config, onEvent func(Event)) (Device, error)
}

// DescriptorProvider 由 Adapter 可选实现：声明该适配器设备的静态 Descriptor
// （Entity/Capability 结构与身份；身份取自 Open 时的 cfg，观测值由 Device 后填）。
// 未实现则 Core 对该适配器设备不生成 Descriptor（前端走通用回落）。
type DescriptorProvider interface {
	Descriptor(cfg Config) model.Descriptor
}

// CapabilityProvider 由 Adapter 可选实现：声明该适配器使用的 Capability catalog
// （Capability 文档，供 /api/capabilities 下发）。未实现则 catalog 为空。
type CapabilityProvider interface {
	Capabilities() []model.Capability
}

// DescriptorSource 由 Device 可选实现：提供带实时观测的完整 Descriptor。
// 未实现时 edge 回落使用 Adapter 的静态 Descriptor。
type DescriptorSource interface {
	Descriptor() model.Descriptor
}

var (
	regMu    sync.RWMutex
	registry = map[string]Adapter{}
)

// Register 注册适配器（通常在各适配器包 init() 中调用）。重复注册 panic（编程错误早暴露）。
func Register(a Adapter) {
	regMu.Lock()
	defer regMu.Unlock()
	if _, dup := registry[a.Name()]; dup {
		panic("driverkit: duplicate adapter registration: " + a.Name())
	}
	registry[a.Name()] = a
}

// Get 按名取适配器。
func Get(name string) (Adapter, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	a, ok := registry[name]
	return a, ok
}

// Names 返回全部已注册适配器名（排序，确定性输出）。
func Names() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
