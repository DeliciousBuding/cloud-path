package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	_ "time/tzdata" // 应用配置里的 IANA 时区名（如 Asia/Shanghai）在任何宿主都可解析

	"github.com/DeliciousBuding/cloud-path/internal/api"
	coreapplication "github.com/DeliciousBuding/cloud-path/internal/application"
	"github.com/DeliciousBuding/cloud-path/internal/appruntime"
	"github.com/DeliciousBuding/cloud-path/internal/plugincontrol"
	"github.com/DeliciousBuding/cloud-path/internal/pluginhost"
	"github.com/DeliciousBuding/cloud-path/internal/registry"
	"github.com/DeliciousBuding/cloud-path/internal/store"
	sdkapplication "github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/application"
)

// AppHost 在 Server 进程内运行 Application Plugin 实例（设计归属见
// docs/architecture/plugin-system.md「Application Plugin → Server Plugin Host」）。
//
// 组成（各积木均已存在，本文件只做接线）：
//   - pluginhost.Manager + plugincontrol.Host：进程面（与 Edge 同款：注册安装物、
//     拉起/监督插件进程、kind 感知地建立 ApplicationClient）；
//   - appruntime.Runtime：协议面（Initialize→Describe→ConfigureInstance→
//     ValidateBinding→HandleEvents 流 + 效果安全边界）；
//   - desired 事实源：Server DB 的 plugin_desired_instances（不是 Edge 期望态、
//     不是 plugincontrol 本地文件态）。
//
// 实例配置约定：plugin_desired_instances.config_json 是 map[string]string（租户安全
// 契约），而应用配置是结构化 JSON。约定应用配置 JSON 字符串放在键 app_config 下，
// AppHost 解出后原样交给 appruntime.ConfigureInstance。
//
// 部署约定：Application 实例的 edge_id 使用保留伪 edge "server"——真实 Edge 不会
// 收到它的期望态（desired 快照按 edge_id 过滤），AppHost 以该 edge_id 上报 observed
// 投影，UI 上即呈现为 server 侧运行的实例。
type AppHost struct {
	srv    *Server
	cfg    AppHostConfig
	logger *slog.Logger

	mgr *pluginhost.Manager
	ph  *plugincontrol.Host
	rt  *appruntime.Runtime

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	bootID string

	mu        sync.Mutex
	running   map[string]*appInstanceRun // instanceID → 运行记录
	ticked    map[string]bool            // "<instance>|<window>|<date>" → 已派发（防重复开窗）
	schedules map[string]map[string]bool // instanceID → 应用声明的 schedule id 集合（任务簿记）
	appCmds   map[int64]appCommandRef    // server 命令 id → 应用侧引用（RequestCompleted 用）
	seq       uint64                     // observed 上报序号
}

// AppHostConfig 是 Server 侧 Application Plugin Host 的配置（Config.AppHost）。
type AppHostConfig struct {
	Enabled    bool
	PluginsDir string
	LockPath   string
	StateDir   string // plugincontrol 进程面本地状态目录（非 desired SSOT）
}

// appConfigKey 是 desired config map 中承载应用配置 JSON 字符串的键。
const appConfigKey = "app_config"

// AppHostEdgeID 是 Server 侧应用实例的部署约定 edge_id：真实 Edge 的 desired
// 快照按 edge_id 过滤，天然不会收到它；AppHost 以该 id 上报 observed 投影。
const AppHostEdgeID = "server"

// appInstanceRun 是一个运行中的应用实例的内存投影。
type appInstanceRun struct {
	row         store.PluginInstanceRow
	tenantStr   string
	reqByEntity map[string]string // entityID → requirementID（事件扇入路由）
	jobIDs      []string          // 应用声明的 job（每分钟驱动一次）
	tz          *time.Location    // 应用配置时区
	windows     []appWindowSpec   // 应用配置的每日窗口
}

// appWindowSpec 是应用配置里的一个每日窗口（HH:MM，配置时区）。
type appWindowSpec struct {
	ID          string `json:"id"`
	Compartment string `json:"compartment"`
	Start       string `json:"start"`
	End         string `json:"end"`
}

// appConfig 是 AppHost 侧需要的应用配置最小投影（应用自身解析完整配置）。
type appConfig struct {
	Timezone string          `json:"timezone"`
	Schedule []appWindowSpec `json:"schedule"`
}

// appCommandRef 把一条 server 命令关联回发起它的应用实例。
type appCommandRef struct {
	InstanceID string
	RequestID  string // 应用侧幂等键（RequestCompleted.RequestID）
	EntityID   string
	Action     string
}

// NewAppHost 构造 AppHost（不启动；调用 Start）。
func NewAppHost(srv *Server, cfg AppHostConfig) (*AppHost, error) {
	logger := slog.Default()
	mgr := pluginhost.NewManager(pluginhost.ManagerOptions{
		Logger:          logger,
		Protocol:        "application",
		ProtocolVersion: 1,
		MaxRestarts:     3,
	})
	ph, err := plugincontrol.NewHost(plugincontrol.HostOptions{
		Manager:    mgr,
		Store:      plugincontrol.NewStore(cfg.StateDir),
		PluginsDir: cfg.PluginsDir,
		LockPath:   cfg.LockPath,
		Logger:     logger,
	})
	if err != nil {
		_ = mgr.Close()
		return nil, fmt.Errorf("apphost: %w", err)
	}
	h := &AppHost{
		srv:       srv,
		cfg:       cfg,
		logger:    logger,
		mgr:       mgr,
		ph:        ph,
		done:      make(chan struct{}),
		bootID:    fmt.Sprintf("server-apphost-%d", time.Now().UnixNano()),
		running:   map[string]*appInstanceRun{},
		ticked:    map[string]bool{},
		schedules: map[string]map[string]bool{},
		appCmds:   map[int64]appCommandRef{},
	}
	rt, err := appruntime.NewRuntime(appruntime.RuntimeOptions{
		Dialer: func(pluginID string) (sdkapplication.ApplicationClient, error) {
			return mgr.ApplicationClient(pluginID)
		},
		Executor: &appEffectExecutor{host: h},
		Logger:   logger,
	})
	if err != nil {
		_ = mgr.Close()
		return nil, fmt.Errorf("apphost: %w", err)
	}
	h.rt = rt
	return h, nil
}

// Start 启动 reconcile/分钟调度/observed 上报三个循环，阻塞至 ctx 取消。
func (h *AppHost) Start(ctx context.Context) error {
	h.ctx, h.cancel = context.WithCancel(ctx)
	defer func() {
		h.cancel()
		_ = h.rt.Close(context.Background())
		_ = h.mgr.Close()
		close(h.done)
	}()

	if err := h.reconcile(h.ctx); err != nil {
		h.logger.Warn("apphost initial reconcile failed", "err", err)
	}

	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); h.reconcileLoop(h.ctx) }()
	go func() { defer wg.Done(); h.minuteLoop(h.ctx) }()
	go func() { defer wg.Done(); h.observedLoop(h.ctx) }()
	wg.Wait()
	return nil
}

// Close 幂等停止（外部主动停机路径）。从未 Start 过时直接释放底层资源。
func (h *AppHost) Close() {
	if h.cancel != nil {
		h.cancel()
		select {
		case <-h.done:
		case <-time.After(10 * time.Second):
		}
		return
	}
	_ = h.rt.Close(context.Background())
	_ = h.mgr.Close()
}

// ctxOrBackground 返回运行根 ctx；Start 之前的钩子调用（测试/极早事件）回落
// Background，避免把 nil ctx 传进 DispatchEvent 的 select。
func (h *AppHost) ctxOrBackground() context.Context {
	if h.ctx != nil {
		return h.ctx
	}
	return context.Background()
}

// ---- reconcile：DB desired → 进程面 + 协议面 ----

func (h *AppHost) reconcileLoop(ctx context.Context) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := h.reconcile(ctx); err != nil {
				h.logger.Warn("apphost reconcile failed", "err", err)
			}
		}
	}
}

func (h *AppHost) reconcile(ctx context.Context) error {
	rows, err := h.srv.cfg.Store.ListPluginInstancesAll()
	if err != nil {
		return fmt.Errorf("list instances: %w", err)
	}
	appPlugins, err := InstalledApplicationPlugins(h.cfg.PluginsDir, h.cfg.LockPath)
	if err != nil {
		return fmt.Errorf("enumerate installed application plugins: %w", err)
	}

	// 进程面：按租户把应用实例期望态收敛给 plugincontrol.Host（注册安装物 + 建/启/停进程）。
	byTenant := map[int64][]api.PluginDesiredInstanceData{}
	for _, r := range rows {
		if _, ok := appPlugins[r.PluginID]; !ok {
			continue
		}
		byTenant[r.TenantID] = append(byTenant[r.TenantID], api.PluginDesiredInstanceData{
			InstanceID: r.InstanceID, PluginID: r.PluginID, Version: r.Version,
			Enabled: r.Enabled, Isolation: r.Isolation,
		})
	}
	tenants := make([]int64, 0, len(byTenant))
	for tid := range byTenant {
		tenants = append(tenants, tid)
	}
	sort.Slice(tenants, func(i, j int) bool { return tenants[i] < tenants[j] })
	for _, tid := range tenants {
		ts := strconv.FormatInt(tid, 10)
		results, err := h.ph.ApplySnapshot(ctx, ts, byTenant[tid])
		if err != nil {
			h.logger.Warn("apphost apply snapshot failed", "tenant", ts, "err", err)
			continue
		}
		for _, res := range results {
			if res.Status != api.PluginAckApplied {
				h.logger.Warn("apphost instance apply result", "instance", res.InstanceID, "status", res.Status, "detail", res.Detail)
			}
		}
	}

	// 协议面：enabled 且已安装的实例 → appruntime 运行；其余 → 停止。
	desired := map[string]store.PluginInstanceRow{}
	for _, r := range rows {
		if _, ok := appPlugins[r.PluginID]; ok && r.Enabled {
			desired[r.InstanceID] = r
		}
	}

	h.mu.Lock()
	var toStop, toStart []store.PluginInstanceRow
	for id := range h.running {
		if _, ok := desired[id]; !ok {
			toStop = append(toStop, h.running[id].row)
		}
	}
	for id, r := range desired {
		run, running := h.running[id]
		if running && run.row.Revision == r.Revision {
			continue // 无变化
		}
		if running {
			toStop = append(toStop, run.row)
		}
		toStart = append(toStart, r)
	}
	h.mu.Unlock()

	for _, r := range toStop {
		h.stopInstance(r.InstanceID)
	}
	for _, r := range toStart {
		if err := h.startInstance(ctx, r); err != nil {
			// 失败不缓存：进程握手是异步的，下一轮 reconcile 自然重试。
			h.logger.Warn("apphost start instance failed", "instance", r.InstanceID, "err", err)
		}
	}
	return nil
}

func (h *AppHost) stopInstance(instanceID string) {
	h.mu.Lock()
	delete(h.running, instanceID)
	delete(h.schedules, instanceID)
	h.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.rt.StopInstance(ctx, instanceID, "desired removed or disabled", 3*time.Second); err != nil {
		h.logger.Warn("apphost stop instance", "instance", instanceID, "err", err)
	}
}

func (h *AppHost) startInstance(ctx context.Context, row store.PluginInstanceRow) error {
	tenantStr := strconv.FormatInt(row.TenantID, 10)
	launchID := "server-apphost-" + row.InstanceID

	cli, err := h.mgr.ApplicationClient(row.PluginID)
	if err != nil {
		return err
	}
	// 预检：拿 requirements 供 Binder 匹配。appruntime.StartInstance 内部会再次
	// Initialize/Describe（应用侧幂等），双跳无害且免去改 appruntime。
	if _, err := cli.Initialize(ctx, &sdkapplication.InitializeRequest{
		PluginID: row.PluginID, PluginVersion: row.Version, LaunchID: launchID,
		ProtocolVersion:           sdkapplication.ProtocolVersion,
		SupportedProtocolVersions: []uint32{sdkapplication.ProtocolVersion},
		NodeID:                    "server", RuntimeType: "server-apphost",
	}); err != nil {
		return fmt.Errorf("pre-initialize: %w", err)
	}
	desc, err := cli.Describe(ctx)
	if err != nil {
		return fmt.Errorf("describe: %w", err)
	}

	reqs := make([]coreapplication.Requirement, 0, len(desc.Requirements))
	for _, r := range desc.Requirements {
		reqs = append(reqs, coreapplication.Requirement{
			ID: r.ID, Capability: r.Capability,
			Cardinality: coreapplication.Cardinality(r.Cardinality),
			MinItems:    int(r.MinItems),
		})
	}
	candidates := h.srv.appCandidates(row.TenantID)
	binder := coreapplication.Binder{ApplicationID: desc.ApplicationID, PluginInstanceID: row.InstanceID, TenantID: tenantStr}
	bs, err := binder.Match(reqs, candidates)
	if err != nil {
		return fmt.Errorf("bind: %w", err)
	}

	if _, err := h.rt.StartInstance(ctx, appruntime.InstanceSpec{
		ApplicationID: desc.ApplicationID, PluginInstanceID: row.InstanceID,
		PluginID: row.PluginID, TenantID: tenantStr, PluginVersion: row.Version,
		LaunchID: launchID, NodeID: "server",
		Config:         appConfigBytes(row.ConfigJSON),
		ConfigRevision: uint32(row.Revision),
		Candidates:     candidates, Bindings: bs.Bindings,
	}); err != nil {
		return fmt.Errorf("start: %w", err)
	}

	run := &appInstanceRun{row: row, tenantStr: tenantStr, reqByEntity: map[string]string{}}
	for _, b := range bs.Bindings {
		run.reqByEntity[b.EntityID] = b.RequirementID
	}
	for _, j := range desc.Jobs {
		run.jobIDs = append(run.jobIDs, j.ID)
	}
	var cfg appConfig
	if raw := appConfigBytes(row.ConfigJSON); len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err == nil {
			if tz, err := time.LoadLocation(cfg.Timezone); err == nil {
				run.tz = tz
			}
			run.windows = cfg.Schedule
		}
	}
	if run.tz == nil {
		run.tz = time.UTC
	}

	h.mu.Lock()
	h.running[row.InstanceID] = run
	h.mu.Unlock()
	h.logger.Info("apphost instance running", "instance", row.InstanceID,
		"plugin", row.PluginID, "version", row.Version, "bindings", len(bs.Bindings))
	return nil
}

// appConfigBytes 从 desired config map（map[string]string JSON）解出应用配置。
// 缺失/非法返回 nil：应用侧 ConfigureInstance 会拒绝空配置，诚实失败。
func appConfigBytes(configJSON string) []byte {
	var m map[string]string
	if err := json.Unmarshal([]byte(configJSON), &m); err != nil {
		return nil
	}
	if v, ok := m[appConfigKey]; ok {
		return []byte(v)
	}
	return nil
}

// ---- 事件扇入（Server ws.go MsgEvent → 这里 → appruntime.DispatchEvent）----

// DispatchDeviceEvent 把一条设备事件路由到绑定了该实体的应用实例。
// nil 接收者安全（未启用 AppHost 时 Server 直接调用）。
func (h *AppHost) DispatchDeviceEvent(deviceTenantID int64, deviceKey, entityID, eventType string, ts int64) {
	if h == nil || entityID == "" {
		return
	}
	h.mu.Lock()
	runs := make([]*appInstanceRun, 0, len(h.running))
	for _, r := range h.running {
		if r.row.TenantID == deviceTenantID {
			runs = append(runs, r)
		}
	}
	h.mu.Unlock()

	occurred := time.Unix(ts, 0).UTC().Format(time.RFC3339)
	for _, run := range runs {
		req, ok := run.reqByEntity[entityID]
		if !ok {
			continue
		}
		err := h.rt.DispatchEvent(h.ctxOrBackground(), run.row.InstanceID, &sdkapplication.ApplicationEvent{
			Union: &sdkapplication.CapabilityEvent{
				RequirementID: req, EntityID: entityID, EventType: eventType,
				PayloadJSON: "{}", OccurredAt: occurred,
			},
		})
		if err != nil {
			h.logger.Warn("apphost dispatch event", "instance", run.row.InstanceID, "entity", entityID, "err", err)
		}
	}
}

// NotifyCommandAck 在应用发起的命令收到最终回执时通知应用（RequestCompleted）。
// nil 接收者安全；非最终态（sent）保留引用，等最终回执再消费。
func (h *AppHost) NotifyCommandAck(commandID int64, status, detail string) {
	if h == nil {
		return
	}
	var state sdkapplication.CommandState
	switch status {
	case "ok":
		state = sdkapplication.CommandStateSucceeded
	case "failed":
		state = sdkapplication.CommandStateFailed
	case "timeout":
		state = sdkapplication.CommandStateTimedOut
	default:
		return
	}
	h.mu.Lock()
	ref, ok := h.appCmds[commandID]
	delete(h.appCmds, commandID)
	h.mu.Unlock()
	if !ok {
		return
	}
	err := h.rt.DispatchEvent(h.ctxOrBackground(), ref.InstanceID, &sdkapplication.ApplicationEvent{
		Union: &sdkapplication.RequestCompleted{
			RequestID: ref.RequestID, EntityID: ref.EntityID, Action: ref.Action,
			State: state, ResultJSON: detail,
		},
	})
	if err != nil {
		h.logger.Warn("apphost dispatch request-completed", "instance", ref.InstanceID, "err", err)
	}
}

// ---- 分钟调度：窗口开启 tick + 声明 job ----

func (h *AppHost) minuteLoop(ctx context.Context) {
	for {
		now := time.Now()
		next := now.Truncate(time.Minute).Add(time.Minute)
		select {
		case <-ctx.Done():
			return
		case <-time.After(next.Sub(now)):
			h.minutePass(next)
		}
	}
}

func (h *AppHost) minutePass(now time.Time) {
	type tickDispatch struct {
		instanceID string
		tick       *sdkapplication.ScheduleTick
	}
	type jobDispatch struct {
		instanceID string
		req        *sdkapplication.RunJobRequest
	}
	h.mu.Lock()
	var ticks []tickDispatch
	var jobs []jobDispatch
	minuteKey := strconv.FormatInt(now.Unix()/60, 10)
	for id, run := range h.running {
		local := now.In(run.tz)
		hhmm := local.Format("15:04")
		date := local.Format("2006-01-02")
		for _, w := range run.windows {
			if w.Start != hhmm {
				continue
			}
			tickKey := id + "|" + w.ID + "|" + date
			if h.ticked[tickKey] {
				continue
			}
			h.ticked[tickKey] = true
			if t := buildWindowTick(w, local, run.tz); t != nil {
				ticks = append(ticks, tickDispatch{instanceID: id, tick: t})
			}
		}
		for _, jobID := range run.jobIDs {
			jobs = append(jobs, jobDispatch{instanceID: id, req: &sdkapplication.RunJobRequest{
				PluginInstanceID: id, JobID: jobID, IdempotencyKey: jobID + "-" + minuteKey,
			}})
		}
	}
	// ticked 只增不减会缓慢膨胀：超限时丢弃非今日键（窗口每日最多开一次，
	// 历史键不再有防重意义）。
	if len(h.ticked) > 4096 {
		today := time.Now().Format("2006-01-02")
		for k := range h.ticked {
			if !hasSuffix(k, today) {
				delete(h.ticked, k)
			}
		}
	}
	h.mu.Unlock()

	for _, t := range ticks {
		if err := h.rt.DispatchEvent(h.ctxOrBackground(), t.instanceID, &sdkapplication.ApplicationEvent{Union: t.tick}); err != nil {
			h.logger.Warn("apphost dispatch schedule tick", "instance", t.instanceID, "err", err)
		} else {
			h.logger.Info("apphost window opened", "instance", t.instanceID, "schedule", t.tick.ScheduleID)
		}
	}
	for _, j := range jobs {
		if _, err := h.rt.RunJob(h.ctx, j.instanceID, j.req); err != nil {
			h.logger.Warn("apphost run job", "instance", j.instanceID, "job", j.req.JobID, "err", err)
		}
	}
}

// buildWindowTick 把当日窗口规格转成应用期望的 WindowJSON
// （RFC3339 start/end，配置时区）。非法时间返回 nil（诚实跳过）。
func buildWindowTick(w appWindowSpec, local time.Time, tz *time.Location) *sdkapplication.ScheduleTick {
	sh, sm, ok1 := parseHHMM(w.Start)
	eh, em, ok2 := parseHHMM(w.End)
	if !ok1 || !ok2 {
		return nil
	}
	start := time.Date(local.Year(), local.Month(), local.Day(), sh, sm, 0, 0, tz)
	end := time.Date(local.Year(), local.Month(), local.Day(), eh, em, 0, 0, tz)
	if !end.After(start) {
		return nil
	}
	payload, err := json.Marshal(map[string]string{
		"id": w.ID, "compartment": w.Compartment,
		"start": start.Format(time.RFC3339), "end": end.Format(time.RFC3339),
	})
	if err != nil {
		return nil
	}
	return &sdkapplication.ScheduleTick{
		ScheduleID: "window-" + w.ID,
		OccurredAt: local.Format(time.RFC3339),
		WindowJSON: string(payload),
	}
}

func parseHHMM(s string) (int, int, bool) {
	if len(s) != 5 || s[2] != ':' {
		return 0, 0, false
	}
	h, err1 := strconv.Atoi(s[0:2])
	m, err2 := strconv.Atoi(s[3:5])
	if err1 != nil || err2 != nil || h > 23 || m > 59 {
		return 0, 0, false
	}
	return h, m, true
}

// ---- observed 投影：以实例 edge_id（约定 "server"）上报插件控制面 ----

func (h *AppHost) observedLoop(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			h.reportObserved()
		}
	}
}

func (h *AppHost) reportObserved() {
	h.mu.Lock()
	h.seq++
	seq := h.seq
	type item struct {
		row store.PluginInstanceRow
		st  appruntime.InstanceState
	}
	var items []item
	for id, run := range h.running {
		st := appruntime.StateRunning
		if inst, err := h.rt.GetInstance(id); err == nil && inst != nil {
			st = inst.State
		}
		items = append(items, item{row: run.row, st: st})
	}
	h.mu.Unlock()

	if h.srv.cfg.PluginStore == nil || len(items) == 0 {
		return
	}
	// 按 (tenant, edge_id) 聚合：observed 投影以伪 edge（约定 AppHostEdgeID）为键。
	type groupKey struct {
		tid    int64
		edgeID string
	}
	groups := map[groupKey][]api.PluginObservedInstanceData{}
	for _, it := range items {
		snap := h.managerSnapshot(it.row)
		k := groupKey{tid: it.row.TenantID, edgeID: it.row.EdgeID}
		groups[k] = append(groups[k], api.PluginObservedInstanceData{
			InstanceID: it.row.InstanceID, PluginID: it.row.PluginID, Version: it.row.Version,
			HostOnline: true, State: string(it.st), Health: snap.health,
			Detail: "server-apphost", RestartCount: snap.restartCount,
		})
	}
	for k, rows := range groups {
		// 事实：AppHost 是 Server 进程内的本地宿主，desired 快照由本进程写入并
		// 已成功收敛（apply snapshot + 实例 running），applied 即 desired、drift 恒否。
		h.srv.plugin.applyAppHostObservations(k.tid, k.edgeID, h.bootID, seq, rows)
	}
}

// runningTenantIDs 返回当前承载着应用实例的租户（伪 edge 在线判定的事实源：
// 只标真实承载的租户，不虚标无实例的租户）。
func (h *AppHost) runningTenantIDs() []int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	seen := map[int64]bool{}
	out := make([]int64, 0, 2)
	for _, run := range h.running {
		if !seen[run.row.TenantID] {
			seen[run.row.TenantID] = true
			out = append(out, run.row.TenantID)
		}
	}
	return out
}

// hasSuffix 报告 s 是否以 suffix 结尾。
func hasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

// managerSnapshot 取进程面的健康/重启计数（找不到时零值）。
func (h *AppHost) managerSnapshot(row store.PluginInstanceRow) struct {
	health       string
	restartCount int
} {
	out := struct {
		health       string
		restartCount int
	}{health: pluginhost.HealthUnknown.String()}
	for _, snap := range h.mgr.ListInstances(strconv.FormatInt(row.TenantID, 10)) {
		if snap.InstanceID == row.InstanceID {
			// Health.String()（"UNKNOWN/HEALTHY/DEGRADED"）：直接 string(枚举)
			// 会把 uint8 零值变成 "\x00" 混进 API 响应（2026-09-05 E2E 实测）
			out.health = snap.Health.String()
			out.restartCount = snap.Restarts
			break
		}
	}
	return out
}

// ---- 效果执行器（Core 侧）----

type appEffectExecutor struct {
	host *AppHost
}

func (e *appEffectExecutor) Execute(ctx context.Context, effect appruntime.Effect) error {
	switch effect.Kind {
	case appruntime.EffectRequestCommand:
		return e.execRequestCommand(ctx, effect)
	case appruntime.EffectCreateDomainRecord:
		tid, err := strconv.ParseInt(effect.TenantID, 10, 64)
		if err != nil {
			return fmt.Errorf("apphost: effect tenant %q: %w", effect.TenantID, err)
		}
		p := effect.CreateDomainRecord
		return e.host.srv.cfg.Store.UpsertAppDomainRecord(tid, effect.PluginInstanceID,
			p.RecordType, p.RecordID, p.DataJSON, p.Version, time.Now().Unix())
	case appruntime.EffectScheduleJob:
		// 任务簿记：应用声明的定时任务记录在册（cancel_job 可撤销）。
		// 声明 job（descriptor.Jobs）由分钟循环驱动执行；schedule payload 的
		// 独立 cron 执行是后续自动化引擎（Milestone D）的工作，这里不臆造。
		e.host.mu.Lock()
		set := e.host.schedules[effect.PluginInstanceID]
		if set == nil {
			set = map[string]bool{}
			e.host.schedules[effect.PluginInstanceID] = set
		}
		set[effect.ScheduleJob.ScheduleID] = true
		e.host.mu.Unlock()
		e.host.logger.Info("apphost schedule registered", "instance", effect.PluginInstanceID,
			"schedule", effect.ScheduleJob.ScheduleID, "cron", effect.ScheduleJob.Cron)
		return nil
	case appruntime.EffectCancelJob:
		e.host.mu.Lock()
		delete(e.host.schedules[effect.PluginInstanceID], effect.CancelJob.ScheduleID)
		e.host.mu.Unlock()
		return nil
	case appruntime.EffectSendNotification:
		// 通知通道（Connector/Notification）属生态扩展阶段：现在如实记录，
		// 不伪造「已通知」。
		e.host.logger.Warn("app notification (通道未建，仅记录)",
			"instance", effect.PluginInstanceID, "title", effect.SendNotification.Title,
			"severity", effect.SendNotification.Severity)
		return nil
	default:
		return fmt.Errorf("apphost: unhandled effect kind %q", effect.Kind)
	}
}

func (e *appEffectExecutor) execRequestCommand(ctx context.Context, effect appruntime.Effect) error {
	p := effect.RequestCommand
	tid, err := strconv.ParseInt(effect.TenantID, 10, 64)
	if err != nil {
		return fmt.Errorf("apphost: effect tenant %q: %w", effect.TenantID, err)
	}
	deviceKey := e.host.srv.deviceKeyForEntity(p.EntityID)
	if deviceKey == "" {
		return fmt.Errorf("apphost: entity %q not found on any device", p.EntityID)
	}
	cmdID, err := e.host.srv.dispatchDeviceCommand(ctx, tid, deviceKey, p.Action, p.ArgsJSON)
	if err != nil {
		return err
	}
	e.host.mu.Lock()
	e.host.appCmds[cmdID] = appCommandRef{
		InstanceID: effect.PluginInstanceID, RequestID: p.IdempotencyKey,
		EntityID: p.EntityID, Action: p.Action,
	}
	e.host.mu.Unlock()
	return nil
}

// ---- Server 侧辅助（AppHost 接线所需）----

// appCandidates 构造某租户的全部可绑定实体（来自最近 Descriptor 快照）。
//
// 已知边界：Candidate.EntityID 是设备内局部 ID（如 key1）；多台同型号设备会出现
// 同名实体，当前按先注册者绑定、命令按首个匹配设备下发。协议层引入限定实体 ID
// （device/entity）前不扩展——单一 reference 设备场景下语义正确。
func (s *Server) appCandidates(tenantID int64) []coreapplication.Candidate {
	tenantStr := strconv.FormatInt(tenantID, 10)
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]coreapplication.Candidate, 0, 32)
	for key, desc := range s.descriptors {
		for _, e := range desc.Entities {
			out = append(out, coreapplication.Candidate{
				EntityID: e.EntityID, Name: e.Name, TenantID: tenantStr,
				Capabilities: e.Capabilities, DeviceID: key,
			})
		}
	}
	return out
}

// deviceKeyForEntity 返回提供该实体的设备 key（"<edge>/<dev>"）；找不到为空。
func (s *Server) deviceKeyForEntity(entityID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for key, desc := range s.descriptors {
		for _, e := range desc.Entities {
			if e.EntityID == entityID {
				return key
			}
		}
	}
	return ""
}

// dispatchDeviceCommand 是应用效果 → 设备命令的下发内核（与 handlePostCommand
// 同一路径：建命令行 + edge 链路投递）。区别：不做进程内适配器白名单——外部
// Driver 设备本就无注册表项，命令合法性由 Edge/Driver 侧校验；应用可下发的
// 动作已被 Capability 绑定约束。
func (s *Server) dispatchDeviceCommand(ctx context.Context, tenantID int64, key, cmd, args string) (int64, error) {
	s.mu.RLock()
	v, devOK := s.devices[key]
	var link *edgeLink
	if devOK {
		if l, ok := s.edges[v.EdgeID]; ok && l.tenantID == tenantID {
			link = l
		}
	}
	s.mu.RUnlock()
	if !devOK {
		return 0, fmt.Errorf("device not found: %s", key)
	}
	if link == nil {
		return 0, fmt.Errorf("edge offline: %s", v.EdgeID)
	}
	if s.cfg.Store == nil {
		return 0, fmt.Errorf("store unavailable")
	}
	id, err := s.cfg.Store.CreateCommandTenant(key, cmd, args, tenantID)
	if err != nil {
		return 0, err
	}
	data, _ := json.Marshal(api.CommandData{CommandID: id, Cmd: cmd, Args: args})
	payload, _ := json.Marshal(api.Envelope{V: api.Version, Type: api.MsgCommand, Device: key, Ts: time.Now().Unix(), Data: data})
	select {
	case link.send <- payload:
		_, err = s.cfg.Store.UpdateCommandStatusScoped(id, key, tenantID, "sent", "")
		return id, err
	default:
		_, _ = s.cfg.Store.UpdateCommandStatusScoped(id, key, tenantID, "failed", "edge 发送队列满")
		return 0, fmt.Errorf("edge send queue full")
	}
}

// InstalledApplicationPlugins 返回已安装的 Application kind 插件集合（pluginID → version）。
func InstalledApplicationPlugins(pluginsDir, lockPath string) (map[string]string, error) {
	lock, err := registry.LoadLockFile(lockPath)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, locked := range lock.Plugins {
		manifestPath := filepath.Join(pluginsDir, registry.SafePluginID(locked.ID), "plugin.yaml")
		manifest, err := registry.LoadManifest(manifestPath)
		if err != nil {
			continue
		}
		if kind, err := pluginhost.ParseKind(manifest.Kind); err == nil && kind == pluginhost.KindApplication {
			out[locked.ID] = locked.Version
		}
	}
	return out, nil
}
