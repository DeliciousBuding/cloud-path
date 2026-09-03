// Package edgedriverhost 把外部 Driver Plugin Host 接入 edge 进程生命周期：
// 由 A4.5 的 desired-state + lockfile 驱动，在 edge 退出时优雅关闭、不留孤儿进程。
//
// 当前是 host-only 形态：外部 driver 的 handshake / descriptor / observation
// 尚未桥接进 edge 数据流，因此外部 driver 只报告 unsupported，绝不伪装成在线设备。
package edgedriverhost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/plugincontrol"
	"github.com/DeliciousBuding/cloud-path/internal/pluginhost"
)

// ErrInvalidConfig 表示 edgedriverhost 配置不合法。
var ErrInvalidConfig = errors.New("edgedriverhost: invalid config")

// Dialer 是握手后连接插件传输端点的接缝。host-only 生命周期从不拨号；
// 保留该接缝是为了 A4.7 数据流桥接可注入，而不改变 host 接线。
type Dialer interface {
	Dial(ctx context.Context, network, address string) (io.Closer, error)
}

// Options 配置外部 Driver Plugin Host。Manager/Runner/Dialer 均为接缝：
// 测试注入 fake，生产用真实实现，任何测试都不启动真实第三方插件。
type Options struct {
	// Manager 是插件实例控制面（注册安装/建实例/启动/关闭）。
	// *pluginhost.Manager 满足它；为 nil 时由 Runner 构造真实 Manager。
	Manager plugincontrol.HostManager
	// Runner 是插件进程启动接缝（生产 ExecRunner）。仅在 Manager 为 nil 时使用。
	Runner pluginhost.Runner
	// Dialer 是插件传输拨号接缝（host-only 阶段保留，不调用）。
	Dialer Dialer

	PluginsDir string
	StateDir   string
	LockPath   string
	Tenant     string

	Logger       *slog.Logger
	CloseTimeout time.Duration
}

// Host 是 edge 侧的外部 Driver Plugin Host 生命周期门面。
type Host struct {
	manager plugincontrol.HostManager
	opts    Options
	ph      *plugincontrol.Host
}

// New 校验 options 并构造 Host。直到 Start 被调用前都不会启动插件进程。
func New(opts Options) (*Host, error) {
	if strings.TrimSpace(opts.PluginsDir) == "" {
		return nil, fmt.Errorf("%w: plugins dir is required", ErrInvalidConfig)
	}
	if strings.TrimSpace(opts.StateDir) == "" {
		return nil, fmt.Errorf("%w: state dir is required", ErrInvalidConfig)
	}
	if strings.TrimSpace(opts.LockPath) == "" {
		return nil, fmt.Errorf("%w: lock path is required", ErrInvalidConfig)
	}
	if strings.TrimSpace(opts.Tenant) == "" {
		opts.Tenant = "default"
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if opts.CloseTimeout <= 0 {
		opts.CloseTimeout = 10 * time.Second
	}

	manager := opts.Manager
	if manager == nil {
		runner := opts.Runner
		if runner == nil {
			runner = pluginhost.ExecRunner{}
		}
		manager = pluginhost.NewManager(pluginhost.ManagerOptions{
			Runner:           runner,
			Logger:           opts.Logger,
			Protocol:         "driver",
			ProtocolVersion:  1,
			HandshakeTimeout: 5 * time.Second,
			ShutdownTimeout:  5 * time.Second,
			MaxRestarts:      3,
			BaseBackoff:      100 * time.Millisecond,
			MaxBackoff:       5 * time.Second,
		})
	}

	ph, err := plugincontrol.NewHost(plugincontrol.HostOptions{
		Manager:    manager,
		Store:      plugincontrol.NewStore(opts.StateDir),
		PluginsDir: opts.PluginsDir,
		LockPath:   opts.LockPath,
		Logger:     opts.Logger,
	})
	if err != nil {
		return nil, err
	}
	return &Host{manager: manager, opts: opts, ph: ph}, nil
}

// Start 加载 desired-state 并启动已启用的 driver 实例。返回时 host 已完成装载；
// 实例进程监督在后台继续，直到 Close。装载失败会先清理中途已启动的实例，
// 避免 optional 路径留孤儿进程。
func (h *Host) Start(ctx context.Context) error {
	if _, err := h.ph.LoadTenant(ctx, h.opts.Tenant); err != nil {
		_ = h.Close()
		return err
	}
	return nil
}

// Run 阻塞至 ctx 取消，然后在 CloseTimeout 内优雅关闭 host。
func (h *Host) Run(ctx context.Context) error {
	<-ctx.Done()
	return h.Close()
}

// Close 在 CloseTimeout 内优雅关闭所有受监督插件进程。幂等。
// 超过 deadline 后返回错误；底层 Manager.Close 仍会在其内部 ShutdownTimeout
// 内完成强制清理（Windows Job Object / Unix process group 防孤儿）。
func (h *Host) Close() error {
	done := make(chan error, 1)
	go func() { done <- h.manager.Close() }()
	timer := time.NewTimer(h.opts.CloseTimeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		h.opts.Logger.Warn("plugin host close deadline exceeded", "timeout", h.opts.CloseTimeout.String())
		return fmt.Errorf("plugin host close deadline exceeded after %s", h.opts.CloseTimeout)
	}
}

// DriverIDs 返回已安装插件贡献的 driver ID（用于内置 adapter 冲突检测）。
func (h *Host) DriverIDs() ([]string, error) {
	return DriverIDs(h.opts.PluginsDir, h.opts.LockPath)
}
