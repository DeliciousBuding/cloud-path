package edge

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/device"
	"github.com/DeliciousBuding/cloud-path/internal/edgedriverhost"
	"github.com/DeliciousBuding/cloud-path/internal/model"
	"github.com/DeliciousBuding/cloud-path/internal/plugincontrol"
	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/driver"
)

// Edge 是边缘代理运行时：N 台设备监督协程 + 1 条 server 长连接。
type Edge struct {
	cfg    *Config
	client *wsClient
	sups   map[string]*supervisor // key: "<edge_id>/<device_id>"
	ctx    context.Context        // 运行期根 ctx（命令执行据此取消）
	// sync 是插件控制面收敛器；nil 表示本 Edge 不承载插件面
	// （plugin_desired 忽略并记 debug，plugin_status 不上报）。
	sync *plugincontrol.Syncer

	// external 是外部 Driver Plugin Host 贡献的桥接 adapter（driver ID -> adapter）。
	external map[string]device.Adapter

	capsMu sync.Mutex
	capsFP string // 上次上报的 Capability 文档指纹（diff 抑制，避免每次重连/期望态变更刷同一条）
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
	lastSent string       // 上次上报的状态语义指纹（diff 抑制，忽略观测时间戳）
	lastDesc string       // 上次上报的 Descriptor 语义指纹（diff 抑制）
	sentAt   time.Time

	// cmdMu 串行化对**同一台设备**的全部写入（命令 + 轮询 + 对时）。
	// 一台设备就是一条物理线/一个状态机：固件命令缓冲可能只有 1 字节（stcb 的
	// 慢发对时正是如此），并发写会互相插字节、把两条命令都写坏。
	// 不同设备之间不共享该锁，因此多设备仍然完全并行、互不影响。
	cmdMu sync.Mutex
}

func (s *supervisor) setDev(d device.Device) {
	s.mu.Lock()
	s.dev = d
	s.mu.Unlock()
}

// send 串行执行一条命令（不需要结果摘要的路径：轮询、对时、上电初始化）。
func (s *supervisor) send(ctx context.Context, c device.Command) error {
	s.cmdMu.Lock()
	defer s.cmdMu.Unlock()
	_, err := s.sendWithResult(ctx, c)
	return err
}

// sendForReport 串行执行一条命令并取回可上报的结果摘要（server 下行命令路径）。
func (s *supervisor) sendForReport(ctx context.Context, c device.Command) (string, error) {
	s.cmdMu.Lock()
	defer s.cmdMu.Unlock()
	return s.sendWithResult(ctx, c)
}

// sendWithResult 执行命令并返回可上报的执行结果摘要（「执行结果反馈」的成功路径）。
// 调用方必须持有 cmdMu（同设备串行）。
//
// 适配器实现 ResultSender 时用它的设备专属摘要；未实现时回落到通用事实摘要
// （cmd/args），绝不因为缺少摘要就把成功命令的 result 留空。
func (s *supervisor) sendWithResult(ctx context.Context, c device.Command) (string, error) {
	s.mu.Lock()
	dev := s.dev
	s.mu.Unlock()
	if dev == nil {
		return "", fmt.Errorf("device offline")
	}
	if rs, ok := dev.(ResultSender); ok {
		return rs.SendWithResult(ctx, c)
	}
	if err := dev.Send(ctx, c); err != nil {
		return "", err
	}
	return fallbackResult(c), nil
}

// fallbackResult 是适配器未提供结果摘要时的通用事实摘要（核心设备无关：
// 只回显命令名与参数，不猜测任何设备语义）。
func fallbackResult(c device.Command) string {
	if strings.TrimSpace(c.Args) == "" {
		return "cmd=" + c.Cmd
	}
	return "cmd=" + c.Cmd + " args=" + c.Args
}

// pollCommand / syncCommand 返回本设备的生命周期命令名（配置可覆盖，缺省 dump/sync）。
func (s *supervisor) pollCommand() string {
	if v := strings.TrimSpace(s.dcfg.PollCommand); v != "" {
		return v
	}
	return DefaultPollCommand
}

func (s *supervisor) syncCommand() string {
	if v := strings.TrimSpace(s.dcfg.SyncCommand); v != "" {
		return v
	}
	return DefaultSyncCommand
}

// supports 报告适配器命令白名单是否包含 cmd。
//
// 核心设备无关：生命周期命令只在适配器自己声明支持时才下发。无硬件参考设备
// （examples/demo）没有对时语义，因此不会在每次打开/每个对时周期产生失败与噪声。
func (s *supervisor) supports(cmd string) bool {
	if cmd == "" {
		return false
	}
	for _, c := range s.adapter.SupportedCommands() {
		if c == cmd {
			return true
		}
	}
	return false
}

// deviceConfig 构造打开参数（含适配器自定义 Extra，核心不解释其中任何键）。
func (s *supervisor) deviceConfig() device.Config {
	return device.Config{
		ID: s.dcfg.ID, Name: s.dcfg.Name,
		Port: s.dcfg.Port, Baud: s.dcfg.Baud,
		Extra: s.dcfg.Extra,
	}
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

// ResultSender 由适配器设备可选实现：Send 的「带执行结果摘要」版本。
//
// 为什么是 internal/edge 内的结构性约定而不是 driverkit 契约：
//   - examples/* 有「不得 import internal/*」的拆仓红线；
//   - driverkit.Device.Send 的签名是已发布 SDK 契约，加方法会破坏全部外部适配器。
//
// 因此约定写在两侧文档里（本包 + examples/demo），靠方法名与签名
// 对齐。未实现时 edge 回落 Device.Send + fallbackResult。
//
// 红线：返回的摘要必须是**短的非敏感事实**——不含明文 secret / 访问令牌 /
// 本机绝对路径 / 进程 stdout、stderr 原文。edge 出网前再统一过 SanitizeDetail。
type ResultSender interface {
	SendWithResult(ctx context.Context, c device.Command) (result string, err error)
}

// PluginHost 是进程内外部 Driver Plugin Host 的生命周期接口。
// 由 edgedriverhost.Host 实现；测试注入 fake，不启动真实第三方插件。
type PluginHost interface {
	Start(ctx context.Context) error
	Run(ctx context.Context) error
	DriverIDs() ([]string, error)
	DriverClient(driverID string) (driver.DriverClient, error)
}

// RunOption 调整 Run 行为（上层注入外部 Driver Plugin Host）。
type RunOption func(*runOptions)

type runOptions struct {
	host       PluginHost
	httpClient *http.Client
	sync       *plugincontrol.Syncer
}

// WithPluginHost 注入外部 Driver Plugin Host；nil 等价于未启用。
func WithPluginHost(h PluginHost) RunOption {
	return func(o *runOptions) { o.host = h }
}

// withHTTPClient 注入 WS 拨号用的 HTTP client（未导出：仅供测试验证 wss:// TLS
// 拨号真的可用）。生产走 nil = 系统信任库，公网正规证书直接可连。
func withHTTPClient(c *http.Client) RunOption {
	return func(o *runOptions) { o.httpClient = c }
}

// Run 启动 edge 并阻塞至 ctx 取消（信号停机由 main 负责）。
// 启用外部 Driver Plugin Host 时：先装载/启动 host，再运行内置 device supervisor；
// ctx 取消时两者都收敛，host 在 CloseTimeout 内优雅关闭（不留孤儿）。
func Run(ctx context.Context, cfg *Config, version string, opts ...RunOption) error {
	ro := runOptions{}
	for _, o := range opts {
		o(&ro)
	}

	e := &Edge{cfg: cfg, sups: map[string]*supervisor{}, external: map[string]device.Adapter{}, ctx: ctx}

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
			for _, id := range ids {
				e.external[id] = newExternalAdapter(host, id)
			}
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

	metas := make([]api.DeviceMeta, 0, len(cfg.Devices))
	for _, d := range cfg.Devices {
		a := e.resolveAdapter(d.Adapter)
		if a == nil {
			return fmt.Errorf("adapter %q 未注册（内置: %v / 外部: %v）", d.Adapter, device.Names(), externalDriverNames(e.external))
		}
		key := api.DeviceKey(cfg.EdgeID, d.ID)
		sup := &supervisor{edgeID: cfg.EdgeID, dcfg: d, adapter: a}
		sup.report = e.reportState
		e.sups[key] = sup
		metas = append(metas, api.DeviceMeta{ID: d.ID, Adapter: d.Adapter, Name: d.Name, Port: d.Port})
	}

	e.client = newWSClient(cfg, version, metas, e.onCommand, e.onServerOnline)
	if ro.httpClient != nil {
		e.client.httpClient = ro.httpClient
	}
	if ro.sync != nil {
		e.sync = ro.sync
		e.client.setPluginHandler(e.onPluginDesired)
		slog.Info("plugin control plane enabled", "tenant", cfg.PluginHost.Tenant,
			"boot_id", ro.sync.BootID(), "applied_revision", ro.sync.AppliedRevision())
	}

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
	if e.sync != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.pluginStatusLoop(ctx)
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

// resolveAdapter 按名字解析设备 adapter：先内置（driverkit 注册表），
// 再外部 Driver Plugin Host 贡献的桥接 adapter。
func (e *Edge) resolveAdapter(name string) device.Adapter {
	if a, ok := device.Get(name); ok {
		return a
	}
	if a, ok := e.external[name]; ok {
		return a
	}
	return nil
}

// externalDriverNames 返回已桥接的外部 driver ID（排序，用于错误提示）。
func externalDriverNames(m map[string]device.Adapter) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// supervise 是设备连接生命周期循环。
func (e *Edge) supervise(ctx context.Context, key string, sup *supervisor) {
	backoff := time.Second
	for ctx.Err() == nil {
		dev, err := sup.adapter.Open(ctx, sup.deviceConfig(), func(ev device.Event) { e.onDeviceEvent(key, ev) })
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

		// 上电即对时+转储（仅在适配器声明支持时）：真实板子掉电后 RTC 会被重置，
		// 对时是刚需（protocol.md 板级限制）；无对时语义的适配器静默跳过。
		go func() {
			sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			if syncCmd := sup.syncCommand(); sup.supports(syncCmd) {
				if err := dev.Send(sctx, device.Command{Cmd: syncCmd}); err != nil {
					slog.Warn("initial sync failed", "device", key, "cmd", syncCmd, "err", err)
				}
				time.Sleep(300 * time.Millisecond) // 等固件落定后再转储
			}
			if pollCmd := sup.pollCommand(); sup.supports(pollCmd) {
				_ = dev.Send(sctx, device.Command{Cmd: pollCmd})
			}
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
			// 状态读取命令是可选的：适配器不支持时仍然照常快照上报
			// （无硬件参考设备的状态由自身进程推进，不需要外部触发）。
			if pollCmd := sup.pollCommand(); sup.supports(pollCmd) {
				sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
				_ = sup.send(sctx, device.Command{Cmd: pollCmd})
				cancel()
			}
			// 回包 ~100ms 到达，稍候再快照上报 + Descriptor 刷新（观测值随之更新）
			time.AfterFunc(500*time.Millisecond, func() {
				e.reportState(key, sup, false)
				e.reportDescriptor(key, sup, false)
			})
		case <-syncT.C:
			syncCmd := sup.syncCommand()
			if !sup.supports(syncCmd) {
				continue // 适配器无对时语义：静默跳过（核心不得假设设备行为）
			}
			sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			if err := sup.send(sctx, device.Command{Cmd: syncCmd}); err != nil {
				slog.Debug("periodic sync skipped", "device", key, "cmd", syncCmd, "err", err)
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

// reportDescriptor 上报设备 Descriptor：语义变化即发，force 无视 diff（上线/重连首刷）。
//
// diff 判定用「抹掉 observation 时间戳」的语义指纹：observed_at / received_at 每拍
// 都在变，若参与比较会让 diff 抑制彻底失效（每拍重发整份 Descriptor）。上报的
// payload 仍然携带真实时间戳，只有「是否变化」的判定忽略它们。
func (e *Edge) reportDescriptor(key string, sup *supervisor, force bool) {
	env, fingerprint := e.descriptorEnvelope(key, sup)
	if env == nil {
		return
	}
	sup.mu.Lock()
	changed := fingerprint != sup.lastDesc
	sup.mu.Unlock()
	if !force && !changed {
		return
	}
	if e.client.enqueue(*env) || force {
		sup.mu.Lock()
		sup.lastDesc = fingerprint
		sup.mu.Unlock()
	}
}

// descriptorEnvelope 构造设备 Descriptor 的 WS 信封与其语义指纹；
// 适配器不支持 Descriptor 时返回 (nil, "")。
// 优先设备实例的实时 Descriptor（含观测值），回落 Adapter 静态 Descriptor（结构骨架）。
func (e *Edge) descriptorEnvelope(key string, sup *supervisor) (*api.Envelope, string) {
	sup.mu.Lock()
	dev := sup.dev
	sup.mu.Unlock()

	var desc model.Descriptor
	switch {
	case dev != nil:
		if ds, ok := dev.(device.DescriptorSource); ok {
			desc = ds.Descriptor()
		} else if dp, ok := sup.adapter.(device.DescriptorProvider); ok {
			desc = dp.Descriptor(sup.deviceConfig())
		} else {
			return nil, ""
		}
	default:
		dp, ok := sup.adapter.(device.DescriptorProvider)
		if !ok {
			return nil, ""
		}
		desc = dp.Descriptor(sup.deviceConfig())
	}

	// 绑定稳定身份：device_id = "<edge>/<dev>"（Core 键），external_id = Driver 内不可变短 ID。
	desc.DeviceID = key
	desc.ExternalID = sup.dcfg.ID
	// received_at 由**可信的 Edge** 生成（capability-model.md §4 数据三分法：
	// observed_at 来自设备侧真实采集时刻，由适配器填；received_at 必须 Edge/Core 填）。
	// 设备时钟不可信时消费方以 received_at 为准。
	stampReceived(&desc, time.Now())
	fingerprint := string(mustJSON(semanticDescriptor(desc)))
	data := mustJSON(desc)
	return &api.Envelope{V: api.Version, Type: api.MsgDescriptor, Device: key, Ts: time.Now().Unix(), Data: data}, fingerprint
}

// stampReceived 把 Edge 侧接收/生成时刻写进全部 observation 的 received_at。
// 设备无关：对任何适配器的任何 Capability 一视同仁。
func stampReceived(desc *model.Descriptor, at time.Time) {
	for i := range desc.Entities {
		for k, o := range desc.Entities[i].Observations {
			o.ReceivedAt = at
			desc.Entities[i].Observations[k] = o
		}
	}
}

// semanticDescriptor 返回抹掉 observation 时间戳的深拷贝，仅供 diff 指纹使用。
// 必须深拷贝 Entities 与 Observations：结构体浅拷贝会与原件共享底层数组/map，
// 直接改会污染真正要上报的 payload。
func semanticDescriptor(desc model.Descriptor) model.Descriptor {
	out := desc
	out.Entities = make([]model.Entity, len(desc.Entities))
	copy(out.Entities, desc.Entities)
	for i := range out.Entities {
		if out.Entities[i].Observations == nil {
			continue
		}
		obs := make(map[string]model.Observation, len(out.Entities[i].Observations))
		for k, o := range out.Entities[i].Observations {
			o.ObservedAt = time.Time{}
			o.ReceivedAt = time.Time{}
			obs[k] = o
		}
		out.Entities[i].Observations = obs
	}
	return out
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

// onServerOnline 在（重）连上 server 后强制补报全部设备状态与插件实际态：
// 断线期间状态消息是被丢弃的（幂等），重连后必须主动刷一遍，面板才不会停在旧值。
// 插件面同理：重连即重报 plugin_status（带本次进程的 boot_id 与最新 sequence），
// Server 据此恢复真实 observed；期望态由 Server 在 hello 后重新下发完整快照收敛。
func (e *Edge) onServerOnline() {
	for key, sup := range e.sups {
		e.reportState(key, sup, true)
		e.reportDescriptor(key, sup, true)
	}
	e.reportPluginStatus()
	// Capability 文档随连接重建：Server 侧按 edge 存储，断线即清理，重连必须重报。
	e.reportCapabilities(true)
}

// reportCapabilities 上报本 Edge 当前全部适配器的 Capability 文档（全量覆盖语义）。
//
// 设备无关：Edge 只把适配器自述的文档搬给 Server，不判断任何硬件语义。缺了这条通道，
// 外部 Driver 设备在 WebUI 上只有裸观测值、没有命令面板（/api/capabilities 只会
// 返回 Server 进程内适配器的 catalog）。
func (e *Edge) reportCapabilities(force bool) {
	if e.client == nil {
		return
	}
	data := api.CapabilitiesData{Sources: e.capabilitySources()}
	fp := string(mustJSON(data))
	e.capsMu.Lock()
	changed := fp != e.capsFP
	e.capsMu.Unlock()
	if !force && !changed {
		return
	}
	env := api.Envelope{V: api.Version, Type: api.MsgCapabilities, Ts: time.Now().Unix(), Data: mustJSON(data)}
	if e.client.enqueue(env) || force {
		e.capsMu.Lock()
		e.capsFP = fp
		e.capsMu.Unlock()
	}
	slog.Debug("capabilities reported", "sources", len(data.Sources))
}

// capabilitySources 收集各适配器自述的 Capability 文档，按声明者去重并排序（确定性输出）。
// 一台 Edge 上多台同型号设备共用一个声明者，只上报一份。
func (e *Edge) capabilitySources() []api.CapabilitySource {
	seen := map[string]bool{}
	var out []api.CapabilitySource
	for _, sup := range e.sups {
		if sup == nil || sup.adapter == nil {
			continue
		}
		name := sup.adapter.Name()
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		cp, ok := sup.adapter.(device.CapabilityProvider)
		if !ok {
			continue
		}
		caps := cp.Capabilities()
		if len(caps) == 0 {
			continue
		}
		out = append(out, api.CapabilitySource{Source: name, Capabilities: caps})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Source < out[j].Source })
	return out
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
	result, err := sup.sendForReport(ctx, device.Command{ID: cmd.CommandID, Cmd: cmd.Cmd, Args: cmd.Args})
	if err != nil {
		// 失败路径：错误文本可能带本机路径/凭据形态，出网前统一脱敏。
		ack("failed", SanitizeDetail(err.Error()))
		return
	}
	// 成功路径同样要有可读的执行结果（验收硬项）：detail 非空、限长、脱敏。
	ack("ok", SanitizeDetail(result))
	// 命令多会改状态（sync 对时/dump 转储）：稍候强制补报一次
	time.AfterFunc(1500*time.Millisecond, func() { e.reportState(env.Device, sup, true) })
}
