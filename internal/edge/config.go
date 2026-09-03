// Package edge 是 Cloudpath 边缘代理运行时：串口设备监督（热插拔自愈）、
// 轮询/对时调度、状态上报与命令执行（WS 持久连接）。
package edge

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/DeliciousBuding/cloud-path/internal/device"
)

// 生命周期命令名：edge 在设备打开后与每个轮询拍尝试下发。
//
// 核心**设备无关**：这两个名字只是缺省约定，是否真的下发由适配器命令白名单决定
// （不在白名单内则静默跳过，不报错、不刷日志），并可用 DeviceCfg.PollCommand /
// SyncCommand 按设备覆盖。这样无硬件参考设备（examples/demo）不会因为核心
// 硬编码真实板子的对时命令而在每次打开/每个对时周期报错。
const (
	DefaultPollCommand = "dump" // 触发一次状态读取
	DefaultSyncCommand = "sync" // 触发一次设备侧时间/基准对齐
)

// PortOptionalAdapter 由无硬件适配器可选实现：PortRequired() == false 表示
// 该适配器不需要真实端口，配置校验放宽 port 必填。
//
// 这是**结构性约定**而不是 driverkit 契约：examples/* 有「不得 import internal/*」
// 的拆仓红线，因此约定同时写在本包与适配器包（examples/demo）的文档里，靠方法名
// 与签名对齐。未实现该接口的适配器（如 stcb）一律 port 必填——校验强度不被削弱；
// 适配器未注册时同样按必填处理（fail-closed）。
type PortOptionalAdapter interface{ PortRequired() bool }

// PortRequired 报告具名适配器是否需要真实端口。
func PortRequired(adapterName string) bool {
	a, ok := device.Get(adapterName)
	if !ok {
		return true // 未注册：无从放宽，fail-closed
	}
	if p, ok := a.(PortOptionalAdapter); ok {
		return p.PortRequired()
	}
	return true
}

// Config 是 edge 配置（edge.yaml，本地私有不入库）。
type Config struct {
	Server          string        `yaml:"server"`            // ws://host:port/ws/edge
	Token           string        `yaml:"token"`             // 支持 ${ENV} 展开
	EdgeID          string        `yaml:"edge_id"`           // 缺省用主机名
	PollIntervalS   int           `yaml:"poll_interval_s"`   // 转储轮询（默认 5）
	SyncIntervalS   int           `yaml:"sync_interval_s"`   // 对时周期（默认 600）
	ReportIntervalS int           `yaml:"report_interval_s"` // 状态心跳兜底（默认 30）
	Devices         []DeviceCfg   `yaml:"devices"`
	PluginHost      PluginHostCfg `yaml:"plugin_host"`
}

// DeviceCfg 是单台设备配置。
type DeviceCfg struct {
	ID      string `yaml:"id"`
	Adapter string `yaml:"adapter"` // 设备适配器名（如 stcb / demo）
	Name    string `yaml:"name"`
	Port    string `yaml:"port"` // Windows: COM3；Linux: /dev/ttyUSB0；无硬件适配器可省略
	Baud    int    `yaml:"baud"` // 默认 9600
	// Extra 是适配器自定义参数，原样传给 driverkit.Config.Extra
	// （例如 demo 的 tick_interval_s）。核心不解释其中任何键。
	Extra map[string]string `yaml:"extra"`
	// PollCommand / SyncCommand 覆盖生命周期命令名（缺省 dump / sync）。
	// 无论叫什么，都只在适配器命令白名单内才会真正下发。
	PollCommand string `yaml:"poll_command"`
	SyncCommand string `yaml:"sync_command"`
}

// PluginHostCfg 配置可选的进程内外部 Driver Plugin Host。默认不启用，保持 P1
// 纯内置 adapter 行为；启用时按 desired-state + lockfile 启动外部 driver 实例。
type PluginHostCfg struct {
	Enabled       bool   `yaml:"enabled"`         // false=不启用外部 host（默认）
	Root          string `yaml:"root"`            // 插件安装根目录（如 plugins.d）
	StateDir      string `yaml:"state_dir"`       // 插件实例 desired-state 目录
	Tenant        string `yaml:"tenant"`          // 外部 driver 实例租户（缺省 default）
	Required      bool   `yaml:"required"`        // true=host 失败则 edge 启动失败；false=optional(DEGRADED)
	Lock          string `yaml:"lock"`            // plugins.lock 路径（缺省 <root>/plugins.lock）
	CloseTimeoutS int    `yaml:"close_timeout_s"` // host 优雅关闭 deadline（秒，默认 10）
	// SyncState 是插件控制面本地 applied cache 路径（缺省 <state_dir>/applied.json）：
	// 保存最近成功应用的 revision + server 下发的规范化摘要 + 每实例结果，
	// 进程重启后据此拒绝旧 revision（control-plane-sync.md §3.2）。
	SyncState string `yaml:"sync_state"`
	// SecretDir 是本地 secret provider 根目录（缺省 <state_dir>/secrets）：
	// 明文只存在于 <dir>/<tenant>/<instance>/<name>，永不进 WS/日志/cache（§7）。
	SecretDir string `yaml:"secret_dir"`
}

// LoadConfig 读取并校验配置。Token 等字段支持 ${ENV_VAR} 展开（凭据不落盘）。
func LoadConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置失败: %w（请复制 edge.example.yaml 为 edge.yaml 并填写）", err)
	}
	var cfg Config
	if err := yaml.Unmarshal([]byte(os.ExpandEnv(string(raw))), &cfg); err != nil {
		return nil, fmt.Errorf("解析配置失败: %w", err)
	}
	if cfg.Server == "" {
		cfg.Server = "ws://127.0.0.1:8080/ws/edge"
	}
	if !strings.HasPrefix(cfg.Server, "ws://") && !strings.HasPrefix(cfg.Server, "wss://") {
		return nil, fmt.Errorf("server 须以 ws:// 或 wss:// 开头，got %q", cfg.Server)
	}
	if cfg.EdgeID == "" {
		hn, err := os.Hostname()
		if err != nil || hn == "" {
			hn = "edge"
		}
		cfg.EdgeID = strings.ToLower(strings.ReplaceAll(hn, ".", "-"))
	}
	if cfg.PollIntervalS <= 0 {
		cfg.PollIntervalS = 5
	}
	if cfg.SyncIntervalS <= 0 {
		cfg.SyncIntervalS = 600
	}
	if cfg.ReportIntervalS <= 0 {
		cfg.ReportIntervalS = 30
	}
	if len(cfg.Devices) == 0 {
		return nil, fmt.Errorf("devices 为空：至少配置一台设备")
	}
	seen := map[string]bool{}
	for i := range cfg.Devices {
		d := &cfg.Devices[i]
		if d.ID == "" {
			return nil, fmt.Errorf("devices[%d]: id 必填", i)
		}
		if seen[d.ID] {
			return nil, fmt.Errorf("devices[%d]: id %q 重复", i, d.ID)
		}
		seen[d.ID] = true
		if d.Adapter == "" {
			return nil, fmt.Errorf("devices[%s]: adapter 必填", d.ID)
		}
		// port 只对需要真实端口的适配器必填（无硬件参考设备可省略）。
		if d.Port == "" && PortRequired(d.Adapter) {
			return nil, fmt.Errorf("devices[%s]: port 必填（如 COM3 或 /dev/ttyUSB0）", d.ID)
		}
		if d.Baud <= 0 {
			d.Baud = 9600
		}
		if strings.TrimSpace(d.PollCommand) == "" {
			d.PollCommand = DefaultPollCommand
		}
		if strings.TrimSpace(d.SyncCommand) == "" {
			d.SyncCommand = DefaultSyncCommand
		}
	}
	if err := cfg.validatePluginHost(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// validatePluginHost 校验外部 Driver Plugin Host 配置。默认不启用时不校验；
// 启用时 fail-closed：root/state 必填，tenant/lock/close_timeout 提供默认值。
func (c *Config) validatePluginHost() error {
	ph := &c.PluginHost
	if !ph.Enabled {
		return nil
	}
	if strings.TrimSpace(ph.Root) == "" {
		return fmt.Errorf("plugin_host.root 必填（启用外部 Driver Plugin Host 时）")
	}
	if strings.TrimSpace(ph.StateDir) == "" {
		return fmt.Errorf("plugin_host.state_dir 必填（启用外部 Driver Plugin Host 时）")
	}
	if strings.TrimSpace(ph.Tenant) == "" {
		ph.Tenant = "default"
	}
	if strings.TrimSpace(ph.Lock) == "" {
		ph.Lock = filepath.Join(ph.Root, "plugins.lock")
	}
	if strings.TrimSpace(ph.SyncState) == "" {
		ph.SyncState = filepath.Join(ph.StateDir, "applied.json")
	}
	if strings.TrimSpace(ph.SecretDir) == "" {
		ph.SecretDir = filepath.Join(ph.StateDir, "secrets")
	}
	if ph.CloseTimeoutS <= 0 {
		ph.CloseTimeoutS = 10
	}
	return nil
}
