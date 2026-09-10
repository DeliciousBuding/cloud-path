package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
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

	mu      sync.Mutex
	running map[appInstKey]*appInstanceRun    // (tenant, instanceID) → 运行记录
	failed  map[appInstKey]appInstanceFailure // (tenant, instanceID) → 启动/绑定失败投影
	appCmds map[int64]appCommandRef           // server 命令 id → 应用侧引用（RequestCompleted 用）
	seq     uint64                            // observed 上报序号
}

// appInstanceFailure 是 AppHost 在 desired 已存在但协议/绑定启动失败时的
// 可观测事实；它不是 desired，必须只进入 observed 投影。
type appInstanceFailure struct {
	row    store.PluginInstanceRow
	detail string
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
// 同一个标识符也用作应用运行时的 NodeID（中心服务就是一个节点）。
// 生产代码不得再写裸 "server" 字面量——那正是 overview.go 曾经绕过这个常量的方式；
// 测试里保留字面量是有意的，用来钉住常量对外的值。
const AppHostEdgeID = "server"

// appInstanceRun 是一个运行中的应用实例的内存投影。
type appInstanceRun struct {
	row            store.PluginInstanceRow
	tenantStr      string
	bindings       []api.AppBindingView           // 启动时 Binder 权威匹配的绑定快照（D1 读面）
	requirements   []coreapplication.Requirement  // 设备选择器读面
	candidates     []coreapplication.Candidate    // 设备选择器读面
	jobIDs         []string                       // 应用声明的 job（包含手动操作）
	jobDescriptors []sdkapplication.JobDescriptor // immutable runtime declaration snapshot
	tz             *time.Location                 // 应用配置声明的时区（通用 durable schedule_job 使用）
}

// appConfig 是 AppHost 为通用 durable schedule_job 读取的最小配置投影。
// 窗口等业务字段由应用自身解析，Core 不保留业务副本。
type appConfig struct {
	Timezone string `json:"timezone"`
}

// appCommandRef 把一条 server 命令关联回发起它的应用实例。
// appInstKey 是协议面里一个实例的身份。store 主键是 (tenant_id, edge_id,
// instance_id)，instance id 只在租户内唯一，所以运行记录、开窗去重与 appruntime
// 都必须带租户；否则两个租户的同名实例会互相覆盖，其中一个被静默饿死。
// edge_id 不进键：reconcile 只处理部署到伪 edge AppHostEdgeID 的实例。
type appInstKey struct {
	tenantID   int64
	instanceID string
}

// tenantStr 返回 appruntime 使用的租户字符串（与 InstanceSpec.TenantID 同形）。
func (k appInstKey) tenantStr() string { return strconv.FormatInt(k.tenantID, 10) }

type appCommandRef struct {
	TenantID   string
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
		srv:     srv,
		cfg:     cfg,
		logger:  logger,
		mgr:     mgr,
		ph:      ph,
		done:    make(chan struct{}),
		bootID:  fmt.Sprintf("server-apphost-%d", time.Now().UnixNano()),
		running: map[appInstKey]*appInstanceRun{},
		failed:  map[appInstKey]appInstanceFailure{},
		appCmds: map[int64]appCommandRef{},
	}
	rt, err := appruntime.NewRuntime(appruntime.RuntimeOptions{
		Dialer:   h.applicationClient,
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
	// 启动即处理声明式任务：停机期间错过的 run 在此按 missed-run policy 收敛
	h.runScheduledJobs(time.Now())

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

// ---- D1 Application Data Plane：运行态内省（bindings / jobs）----

// InstanceBindings 返回实例的 Capability 绑定投影（运行态）。ok=false 表示
// 实例未运行或 AppHost 未启用——绑定只存在于实例运行期间。
// instance id 不跨租户全局唯一，因此运行记录按 (tenant, instance) 寻址。
func (h *AppHost) InstanceBindings(tenantID int64, instanceID string) (api.AppBindingsView, bool) {
	if h == nil {
		return api.AppBindingsView{InstanceID: instanceID}, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	run, ok := h.running[appInstKey{tenantID, instanceID}]
	if !ok {
		return api.AppBindingsView{InstanceID: instanceID}, false
	}
	view := api.AppBindingsView{
		InstanceID: instanceID,
		Running:    true,
		Bindings:   append([]api.AppBindingView(nil), run.bindings...),
	}
	for _, req := range run.requirements {
		view.Requirements = append(view.Requirements, api.AppBindingRequirementView{
			ID: req.ID, Capability: req.Capability, Cardinality: string(req.Cardinality),
			MinItems: req.MinItems, AllowReuse: req.AllowReuse,
		})
	}
	for _, candidate := range run.candidates {
		view.Candidates = append(view.Candidates, api.AppBindingCandidateView{
			EntityID: candidate.EntityID, DeviceID: candidate.DeviceID, Name: candidate.Name,
			Capabilities: append([]string(nil), candidate.Capabilities...),
		})
	}
	return view, true
}

// InstanceJobs 返回实例声明的 job id 列表（运行态）。
func (h *AppHost) InstanceJobs(tenantID int64, instanceID string) ([]string, bool) {
	if h == nil {
		return nil, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	run, ok := h.running[appInstKey{tenantID, instanceID}]
	if !ok {
		return nil, false
	}
	out := append([]string(nil), run.jobIDs...)
	return out, true
}

// broadcastDomainRecord 把领域记录写入投影成 WS 消息，按租户广播给浏览器。
// 投影失败（slug 解析 / 序列化）只记日志，绝不影响记录落库主路径。
func (h *AppHost) broadcastDomainRecord(tenantID int64, instanceID string, p *appruntime.CreateDomainRecord, created bool, updatedAt int64) {
	t, err := h.srv.cfg.Store.GetTenantByID(tenantID)
	if err != nil {
		h.logger.Warn("apphost domain record projection: tenant lookup", "tenant", tenantID, "err", err)
		return
	}
	data, err := json.Marshal(api.DomainRecordData{
		InstanceID: instanceID,
		RecordType: p.RecordType,
		RecordID:   p.RecordID,
		DataJSON:   p.DataJSON,
		Version:    p.Version,
		UpdatedAt:  updatedAt,
		Created:    created,
	})
	if err != nil {
		h.logger.Warn("apphost domain record projection: marshal", "instance", instanceID, "err", err)
		return
	}
	h.srv.broadcastAs(api.Envelope{
		V: api.Version, Type: api.MsgDomainRecord, Ts: updatedAt, Data: data,
	}, t.Slug)
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

// serverHostedRows 只保留部署到 Server 伪 edge 的期望态行。真实 Edge 的 desired
// 由该 Edge 自己收敛；AppHost 若也应用一遍，就会在 Server 上多跑一份属于 Edge 的
// 实例。这是 AppHost 唯一的部署边界，进程面与协议面共用同一次过滤。
func serverHostedRows(rows []store.PluginInstanceRow) []store.PluginInstanceRow {
	out := make([]store.PluginInstanceRow, 0, len(rows))
	for _, r := range rows {
		if r.EdgeID == AppHostEdgeID {
			out = append(out, r)
		}
	}
	return out
}

// desiredProtocolInstances 选出协议面应当运行的实例：插件已安装且期望态 enabled。
// 键带租户——instance id 只在租户内唯一，按裸 id 建键会让两个租户的同名实例互相
// 覆盖，其中一个被静默饿死。
func desiredProtocolInstances(rows []store.PluginInstanceRow, appPlugins map[string]string) map[appInstKey]store.PluginInstanceRow {
	desired := map[appInstKey]store.PluginInstanceRow{}
	for _, r := range rows {
		if _, ok := appPlugins[r.PluginID]; ok && r.Enabled {
			desired[appInstKey{r.TenantID, r.InstanceID}] = r
		}
	}
	return desired
}

func (h *AppHost) reconcile(ctx context.Context) error {
	rows, err := h.srv.cfg.Store.ListPluginInstancesAll()
	if err != nil {
		return fmt.Errorf("list instances: %w", err)
	}
	rows = serverHostedRows(rows)
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
	applied := map[int64]map[string]bool{}
	for _, tid := range tenants {
		ts := strconv.FormatInt(tid, 10)
		results, err := h.ph.ApplySnapshot(ctx, ts, byTenant[tid])
		if err != nil {
			h.logger.Warn("apphost apply snapshot failed", "tenant", ts, "err", err)
			continue
		}
		applied[tid] = map[string]bool{}
		for _, res := range results {
			applied[tid][res.InstanceID] = res.Status == api.PluginAckApplied
			if res.Status != api.PluginAckApplied {
				h.logger.Warn("apphost instance apply result", "instance", res.InstanceID, "status", res.Status, "detail", res.Detail)
			}
		}
	}

	// 协议面：enabled 且已安装的实例 → appruntime 运行；其余 → 停止。
	desired := desiredProtocolInstances(rows, appPlugins)

	h.mu.Lock()
	var toStop, toStart []store.PluginInstanceRow
	for key := range h.running {
		if _, ok := desired[key]; !ok {
			toStop = append(toStop, h.running[key].row)
		}
	}
	for key := range h.failed {
		if _, ok := desired[key]; !ok {
			delete(h.failed, key)
		}
	}
	for key, r := range desired {
		if !applied[r.TenantID][key.instanceID] {
			continue // Keep the prior protocol instance; do not promote a failed apply.
		}
		run, running := h.running[key]
		if running && run.row.Revision == r.Revision {
			// desired 无变化，但实际态可能已失活（共享进程被连带杀死、
			// 插件进程崩溃后流断开等）。reconcile 必须自愈：否则 failed
			// 实例永远躺着（2026-09-05 生产环境实测：box-prod failed 后
			// 90 分钟无人重启，窗口照开但 job/事件全部丢弃）。
			alive := false
			if inst, err := h.rt.GetInstance(key.tenantStr(), key.instanceID); err == nil {
				alive = inst.State == appruntime.StateRunning || inst.State == appruntime.StateStarting
			}
			if alive {
				continue
			}
			h.logger.Warn("apphost instance not running, healing", "instance", key.instanceID, "tenant", key.tenantID)
			// 落到下面的 stop+start 路径重建会话
		}
		if running {
			toStop = append(toStop, run.row)
		}
		toStart = append(toStart, r)
	}
	h.mu.Unlock()

	for _, r := range toStop {
		h.stopInstance(appInstKey{r.TenantID, r.InstanceID})
	}
	for _, r := range toStart {
		if err := h.startInstance(ctx, r); err != nil {
			// 失败会保留为 observed 事实；下一轮 reconcile 仍会自然重试。
			h.recordInstanceFailure(r, err)
			h.logger.Warn("apphost start instance failed", "instance", r.InstanceID, "err", err)
		}
	}
	return nil
}

func (h *AppHost) stopInstance(key appInstKey) {
	h.mu.Lock()
	pluginID := ""
	if run, ok := h.running[key]; ok {
		pluginID = run.row.PluginID
	}
	delete(h.running, key)
	delete(h.failed, key)
	siblings := 0
	if pluginID != "" {
		for _, run := range h.running {
			if run.row.PluginID == pluginID {
				siblings++
			}
		}
	}
	h.mu.Unlock()

	if siblings == 0 {
		// 该插件的最后一个实例：进程级优雅关停（Shutdown RPC 对参考应用
		// 是进程退出信号，此时发送才是安全的）
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := h.rt.StopInstance(ctx, key.tenantStr(), key.instanceID, "desired removed or disabled", 3*time.Second); err != nil {
			h.logger.Warn("apphost stop instance", "instance", key.instanceID, "tenant", key.tenantID, "err", err)
		}
		return
	}
	// 共享进程还有兄弟实例：只拆本实例会话。绝不能发 Shutdown RPC——
	// 2026-09-05 生产环境实测：删除兄弟实例触发进程退出，同进程的
	// box-prod 被连带杀死 → state=failed。
	if err := h.rt.StopInstanceStreamOnly(key.tenantStr(), key.instanceID); err != nil {
		h.logger.Warn("apphost stop instance (stream only)", "instance", key.instanceID, "tenant", key.tenantID, "err", err)
	}
}

// applicationClient pins protocol work to the same tenant/instance definition
// that the process reconciler applied. A stale binding must not receive a new
// desired version's Initialize/Configure calls merely because plugin IDs match.
func (h *AppHost) applicationClient(spec appruntime.InstanceSpec) (sdkapplication.ApplicationClient, error) {
	snap, err := h.mgr.Snapshot(spec.TenantID, spec.PluginInstanceID)
	if err != nil {
		return nil, err
	}
	if !snap.Enabled || snap.PluginID != spec.PluginID || snap.Version != spec.PluginVersion {
		return nil, fmt.Errorf("apphost: process definition not converged for instance %s/%s", spec.TenantID, spec.PluginInstanceID)
	}
	return h.mgr.ApplicationClientForInstance(spec.TenantID, spec.PluginInstanceID)
}

func (h *AppHost) startInstance(ctx context.Context, row store.PluginInstanceRow) error {
	tenantStr := strconv.FormatInt(row.TenantID, 10)
	launchID := "server-apphost-" + row.InstanceID
	spec := appruntime.InstanceSpec{
		PluginInstanceID: row.InstanceID, PluginID: row.PluginID, TenantID: tenantStr,
		PluginVersion: row.Version, LaunchID: launchID, NodeID: AppHostEdgeID,
		Config: appConfigBytes(row.ConfigJSON), ConfigRevision: uint32(row.Revision),
	}

	cli, err := h.applicationClient(spec)
	if err != nil {
		return err
	}
	// 预检：拿 requirements 供 Binder 匹配。appruntime.StartInstance 内部会再次
	// Initialize/Describe（应用侧幂等），双跳无害且免去改 appruntime。
	if _, err := cli.Initialize(ctx, &sdkapplication.InitializeRequest{
		PluginID: row.PluginID, PluginVersion: row.Version, LaunchID: launchID,
		ProtocolVersion:           sdkapplication.ProtocolVersion,
		SupportedProtocolVersions: []uint32{sdkapplication.ProtocolVersion},
		NodeID:                    AppHostEdgeID, RuntimeType: "server-apphost",
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
	bs, err := selectApplicationBindings(binder, reqs, candidates, row.ConfigJSON)
	if err != nil {
		return fmt.Errorf("bind: %w", err)
	}

	reqCap := make(map[string]string, len(reqs))
	for _, rq := range reqs {
		reqCap[rq.ID] = rq.Capability
	}
	if err := bindingConflictError(h.bindingConflicts(row.TenantID, row.InstanceID, bs.Bindings, reqCap)); err != nil {
		return err
	}

	spec.ApplicationID = desc.ApplicationID
	spec.Candidates = candidates
	spec.Bindings = bs.Bindings
	if _, err := h.rt.StartInstance(ctx, spec); err != nil {
		return fmt.Errorf("start: %w", err)
	}

	run := &appInstanceRun{
		row: row, tenantStr: tenantStr,
		requirements: append([]coreapplication.Requirement(nil), reqs...),
		candidates:   append([]coreapplication.Candidate(nil), candidates...),
	}
	for _, b := range bs.Bindings {
		run.bindings = append(run.bindings, api.AppBindingView{
			RequirementID: b.RequirementID,
			Capability:    reqCap[b.RequirementID],
			EntityID:      b.EntityID,
			DeviceID:      b.DeviceID,
		})
	}
	for _, j := range desc.Jobs {
		run.jobIDs = append(run.jobIDs, j.ID)
		run.jobDescriptors = append(run.jobDescriptors, j)
	}
	var cfg appConfig
	if raw := appConfigBytes(row.ConfigJSON); len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err == nil {
			if tz, err := time.LoadLocation(cfg.Timezone); err == nil {
				run.tz = tz
			}
		}
	}
	if run.tz == nil {
		run.tz = time.UTC
	}

	h.mu.Lock()
	key := appInstKey{row.TenantID, row.InstanceID}
	h.running[key] = run
	delete(h.failed, key)
	h.mu.Unlock()
	h.logger.Info("apphost instance running", "instance", row.InstanceID,
		"plugin", row.PluginID, "version", row.Version, "bindings", len(bs.Bindings))
	// 启动成功后立即投影一次 observed：生产 reconcile 也经由此路径，避免实例已 running
	// 但控制面最多 30s 仍显示旧状态。此处已释放 h.mu；reportObserved 自行取锁。
	h.reportObserved()
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

// routedEvent 是一条设备事件路由结果：哪个实例、经哪个 requirement 收到该实体。
type routedEvent struct {
	run *appInstanceRun
	req string
}

// routeDeviceEvent 返回应收到该设备事件的应用路由：同租户且绑定该实体的实例。
// 隔离在此层强制——跨租户设备的 event 绝不路由给其他租户的应用，未绑定实体
// 不投递。纯函数（只读 h.running 快照），是 DispatchDeviceEvent 的可测内核。
func (h *AppHost) routeDeviceEvent(deviceTenantID int64, deviceKey, entityID string) []routedEvent {
	if entityID == "" {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []routedEvent
	for _, r := range h.running {
		if r.row.TenantID != deviceTenantID {
			continue
		}
		for _, binding := range r.bindings {
			if binding.EntityID != entityID {
				continue
			}
			if binding.DeviceID != "" && binding.DeviceID != deviceKey {
				continue
			}
			out = append(out, routedEvent{run: r, req: binding.RequirementID})
		}
	}
	return out
}

// DispatchDeviceEvent 把一条设备事件路由到绑定了该实体的应用实例。
// nil 接收者安全（未启用 AppHost 时 Server 直接调用）。
// 成功路由与无路由都留痕：实体事件是稀疏高信号面（key/hall/vib/nav），
// 静默丢弃会让「按了没反应」类问题无从排查（2026-09-05 D3 真板实测教训）。
func (h *AppHost) DispatchDeviceEvent(deviceTenantID int64, deviceKey, entityID, eventType string, ts int64) {
	if h == nil || entityID == "" {
		return
	}
	routes := h.routeDeviceEvent(deviceTenantID, deviceKey, entityID)
	if len(routes) == 0 {
		h.logger.Info("apphost event unrouted", "entity", entityID,
			"type", eventType, "device", deviceKey, "tenant", deviceTenantID)
		return
	}
	occurred := time.Unix(ts, 0).UTC().Format(time.RFC3339)
	for _, route := range routes {
		err := h.rt.DispatchEvent(h.ctxOrBackground(), route.run.tenantStr, route.run.row.InstanceID, &sdkapplication.ApplicationEvent{
			Union: &sdkapplication.CapabilityEvent{
				RequirementID: route.req, EntityID: entityID, EventType: eventType,
				PayloadJSON: "{}", OccurredAt: occurred,
			},
		})
		if err != nil {
			h.logger.Warn("apphost dispatch event", "instance", route.run.row.InstanceID, "entity", entityID, "err", err)
		} else {
			h.logger.Info("apphost dispatch event", "instance", route.run.row.InstanceID,
				"entity", entityID, "type", eventType, "requirement", route.req)
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
	h.dispatchRequestCompleted(ref, state, detail)
}

// dispatchRequestCompleted 把一个应用发起的命令终态送回应用事件流。
func (h *AppHost) dispatchRequestCompleted(ref appCommandRef, state sdkapplication.CommandState, detail string) {
	err := h.rt.DispatchEvent(h.ctxOrBackground(), ref.TenantID, ref.InstanceID, &sdkapplication.ApplicationEvent{
		Union: &sdkapplication.RequestCompleted{
			RequestID: ref.RequestID, EntityID: ref.EntityID, Action: ref.Action,
			State: state, ResultJSON: detail,
		},
	})
	if err != nil {
		h.logger.Warn("apphost dispatch request-completed", "instance", ref.InstanceID, "err", err)
	}
}

// ---- 分钟调度：自动 job + 通用 durable job ----

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
	type jobDispatch struct {
		tenantStr  string
		instanceID string
		req        *sdkapplication.RunJobRequest
	}
	h.mu.Lock()
	var jobs []jobDispatch
	minuteKey := strconv.FormatInt(now.Unix()/60, 10)
	for key, run := range h.running {
		id := key.instanceID
		for _, jobID := range run.automaticJobs() {
			jobs = append(jobs, jobDispatch{tenantStr: run.tenantStr, instanceID: id, req: &sdkapplication.RunJobRequest{
				PluginInstanceID: id, JobID: jobID, IdempotencyKey: jobID + "-" + minuteKey,
			}})
		}
	}
	h.mu.Unlock()

	for _, j := range jobs {
		if _, err := h.rt.RunJob(h.ctxOrBackground(), j.tenantStr, j.instanceID, j.req); err != nil {
			h.logger.Warn("apphost run job", "instance", j.instanceID, "job", j.req.JobID, "err", err)
		}
	}

	// D2 Durable Scheduler：声明式 cron 任务（schedule_job 效果声明，DB 持久）
	h.runScheduledJobs(now)
}

// runScheduledJobs 扫描到期声明式任务并派发。每分钟（及启动时）调用一次；
// 语义见 dispatchScheduledJob（claim-then-dispatch + missed-run policy）。
func (h *AppHost) runScheduledJobs(now time.Time) {
	st := h.srv.cfg.Store
	if st == nil {
		return
	}
	due, err := st.ListScheduledJobsDue(now.Unix())
	if err != nil {
		h.logger.Warn("apphost scheduled jobs scan", "err", err)
		return
	}
	for _, row := range due {
		h.dispatchScheduledJob(st, row, now)
	}
}

// scheduledMissedGrace 是「正常准点」与「停机错过」的分界：到期不超过该值的
// 视为正常触发（minutePass 的分钟粒度 + 少量调度延迟）；超过即视为停机期间
// 错过，走 missed-run policy。
const scheduledMissedGrace = 90

// dispatchScheduledJob 处理一条到期声明式任务。核心语义：
//
//   - claim-then-dispatch：先把 next_run_at / last_run_at / last_dispatch 持久
//     推进，再派发。崩溃在 claim 之后 → 该次运行静默跳过（at-most-once），
//     绝不重放——与 Edge 重连「只收敛最终态，不回放过期副作用」同一哲学。
//   - 节奏保持：推进从原计划时刻起算（不从 now），停机不漂移 cron 节奏。
//   - missed-run policy：停机错过的 run，skip（默认）= 只推进不派发；
//     run_once = 补派发恰好一次（不管错过了几个周期）。
func (h *AppHost) dispatchScheduledJob(st *store.Store, row store.ScheduledJobRow, now time.Time) {
	expr, err := parseCronExpr(row.Cron)
	if err != nil {
		// 非法 cron（上游版本变更等）：撤销以免每分钟热循环
		h.logger.Warn("apphost scheduled job bad cron, cancelling", "instance", row.InstanceID,
			"schedule", row.ScheduleID, "cron", row.Cron, "err", err)
		_ = st.CancelScheduledJob(row.TenantID, row.InstanceID, row.ScheduleID)
		return
	}
	tz, err := time.LoadLocation(row.Timezone)
	if err != nil {
		tz = time.UTC
	}
	next := expr.nextAfter(time.Unix(row.NextRunAt, 0), tz)
	for !next.IsZero() && next.Unix() <= now.Unix() {
		next = expr.nextAfter(next, tz)
	}
	if next.IsZero() {
		// 表达式在扫描视野内再无触发（如 2 月 30 日）：撤销
		h.logger.Warn("apphost scheduled job has no future occurrence, cancelling",
			"instance", row.InstanceID, "schedule", row.ScheduleID)
		_ = st.CancelScheduledJob(row.TenantID, row.InstanceID, row.ScheduleID)
		return
	}
	dispatch := now.Unix()-row.NextRunAt <= scheduledMissedGrace || row.MissedPolicy == "run_once"
	dispatchKey := ""
	if dispatch {
		dispatchKey = fmt.Sprintf("sj|%s|%s|%d", row.InstanceID, row.ScheduleID, row.NextRunAt)
	}
	if err := st.ClaimScheduledJobRun(row.TenantID, row.InstanceID, row.ScheduleID,
		next.Unix(), row.NextRunAt, dispatchKey); err != nil {
		h.logger.Warn("apphost scheduled job claim", "instance", row.InstanceID,
			"schedule", row.ScheduleID, "err", err)
		return
	}
	if !dispatch {
		h.logger.Info("apphost scheduled job run missed (skip policy)",
			"instance", row.InstanceID, "schedule", row.ScheduleID,
			"planned_at", row.NextRunAt, "next_run_at", next.Unix())
		return
	}
	if _, err := h.rt.RunJob(h.ctxOrBackground(), strconv.FormatInt(row.TenantID, 10), row.InstanceID, &sdkapplication.RunJobRequest{
		PluginInstanceID: row.InstanceID,
		JobID:            row.ScheduleID,
		JobType:          "scheduled",
		ArgsJSON:         row.PayloadJSON,
		IdempotencyKey:   dispatchKey,
	}); err != nil {
		// 已 claim（at-most-once）：本轮不重试，下轮自然进入下一计划时刻
		h.logger.Warn("apphost scheduled job dispatch", "instance", row.InstanceID,
			"schedule", row.ScheduleID, "err", err)
		return
	}
	h.logger.Info("apphost scheduled job dispatched", "instance", row.InstanceID,
		"schedule", row.ScheduleID, "planned_at", row.NextRunAt, "next_run_at", next.Unix())
}

// instanceTimezone 返回实例配置时区（schedule_job 效果声明时取用）；实例不
// 在运行（理论不可达：效果来自运行中的实例）或无配置 → UTC。
func (h *AppHost) instanceTimezone(tenantStr, instanceID string) *time.Location {
	tid, err := strconv.ParseInt(tenantStr, 10, 64)
	if err != nil {
		return time.UTC
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if run, ok := h.running[appInstKey{tid, instanceID}]; ok && run.tz != nil {
		return run.tz
	}
	return time.UTC
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
	for _, run := range h.running {
		st := appruntime.StateRunning
		if inst, err := h.rt.GetInstance(run.tenantStr, run.row.InstanceID); err == nil && inst != nil {
			st = inst.State
		}
		items = append(items, item{row: run.row, st: st})
	}
	failures := make([]appInstanceFailure, 0, len(h.failed))
	for _, failure := range h.failed {
		failures = append(failures, failure)
	}
	h.mu.Unlock()

	if h.srv.cfg.PluginStore == nil || (len(items) == 0 && len(failures) == 0) {
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
	for _, failure := range failures {
		k := groupKey{tid: failure.row.TenantID, edgeID: failure.row.EdgeID}
		groups[k] = append(groups[k], api.PluginObservedInstanceData{
			InstanceID: failure.row.InstanceID, PluginID: failure.row.PluginID, Version: failure.row.Version,
			HostOnline: true, State: string(appruntime.StateFailed), Health: "ERROR",
			Detail: "server-apphost: " + failure.detail,
		})
	}
	for k, rows := range groups {
		// 事实：AppHost 是 Server 进程内的本地宿主，desired 快照由本进程写入并
		// 已成功收敛（apply snapshot + 实例 running），applied 即 desired、drift 恒否。
		h.srv.plugin.applyAppHostObservations(k.tid, k.edgeID, h.bootID, seq, rows)
	}
}

func (h *AppHost) recordInstanceFailure(row store.PluginInstanceRow, err error) {
	if err == nil {
		return
	}
	h.mu.Lock()
	h.failed[appInstKey{row.TenantID, row.InstanceID}] = appInstanceFailure{row: row, detail: err.Error()}
	h.mu.Unlock()
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
	for _, failure := range h.failed {
		if !seen[failure.row.TenantID] {
			seen[failure.row.TenantID] = true
			out = append(out, failure.row.TenantID)
		}
	}
	return out
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
		now := time.Now().Unix()
		// 先查再写以区分 created/updated：并发下可能误判为 updated，投影
		// 语义无损（消费端按 (instance, type, id) upsert 收敛最终态）。
		_, getErr := e.host.srv.cfg.Store.GetAppDomainRecord(tid, effect.PluginInstanceID, p.RecordType, p.RecordID)
		created := getErr == sql.ErrNoRows
		if getErr != nil && getErr != sql.ErrNoRows {
			// 读失败不阻断主路径：记录落库优先，投影降级为 updated
			created = false
		}
		if err := e.host.srv.cfg.Store.UpsertAppDomainRecord(tid, effect.PluginInstanceID,
			p.RecordType, p.RecordID, p.DataJSON, p.Version, now); err != nil {
			return err
		}
		e.host.broadcastDomainRecord(tid, effect.PluginInstanceID, p, created, now)
		return nil
	case appruntime.EffectScheduleJob:
		tid, err := strconv.ParseInt(effect.TenantID, 10, 64)
		if err != nil {
			return fmt.Errorf("apphost: effect tenant %q: %w", effect.TenantID, err)
		}
		p := effect.ScheduleJob
		expr, err := parseCronExpr(p.Cron)
		if err != nil {
			return fmt.Errorf("apphost: schedule %q: %w", p.ScheduleID, err)
		}
		tz := e.host.instanceTimezone(effect.TenantID, effect.PluginInstanceID)
		next := expr.nextAfter(time.Now(), tz)
		if next.IsZero() {
			return fmt.Errorf("apphost: schedule %q: cron %q has no future occurrence", p.ScheduleID, p.Cron)
		}
		if _, err := e.host.srv.cfg.Store.UpsertScheduledJob(store.ScheduledJobRow{
			TenantID: tid, InstanceID: effect.PluginInstanceID, ScheduleID: p.ScheduleID,
			Cron: p.Cron, Timezone: tz.String(), PayloadJSON: p.PayloadJSON,
			MissedPolicy: "skip", NextRunAt: next.Unix(),
		}); err != nil {
			return fmt.Errorf("apphost: schedule %q: %w", p.ScheduleID, err)
		}
		e.host.logger.Info("apphost schedule registered", "instance", effect.PluginInstanceID,
			"schedule", p.ScheduleID, "cron", p.Cron, "next_run_at", next.Unix())
		return nil
	case appruntime.EffectCancelJob:
		tid, err := strconv.ParseInt(effect.TenantID, 10, 64)
		if err != nil {
			return fmt.Errorf("apphost: effect tenant %q: %w", effect.TenantID, err)
		}
		if err := e.host.srv.cfg.Store.CancelScheduledJob(tid, effect.PluginInstanceID, effect.CancelJob.ScheduleID); err != nil {
			return fmt.Errorf("apphost: cancel schedule %q: %w", effect.CancelJob.ScheduleID, err)
		}
		e.host.logger.Info("apphost schedule cancelled", "instance", effect.PluginInstanceID,
			"schedule", effect.CancelJob.ScheduleID)
		return nil
	case appruntime.EffectSendNotification:
		// 通知通道（Connector/Notification）尚未实现：显式 fail-closed，
		// 不把未发送伪装成成功。
		return fmt.Errorf("%w: send_notification for instance %q: notification channel is not configured", appruntime.ErrEffectNotImplemented, effect.PluginInstanceID)
	default:
		return fmt.Errorf("apphost: unhandled effect kind %q", effect.Kind)
	}
}

func (e *appEffectExecutor) execRequestCommand(ctx context.Context, effect appruntime.Effect) error {
	p := effect.RequestCommand
	ref := appCommandRef{
		TenantID:   effect.TenantID,
		InstanceID: effect.PluginInstanceID, RequestID: p.IdempotencyKey,
		EntityID: p.EntityID, Action: p.Action,
	}
	tid, err := strconv.ParseInt(effect.TenantID, 10, 64)
	if err != nil {
		return fmt.Errorf("apphost: effect tenant %q: %w", effect.TenantID, err)
	}
	deviceKey, routeErr := e.host.srv.deviceKeyForBinding(p.DeviceID, p.EntityID)
	if routeErr != nil {
		err := fmt.Errorf("apphost: %w", routeErr)
		e.host.dispatchRequestCompleted(ref, sdkapplication.CommandStateFailed, err.Error())
		return err
	}
	registered := false
	cmdID, err := e.host.srv.dispatchDeviceCommandWithHook(ctx, tid, deviceKey, p.Action, p.ArgsJSON, func(id int64) {
		// 在命令写入 Edge 之前登记，避免设备 ACK 比 dispatch 返回更快时丢事件。
		e.host.mu.Lock()
		e.host.appCmds[id] = ref
		e.host.mu.Unlock()
		registered = true
	})
	if err != nil {
		if registered {
			e.host.mu.Lock()
			_, pending := e.host.appCmds[cmdID]
			if pending {
				delete(e.host.appCmds, cmdID)
			}
			e.host.mu.Unlock()
			if !pending {
				// 极快 ACK 已消费引用并投递终态，不能再用发送错误覆盖它。
				return err
			}
		}
		e.host.dispatchRequestCompleted(ref, sdkapplication.CommandStateFailed, err.Error())
		return err
	}
	return nil
}

// ---- Server 侧辅助（AppHost 接线所需）----

// appCandidates constructs every bindable entity for a tenant. EntityID remains
// device-local, while DeviceID is retained in the binding and used to route
// effects to the exact provider selected by the Binder.
func (s *Server) appCandidates(tenantID int64) []coreapplication.Candidate {
	// Resolve ownership outside the memory lock. Never relabel all devices as
	// the requesting tenant merely to make the binder accept them.
	if s.cfg.Store == nil {
		return nil
	}
	tenant, err := s.cfg.Store.GetTenantByID(tenantID)
	if err != nil {
		return nil
	}
	tenantStr := strconv.FormatInt(tenantID, 10)
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]coreapplication.Candidate, 0, 32)
	for key, desc := range s.descriptors {
		if s.deviceTenant(key) != tenant.Slug {
			continue
		}
		for _, e := range desc.Entities {
			out = append(out, coreapplication.Candidate{
				EntityID: e.EntityID, Name: e.Name, TenantID: tenantStr,
				Capabilities: e.Capabilities, DeviceID: key,
			})
		}
	}
	// 候选顺序必须确定：s.descriptors 是 map（设备间迭代随机），Binder 的
	// first-match 语义依赖顺序——顺序抖动会让 one-or-more/min-1 类需求每次
	// 绑到不同实体（2026-09-05 D3 真板实测根因之一）。按 (DeviceID, EntityID)
	// 排序：跨设备、跨重启完全确定。
	sort.Slice(out, func(i, j int) bool {
		if out[i].DeviceID != out[j].DeviceID {
			return out[i].DeviceID < out[j].DeviceID
		}
		return out[i].EntityID < out[j].EntityID
	})
	return out
}

// deviceKeyForBinding routes a bound effect. Explicit DeviceID is authoritative;
// legacy bindings without one retain the unique-online-provider fallback.
func (s *Server) deviceKeyForBinding(deviceID, entityID string) (string, error) {
	if deviceID == "" {
		return s.deviceKeyForEntity(entityID)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	desc, ok := s.descriptors[deviceID]
	if !ok {
		return "", fmt.Errorf("entity %q target device %q has no descriptor", entityID, deviceID)
	}
	found := false
	for _, entity := range desc.Entities {
		if entity.EntityID == entityID {
			found = true
			break
		}
	}
	if !found {
		return "", fmt.Errorf("entity %q not found on target device %q", entityID, deviceID)
	}
	if v, ok := s.devices[deviceID]; !ok || !v.Online {
		return "", fmt.Errorf("target device %q is offline", deviceID)
	}
	return deviceID, nil
}

// deviceKeyForEntity is the compatibility path for bindings created before
// DeviceID was persisted. It accepts exactly one online provider.
func (s *Server) deviceKeyForEntity(entityID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var online, offline []string
	for key, desc := range s.descriptors {
		found := false
		for _, entity := range desc.Entities {
			if entity.EntityID == entityID {
				found = true
				break
			}
		}
		if !found {
			continue
		}
		if v, ok := s.devices[key]; ok && v.Online {
			online = append(online, key)
		} else {
			offline = append(offline, key)
		}
	}
	sort.Strings(online)
	sort.Strings(offline)
	switch {
	case len(online) == 1:
		return online[0], nil
	case len(online) > 1:
		return "", fmt.Errorf("entity %q is ambiguous across online devices: %s", entityID, strings.Join(online, ", "))
	case len(offline) > 0:
		return "", fmt.Errorf("entity %q is only present on offline device(s): %s", entityID, strings.Join(offline, ", "))
	default:
		return "", fmt.Errorf("entity %q not found on any device", entityID)
	}
}

// dispatchDeviceCommandWithHook 是应用效果 → 设备命令的下发内核（与
// handlePostCommand 同一路径：建命令行 + edge 链路投递）。区别：不做进程内
// 适配器白名单——外部 Driver 设备本就无注册表项，命令合法性由 Edge/Driver
// 侧校验；应用可下发的动作已被 Capability 绑定约束。onCreated 在命令落库后、
// 任何网络写入前同步调用，供 AppHost 先登记 RequestCompleted 引用。
func (s *Server) dispatchDeviceCommandWithHook(ctx context.Context, tenantID int64, key, cmd, args string, onCreated func(int64)) (int64, error) {
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
	if onCreated != nil {
		onCreated(id)
	}
	data, _ := json.Marshal(api.CommandData{CommandID: id, Cmd: cmd, Args: args})
	payload, _ := json.Marshal(api.Envelope{V: api.Version, Type: api.MsgCommand, Device: key, Ts: time.Now().Unix(), Data: data})
	// 先落 sent 再写 WS：Edge 可能在本函数返回前就回 ack，避免终态被覆盖。
	if _, err := s.cfg.Store.UpdateCommandStatusScoped(id, key, tenantID, "sent", ""); err != nil {
		return id, err
	}
	if err := link.sendCommand(ctx, payload); err != nil {
		_, markErr := s.cfg.Store.UpdateCommandStatusScoped(id, key, tenantID, "failed", edgeSendFailureDetail(err))
		if markErr != nil {
			return id, fmt.Errorf("edge send: %w (mark failed: %v)", err, markErr)
		}
		return id, fmt.Errorf("edge send: %w", err)
	}
	return id, nil
}

// installedAppPlugin 是 AppHost 目录中一个通过 lock/manifest 一致性校验的
// Application 插件事实。只保存公开字段所需的 manifest 与 lock 元数据。
type installedAppPlugin struct {
	manifest *registry.Manifest
	locked   registry.LockedPlugin
}

// loadInstalledApplicationPlugins 读取 AppHost 的 lockfile + plugin.yaml，并
// 只返回通过一致性校验的 Application 插件。lock/manifest 读取失败、ID/版本
// 不一致或 kind 非法都 fail-closed；非 Application 插件合法跳过。
func loadInstalledApplicationPlugins(pluginsDir, lockPath string) ([]installedAppPlugin, error) {
	lock, err := registry.LoadLockFile(lockPath)
	if err != nil {
		return nil, fmt.Errorf("apphost: load plugin lock: %w", err)
	}
	out := make([]installedAppPlugin, 0, len(lock.Plugins))
	for _, locked := range lock.Plugins {
		manifestPath := filepath.Join(pluginsDir, registry.SafePluginID(locked.ID), "plugin.yaml")
		manifest, err := registry.ReadManifest(manifestPath)
		if err != nil {
			return nil, fmt.Errorf("apphost: read manifest for %s: %w", locked.ID, err)
		}
		if manifest.ID != locked.ID {
			return nil, fmt.Errorf("apphost: manifest id %q does not match lock id %q", manifest.ID, locked.ID)
		}
		if locked.Version != "" && manifest.Version != locked.Version {
			return nil, fmt.Errorf("apphost: manifest version %q does not match lock version %q for %s",
				manifest.Version, locked.Version, locked.ID)
		}
		kind, err := pluginhost.ParseKind(manifest.Kind)
		if err != nil {
			return nil, fmt.Errorf("apphost: parse kind for %s: %w", locked.ID, err)
		}
		if kind != pluginhost.KindApplication {
			continue
		}
		out = append(out, installedAppPlugin{manifest: manifest, locked: locked})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].locked.ID != out[j].locked.ID {
			return out[i].locked.ID < out[j].locked.ID
		}
		return out[i].manifest.Version < out[j].manifest.Version
	})
	return out, nil
}

// installationStatuses 返回中心服务托管的 Application 插件公开事实。
// 与 Edge 上报的安装物同形，供只读插件目录统一展示；不包含本地路径、启动参数或 secret。
// lock/manifest 损坏时返回错误，由读面映射为 500，绝不静默隐藏安装事实。
func (h *AppHost) installationStatuses() ([]api.PluginInstallationStatusData, error) {
	if h == nil || !h.cfg.Enabled {
		return nil, nil
	}
	plugins, err := loadInstalledApplicationPlugins(h.cfg.PluginsDir, h.cfg.LockPath)
	if err != nil {
		return nil, err
	}
	out := make([]api.PluginInstallationStatusData, 0, len(plugins))
	for _, plugin := range plugins {
		out = append(out, installationStatusFromManifest(plugin.manifest, plugin.locked))
	}
	return out, nil
}

func installationStatusFromManifest(m *registry.Manifest, locked registry.LockedPlugin) api.PluginInstallationStatusData {
	if m == nil {
		return api.PluginInstallationStatusData{}
	}
	out := api.PluginInstallationStatusData{
		PluginID: m.ID, Version: m.Version, Kind: m.Kind, Protocol: m.Protocol,
		Digest: locked.Digest, TrustMode: string(locked.Mode), Verified: locked.Verified,
		VerifiedPublisher: locked.VerifiedPublisher,
		Permissions: api.PluginPermissionsData{
			Hardware:   append([]string(nil), m.Permissions.Hardware...),
			Network:    append([]string(nil), m.Permissions.Network...),
			Filesystem: append([]string(nil), m.Permissions.Filesystem...),
			Secrets:    append([]string(nil), m.Permissions.Secrets...),
		},
		Capabilities: append([]string(nil), m.Capabilities...),
	}
	out.Contributions = m.PublicContributions()
	return out
}

// InstalledApplicationPlugins 返回已安装的 Application kind 插件集合（pluginID → version）。
func InstalledApplicationPlugins(pluginsDir, lockPath string) (map[string]string, error) {
	plugins, err := loadInstalledApplicationPlugins(pluginsDir, lockPath)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(plugins))
	for _, plugin := range plugins {
		out[plugin.locked.ID] = plugin.locked.Version
	}
	return out, nil
}
