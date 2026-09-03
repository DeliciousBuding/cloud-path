package edge

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/device"
	"github.com/DeliciousBuding/cloud-path/internal/edgedriverhost"
	"github.com/DeliciousBuding/cloud-path/internal/model"
)

// Edge 是边缘代理运行时：N 台设备监督协程 + 1 条 server 长连接。
type Edge struct {
	cfg    *Config
	client *wsClient
	sups   map[string]*supervisor // key: "<edge_id>/<device_id>"
	ctx    context.Context        // 运行期根 ctx（命令执行据此取消）
}

// supervisor 监督单台设备：打开→采集→端口死→退避重开（热插拔自愈）。
type supervisor struct {
	edgeID  string
	dcfg    DeviceCfg
	adapter device.Adapter
	report  func(key string, sup *supervisor, force bool) // 状态上报回调（注入避免环依赖）

	mu       sync.Mutex
	dev      device.Device
	last     device.State // 最近一次快照（端口死时保留 Raw 只翻 Online）
	lastSent string       // 上次上报的状态序列化（diff 抑制）
	lastDesc string       // 上次上报的 Descriptor 序列化（diff 抑制）
	sentAt   time.Time
}

func (s *supervisor) setDev(d device.Device) {
	s.mu.Lock()
	s.dev = d
	s.mu.Unlock()
}

func (s *supervisor) send(ctx context.Context, c device.Command) error {
	s.mu.Lock()
	dev := s.dev
	s.mu.Unlock()
	if dev == nil {
		return fmt.Errorf("device offline")
	}
	return dev.Send(ctx, c)
}

// snapshot 取当前状态；端口死时返回 last（Online=false）。
func (s *supervisor) snapshot() device.State {
	s.mu.Lock()
	dev, last := s.dev, s.last
	s.mu.Unlock()
	if dev == nil {
		last.Online = false
		if last.Raw == nil {
			last.Raw = map[string]any{}
		}
		return last
	}
	st := dev.Snapshot()
	s.mu.Lock()
	s.last = st
	s.mu.Unlock()
	return st
}

// PluginHost 是进程内外部 Driver Plugin Host 的生命周期接口。
// 由 edgedriverhost.Host 实现；测试注入 fake，不启动真实第三方插件。
type PluginHost interface {
	Start(ctx context.Context) error
	Run(ctx context.Context) error
	DriverIDs() ([]string, error)
}

// RunOption 调整 Run 行为（上层注入外部 Driver Plugin Host）。
type RunOption func(*runOptions)

type runOptions struct {
	host PluginHost
}

// WithPluginHost 注入外部 Driver Plugin Host；nil 等价于未启用。
func WithPluginHost(h PluginHost) RunOption {
	return func(o *runOptions) { o.host = h }
}

// Run 启动 edge 并阻塞至 ctx 取消（信号停机由 main 负责）。
// 启用外部 Driver Plugin Host 时：先装载/启动 host，再运行内置 device supervisor；
// ctx 取消时两者都收敛，host 在 CloseTimeout 内优雅关闭（不留孤儿）。
func Run(ctx context.Context, cfg *Config, version string, opts ...RunOption) error {
	ro := runOptions{}
	for _, o := range opts {
		o(&ro)
	}

	e := &Edge{cfg: cfg, sups: map[string]*supervisor{}, ctx: ctx}
	metas := make([]api.DeviceMeta, 0, len(cfg.Devices))
	for _, d := range cfg.Devices {
		a, ok := device.Get(d.Adapter)
		if !ok {
			return fmt.Errorf("adapter %q 未注册（已注册: %v）", d.Adapter, device.Names())
		}
		key := api.DeviceKey(cfg.EdgeID, d.ID)
		sup := &supervisor{edgeID: cfg.EdgeID, dcfg: d, adapter: a}
		sup.report = e.reportState
		e.sups[key] = sup
		metas = append(metas, api.DeviceMeta{ID: d.ID, Adapter: d.Adapter, Name: d.Name, Port: d.Port})
	}

	host := ro.host
	if host == nil && cfg.PluginHost.Enabled {
		return fmt.Errorf("plugin_host.enabled 但未提供外部 Driver Plugin Host（接线错误）")
	}
	hostRunning := false
	if host != nil {
		var hostErr error
		ids, err := host.DriverIDs()
		if err != nil {
			hostErr = err
		} else if err := edgedriverhost.CheckConflicts(device.Names(), ids); err != nil {
			return err // 内置 adapter 与外部 driver ID 冲突：显式拒绝，绝不静默覆盖
		} else if err := host.Start(ctx); err != nil {
			hostErr = err
		} else {
			hostRunning = true
			e.reportHostOnlyDrivers(ids)
			slog.Info("external driver host started", "drivers", ids, "tenant", cfg.PluginHost.Tenant)
		}
		if hostErr != nil {
			if cfg.PluginHost.Required {
				return fmt.Errorf("external driver host: %w", hostErr)
			}
			slog.Warn("external driver host unavailable; builtin devices continue", "err", hostErr, "status", "DEGRADED")
			host = nil
		}
	}

	e.client = newWSClient(cfg, version, metas, e.onCommand, e.onServerOnline)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		e.client.run(ctx)
	}()
	if hostRunning {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := host.Run(ctx); err != nil {
				slog.Warn("external driver host stop", "err", err)
			}
		}()
	}
	for key, sup := range e.sups {
		wg.Add(2)
		go func(key string, sup *supervisor) {
			defer wg.Done()
			e.supervise(ctx, key, sup)
		}(key, sup)
		go func(key string, sup *supervisor) {
			defer wg.Done()
			e.pollLoop(ctx, key, sup)
		}(key, sup)
	}

	slog.Info("edge started", "edge", cfg.EdgeID, "devices", len(cfg.Devices),
		"server", cfg.Server, "poll_s", cfg.PollIntervalS, "sync_s", cfg.SyncIntervalS, "pid", os.Getpid())
	wg.Wait()
	slog.Info("edge stopped", "edge", cfg.EdgeID)
	return nil
}

// reportHostOnlyDrivers 明确报告外部 driver 尚未桥接进数据流：
// 只记 unsupported 日志，绝不生成 fake 设备或伪装在线。
func (e *Edge) reportHostOnlyDrivers(ids []string) {
	for _, id := range ids {
		slog.Warn("external driver host-only: data flow not bridged", "driver", id, "status", "unsupported")
	}
}

// supervise 是设备连接生命周期循环。
func (e *Edge) supervise(ctx context.Context, key string, sup *supervisor) {
	backoff := time.Second
	for ctx.Err() == nil {
		dev, err := sup.adapter.Open(ctx, device.Config{
			ID: sup.dcfg.ID, Name: sup.dcfg.Name,
			Port: sup.dcfg.Port, Baud: sup.dcfg.Baud,
		}, func(ev device.Event) { e.onDeviceEvent(key, ev) })
		if err != nil {
			slog.Warn("device open failed", "device", key, "port", sup.dcfg.Port,
				"err", err, "retry_in", backoff.Round(time.Millisecond))
			sup.setDev(nil)
			e.reportState(key, sup, true)
			e.reportDescriptor(key, sup, true)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, 30*time.Second)
			continue
		}
		backoff = time.Second
		sup.setDev(dev)
		slog.Info("device opened", "device", key, "port", sup.dcfg.Port)

		// 上电即对时+转储：板子掉电后 RTC 会被重置，对时是刚需（protocol.md 板级限制）
		go func() {
			sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			if err := dev.Send(sctx, device.Command{Cmd: "sync"}); err != nil {
				slog.Warn("initial sync failed", "device", key, "err", err)
			}
			time.Sleep(300 * time.Millisecond)
			_ = dev.Send(sctx, device.Command{Cmd: "dump"})
			e.reportState(key, sup, true)
			e.reportDescriptor(key, sup, true)
		}()

		select {
		case <-dev.Done():
			slog.Warn("device port died (unplugged?)", "device", key)
		case <-ctx.Done():
		}
		_ = dev.Close()
		sup.setDev(nil)
		e.reportState(key, sup, true)
		e.reportDescriptor(key, sup, true)
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second): // 拔插防抖
		}
	}
}

// pollLoop 周期转储轮询 + 周期对时 + 状态上报（diff 抑制 + 心跳兜底）。
func (e *Edge) pollLoop(ctx context.Context, key string, sup *supervisor) {
	poll := time.NewTicker(time.Duration(e.cfg.PollIntervalS) * time.Second)
	defer poll.Stop()
	syncT := time.NewTicker(time.Duration(e.cfg.SyncIntervalS) * time.Second)
	defer syncT.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-poll.C:
			sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			_ = sup.send(sctx, device.Command{Cmd: "dump"})
			cancel()
			// 转储回包 ~100ms 到达，稍候再快照上报 + Descriptor 刷新（观测值随之更新）
			time.AfterFunc(500*time.Millisecond, func() {
				e.reportState(key, sup, false)
				e.reportDescriptor(key, sup, false)
			})
		case <-syncT.C:
			sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			if err := sup.send(sctx, device.Command{Cmd: "sync"}); err != nil {
				slog.Debug("periodic sync skipped", "device", key, "err", err)
			}
			cancel()
		}
	}
}

// reportState 上报状态：变化即发，未变化按 ReportIntervalS 心跳兜底。force 无视 diff。
func (e *Edge) reportState(key string, sup *supervisor, force bool) {
	st := sup.snapshot()
	data := mustJSON(api.StateData{Online: st.Online, Raw: st.Raw, UpdatedAt: st.UpdatedAt.Unix()})

	sup.mu.Lock()
	changed := string(data) != sup.lastSent
	stale := time.Since(sup.sentAt) >= time.Duration(e.cfg.ReportIntervalS)*time.Second
	sup.mu.Unlock()
	if !force && !changed && !stale {
		return
	}

	env := api.Envelope{V: api.Version, Type: api.MsgState, Device: key, Ts: time.Now().Unix(), Data: data}
	if e.client.enqueue(env) || force {
		sup.mu.Lock()
		sup.lastSent = string(data)
		sup.sentAt = time.Now()
		sup.mu.Unlock()
	}
}

// reportDescriptor 上报设备 Descriptor：变化即发，force 无视 diff（上线/重连首刷）。
func (e *Edge) reportDescriptor(key string, sup *supervisor, force bool) {
	env := e.descriptorEnvelope(key, sup)
	if env == nil {
		return
	}
	sup.mu.Lock()
	changed := string(env.Data) != sup.lastDesc
	sup.mu.Unlock()
	if !force && !changed {
		return
	}
	if e.client.enqueue(*env) || force {
		sup.mu.Lock()
		sup.lastDesc = string(env.Data)
		sup.mu.Unlock()
	}
}

// descriptorEnvelope 构造设备 Descriptor 的 WS 信封；适配器不支持 Descriptor 时返回 nil。
// 优先设备实例的实时 Descriptor（含观测值），回落 Adapter 静态 Descriptor（结构骨架）。
func (e *Edge) descriptorEnvelope(key string, sup *supervisor) *api.Envelope {
	sup.mu.Lock()
	dev := sup.dev
	sup.mu.Unlock()

	var desc model.Descriptor
	switch {
	case dev != nil:
		if ds, ok := dev.(device.DescriptorSource); ok {
			desc = ds.Descriptor()
		} else if dp, ok := sup.adapter.(device.DescriptorProvider); ok {
			desc = dp.Descriptor(device.Config{ID: sup.dcfg.ID, Name: sup.dcfg.Name, Port: sup.dcfg.Port})
		} else {
			return nil
		}
	default:
		dp, ok := sup.adapter.(device.DescriptorProvider)
		if !ok {
			return nil
		}
		desc = dp.Descriptor(device.Config{ID: sup.dcfg.ID, Name: sup.dcfg.Name, Port: sup.dcfg.Port})
	}

	// 绑定稳定身份：device_id = "<edge>/<dev>"（Core 键），external_id = Driver 内不可变短 ID。
	desc.DeviceID = key
	desc.ExternalID = sup.dcfg.ID
	data := mustJSON(desc)
	return &api.Envelope{V: api.Version, Type: api.MsgDescriptor, Device: key, Ts: time.Now().Unix(), Data: data}
}

func (e *Edge) onDeviceEvent(key string, ev device.Event) {
	slog.Info("device event", "device", key, "type", ev.Type)
	data := mustJSON(api.EventData{Type: ev.Type})
	e.client.enqueue(api.Envelope{V: api.Version, Type: api.MsgEvent, Device: key, Ts: ev.At.Unix(), Data: data})
	// 事件通常伴随状态机变化：延迟一拍补报状态（等固件落定）
	time.AfterFunc(time.Second, func() {
		if sup, ok := e.sups[key]; ok {
			e.reportState(key, sup, false)
		}
	})
}

// onServerOnline 在（重）连上 server 后强制补报全部设备状态：
// 断线期间状态消息是被丢弃的（幂等），重连后必须主动刷一遍，面板才不会停在旧值。
func (e *Edge) onServerOnline() {
	for key, sup := range e.sups {
		e.reportState(key, sup, true)
		e.reportDescriptor(key, sup, true)
	}
}

// onCommand 执行 server 下行命令并回执 ack。
func (e *Edge) onCommand(env api.Envelope) {
	var cmd api.CommandData
	if err := json.Unmarshal(env.Data, &cmd); err != nil {
		slog.Warn("bad command payload", "err", err)
		return
	}
	sup, ok := e.sups[env.Device]
	ack := func(status, detail string) {
		data := mustJSON(api.AckData{CommandID: cmd.CommandID, Status: status, Detail: detail})
		e.client.enqueue(api.Envelope{V: api.Version, Type: api.MsgCommandAck,
			Device: env.Device, Ts: time.Now().Unix(), Data: data})
	}
	if !ok {
		ack("failed", "unknown device")
		return
	}
	slog.Info("executing command", "device", env.Device, "cmd", cmd.Cmd, "cmd_id", cmd.CommandID)
	base := e.ctx
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithTimeout(base, 15*time.Second)
	defer cancel()
	if err := sup.send(ctx, device.Command{ID: cmd.CommandID, Cmd: cmd.Cmd, Args: cmd.Args}); err != nil {
		ack("failed", err.Error())
		return
	}
	ack("ok", "")
	// 命令多会改状态（sync 对时/dump 转储）：稍候强制补报一次
	time.AfterFunc(1500*time.Millisecond, func() { e.reportState(env.Device, sup, true) })
}
