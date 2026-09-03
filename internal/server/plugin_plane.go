package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/plugincatalog"
	"github.com/DeliciousBuding/cloud-path/internal/server/storeport"
	"github.com/DeliciousBuding/cloud-path/internal/tenantpolicy"
)

const (
	// defaultPluginStaleAfter 是 observed 投影的过期阈值：超过该时长未收到
	// plugin_status 即标 stale（只标记，绝不改写 desired）。
	defaultPluginStaleAfter = 2 * time.Minute
	// maxDesiredSnapshotInstances 限制单次下发快照的实例数，防止响应无界膨胀。
	maxDesiredSnapshotInstances = 512
	// quotaAuditInterval 是 quota.exceeded 审计的节流窗口
	// （tenant-security-policy §4.3：每 tenant/resource 每分钟最多一条）。
	quotaAuditInterval = time.Minute
)

// 插件控制面审计动作（audit 包只给字符串常量，本 lane 自己定义域内动作名）。
const (
	actionPluginCreate    = "plugin_instance.create"
	actionPluginUpdate    = "plugin_instance.update"
	actionPluginDelete    = "plugin_instance.delete"
	actionPluginReconcile = "plugin_instance.reconcile"
	actionPluginApplied   = "plugin_instance.applied"
	actionPluginRejected  = "plugin_instance.rejected"
	actionPluginProtocol  = "plugin.protocol_error"
	actionPluginQuota     = "quota.exceeded"
	actionPluginSecret    = "plugin.secret_rejected"

	targetPluginInstance = "plugin_instance"
	targetPluginEdge     = "plugin_edge"
)

// pluginInstKey 是期望态行的租户内键（tenant 已在 map 外层作用域）。
type pluginInstKey struct {
	edgeID     string
	instanceID string
}

// pluginEdgePlane 是单个 tenant/edge 的 revision 与 Edge 上报投影。
// desired 与 observed 严格分离：本结构同时持有两者，但读面永远分别返回。
type pluginEdgePlane struct {
	desiredRevision uint64
	appliedRevision uint64
	bootID          string
	retiredBoots    []string
	lastSequence    uint64
	lastReportAt    int64
	lastAckAt       int64
	lastAckStatus   string
	lastResults     []api.PluginApplyResultData
	observed        map[string]api.PluginObservedInstanceData
	installations   map[string]api.PluginInstallationStatusData
}

// maxRetiredBoots 限制每个 tenant/edge 记住的历史 boot id 数量（防无界增长）。
const maxRetiredBoots = 16

// hasRetiredBoot 报告某个 boot id 是否已被更晚的 boot 取代。
// 旧 boot 的迟到消息即使携带更大的 sequence 也必须忽略（暗卷 2）。
func (ep *pluginEdgePlane) hasRetiredBoot(bootID string) bool {
	for _, old := range ep.retiredBoots {
		if old == bootID {
			return true
		}
	}
	return false
}

// retireBoot 把当前 boot id 移入历史集合（FIFO，上限 maxRetiredBoots）。
func (ep *pluginEdgePlane) retireBoot() {
	if ep.bootID == "" {
		return
	}
	ep.retiredBoots = append(ep.retiredBoots, ep.bootID)
	if len(ep.retiredBoots) > maxRetiredBoots {
		ep.retiredBoots = ep.retiredBoots[len(ep.retiredBoots)-maxRetiredBoots:]
	}
}

// pluginTenantPlane 是单租户的插件控制面缓存（懒加载自 PluginStore）。
type pluginTenantPlane struct {
	slug      string
	loaded    bool
	instances map[pluginInstKey]storeport.PluginInstanceRow
	edges     map[string]*pluginEdgePlane
}

// pluginPlane 是 Server 侧插件控制面运行态：
// desired 权威缓存（store 为权威源）+ Edge observed 投影 + 配额审计节流。
//
// 锁纪律：p.mu 只保护本结构；**持 p.mu 时绝不调用任何需要 s.mu 的方法**
// （在线态一律先在 s.mu 下取好快照再进来），避免两把锁交叉死锁。
type pluginPlane struct {
	store      storeport.PluginStore
	staleAfter time.Duration
	now        func() time.Time

	// writeMu 串行化期望态写路径：配额计数与写入必须在同一临界区内完成
	// （tenant-security-policy §4.1 原子性：禁止「先 COUNT、解锁、再 INSERT」）。
	writeMu sync.Mutex

	mu         sync.Mutex
	tenants    map[int64]*pluginTenantPlane
	quotaAudit map[string]time.Time
}

func newPluginPlane(st storeport.PluginStore, staleAfter time.Duration) *pluginPlane {
	if staleAfter <= 0 {
		staleAfter = defaultPluginStaleAfter
	}
	return &pluginPlane{
		store: st, staleAfter: staleAfter, now: time.Now,
		tenants: map[int64]*pluginTenantPlane{}, quotaAudit: map[string]time.Time{},
	}
}

// enabled 报告插件控制面是否已接线（PluginStore 为 nil 时全部路径降级为忽略/空）。
func (p *pluginPlane) enabled() bool { return p != nil && p.store != nil }

// PluginControlPlaneWired 报告插件控制面持久化是否已接线（启动日志/健康自检用）。
func (s *Server) PluginControlPlaneWired() bool { return s.plugin.enabled() }

// ensureLoaded 懒加载租户插件控制面（Server 重启后由首次访问/Edge 重连触发恢复）。
// §3 契约没有「列出全部租户」的能力，因此按 (tenantID, slug) 懒加载而非全量水合。
func (p *pluginPlane) ensureLoaded(tenantID int64, slug string) (*pluginTenantPlane, error) {
	if !p.enabled() {
		return nil, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ensureLoadedLocked(tenantID, slug)
}

func (p *pluginPlane) ensureLoadedLocked(tenantID int64, slug string) (*pluginTenantPlane, error) {
	t := p.tenants[tenantID]
	if t == nil {
		t = &pluginTenantPlane{
			instances: map[pluginInstKey]storeport.PluginInstanceRow{},
			edges:     map[string]*pluginEdgePlane{},
		}
		p.tenants[tenantID] = t
	}
	if slug != "" {
		t.slug = slug
	}
	if t.loaded {
		return t, nil
	}
	rows, err := p.store.ListPluginInstancesTenant(tenantID)
	if err != nil {
		t.loaded = false
		return nil, err
	}
	for _, row := range rows {
		t.instances[pluginInstKey{edgeID: row.EdgeID, instanceID: row.InstanceID}] = row
		p.edgePlaneLocked(t, row.EdgeID)
	}
	obs, err := p.store.ListPluginObservationsTenant(tenantID)
	if err != nil {
		return nil, err
	}
	for _, row := range obs {
		ep := p.edgePlaneLocked(t, row.EdgeID)
		ep.observed[row.InstanceID] = row.Observed()
		if row.ReportedAt > ep.lastReportAt {
			ep.lastReportAt = row.ReportedAt
		}
	}
	insts, err := p.store.ListPluginInstallationsTenant(tenantID)
	if err != nil {
		return nil, err
	}
	for _, row := range insts {
		ep := p.edgePlaneLocked(t, row.EdgeID)
		ep.installations[row.PluginID] = row.Status()
		if row.ReportedAt > ep.lastReportAt {
			ep.lastReportAt = row.ReportedAt
		}
	}
	for edgeID := range t.edges {
		rev, err := p.store.GetPluginEdgeRevision(tenantID, edgeID)
		if err != nil {
			return nil, err
		}
		ep := t.edges[edgeID]
		ep.desiredRevision = rev.DesiredRevision
		ep.appliedRevision = rev.AppliedRevision
		ep.bootID = rev.BootID
		ep.lastSequence = rev.LastSequence
		if rev.LastReportAt > ep.lastReportAt {
			ep.lastReportAt = rev.LastReportAt
		}
		ep.lastAckAt = rev.LastAckAt
	}
	t.loaded = true
	return t, nil
}

func (p *pluginPlane) edgePlaneLocked(t *pluginTenantPlane, edgeID string) *pluginEdgePlane {
	ep := t.edges[edgeID]
	if ep == nil {
		ep = &pluginEdgePlane{
			observed:      map[string]api.PluginObservedInstanceData{},
			installations: map[string]api.PluginInstallationStatusData{},
		}
		t.edges[edgeID] = ep
	}
	return ep
}

// tenantIDForSlug 在已加载的缓存里反查租户 id；未知返回 (0,false)。
func (p *pluginPlane) tenantIDForSlug(slug string) (int64, bool) {
	if !p.enabled() || slug == "" {
		return 0, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, t := range p.tenants {
		if t.slug == slug {
			return id, true
		}
	}
	return 0, false
}

// quotaLimit 解析租户插件实例配额：策略行 <=0 的字段表示继承 Server 默认值。
func (p *pluginPlane) quotaLimit(tenantID int64) int {
	def := tenantpolicy.Defaults().Quotas.PluginInstances
	if !p.enabled() {
		return def
	}
	p.mu.Lock()
	row, err := p.store.GetTenantPolicy(tenantID)
	p.mu.Unlock()
	if err != nil || row.QuotaPluginInstances <= 0 {
		return def
	}
	return row.QuotaPluginInstances
}

// admitQuota 在写入前做配额判定，并按 tenant-security-policy §4.3 节流失败审计。
// 返回 nil 表示放行；返回 error 时不得写入、不得增加 revision。
func (p *pluginPlane) admitQuota(tenantID int64) error {
	if !p.enabled() {
		return nil
	}
	limit := p.quotaLimit(tenantID)
	p.mu.Lock()
	n, err := p.store.CountPluginInstances(tenantID)
	p.mu.Unlock()
	if err != nil {
		return err
	}
	if n < limit {
		return nil
	}
	return &tenantpolicy.QuotaError{
		Resource: tenantpolicy.ResourcePluginInstances, Limit: limit, Usage: n,
	}
}

// allowQuotaAudit 判定本次 quota 拒绝是否应写审计（每 tenant/resource 每分钟一条）。
func (p *pluginPlane) allowQuotaAudit(tenantID int64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := strconv.FormatInt(tenantID, 10) + "/plugin_instances"
	if last, ok := p.quotaAudit[key]; ok && p.now().Sub(last) < quotaAuditInterval {
		return false
	}
	p.quotaAudit[key] = p.now()
	return true
}

// ---------- desired 快照（Server→Edge 下发用） ----------

// desiredSnapshot 构造 tenant/edge 的完整声明式期望态快照。
// 快照永远是「当前完整状态」，不是增量队列：Edge 离线期间的多次变更在重连后
// 只收敛到这一份最终快照，不回放中间副作用（暗卷 4）。
func (p *pluginPlane) desiredSnapshot(tenantID int64, edgeID string) (api.PluginDesiredData, bool) {
	if !p.enabled() {
		return api.PluginDesiredData{}, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	t, err := p.ensureLoadedLocked(tenantID, "")
	if err != nil || t == nil {
		return api.PluginDesiredData{}, false
	}
	return p.desiredSnapshotLocked(t, edgeID), true
}

// desiredSnapshotLocked 构造快照的核心逻辑（调用方持 p.mu）。
func (p *pluginPlane) desiredSnapshotLocked(t *pluginTenantPlane, edgeID string) api.PluginDesiredData {
	ep := p.edgePlaneLocked(t, edgeID)
	out := make([]api.PluginDesiredInstanceData, 0, len(t.instances))
	for k, row := range t.instances {
		if k.edgeID != edgeID {
			continue
		}
		out = append(out, api.PluginDesiredInstanceData{
			InstanceID: row.InstanceID, PluginID: row.PluginID, Version: row.Version,
			Enabled: row.Enabled, Isolation: row.Isolation, Config: decodeConfigJSON(row.ConfigJSON),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].InstanceID < out[j].InstanceID })
	if len(out) > maxDesiredSnapshotInstances {
		out = out[:maxDesiredSnapshotInstances]
	}
	return api.PluginDesiredData{
		Revision: ep.desiredRevision, SnapshotDigest: pluginSnapshotDigest(out), Instances: out,
	}
}

// pluginSnapshotDigest 计算绑定同 revision payload 的规范化摘要：
// 实例先按 instance_id 排序（config map 由 encoding/json 自动按键排序），
// 只对 instances 数组取 sha256。Edge 用它拒绝「同 revision 不同 payload」。
func pluginSnapshotDigest(instances []api.PluginDesiredInstanceData) string {
	payload := struct {
		Instances []api.PluginDesiredInstanceData `json:"instances"`
	}{Instances: instances}
	b, err := json.Marshal(payload)
	if err != nil {
		return "sha256:unavailable"
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func decodeConfigJSON(raw string) map[string]string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(raw), &out); err != nil || len(out) == 0 {
		return nil
	}
	return out
}

func decodeSecretRefs(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil || len(out) == 0 {
		return nil
	}
	return out
}

func encodeConfigJSON(cfg map[string]string) string {
	if len(cfg) == 0 {
		return "{}"
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func encodeSecretRefs(refs []string) string {
	if len(refs) == 0 {
		return "[]"
	}
	b, err := json.Marshal(refs)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// ---------- 缓存写入（store 提交成功后同步内存，保持读面一致） ----------

func (p *pluginPlane) remember(row storeport.PluginInstanceRow) {
	if !p.enabled() {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	t, err := p.ensureLoadedLocked(row.TenantID, "")
	if err != nil || t == nil {
		return
	}
	t.instances[pluginInstKey{edgeID: row.EdgeID, instanceID: row.InstanceID}] = row
	ep := p.edgePlaneLocked(t, row.EdgeID)
	ep.desiredRevision = row.Revision
}

func (p *pluginPlane) forget(tenantID int64, edgeID, instanceID string, revision uint64) {
	if !p.enabled() {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	t := p.tenants[tenantID]
	if t == nil {
		return
	}
	delete(t.instances, pluginInstKey{edgeID: edgeID, instanceID: instanceID})
	if ep := p.edgePlaneLocked(t, edgeID); revision > ep.desiredRevision {
		ep.desiredRevision = revision
	}
}

func (p *pluginPlane) forgetObserved(tenantID int64, edgeID, instanceID string) {
	if !p.enabled() {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	t := p.tenants[tenantID]
	if t == nil {
		return
	}
	if ep, ok := t.edges[edgeID]; ok {
		delete(ep.observed, instanceID)
	}
}

// setRevision 把 store 返回的新 desired revision 同步进缓存。
func (p *pluginPlane) setRevision(tenantID int64, edgeID string, revision uint64) {
	if !p.enabled() {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	t, err := p.ensureLoadedLocked(tenantID, "")
	if err != nil || t == nil {
		return
	}
	if ep := p.edgePlaneLocked(t, edgeID); revision > ep.desiredRevision {
		ep.desiredRevision = revision
	}
}

// ---------- 只读投影（plugincatalog.ProjectionSource 的真实数据源） ----------

// pluginProjection 把 Server 的插件控制面投影适配为 plugincatalog 的只读源。
// 它取代任何 fake/静态来源：安装物只来自 Edge 上报，期望态只来自 Server 权威存储。
type pluginProjection struct{ s *Server }

// Installations 返回租户可见的安装物投影（Edge 上报的公开 manifest 事实）。
func (pr pluginProjection) Installations(tenant string) ([]api.PluginInstallationStatusData, error) {
	s := pr.s
	if !s.plugin.enabled() {
		return []api.PluginInstallationStatusData{}, nil
	}
	p := s.plugin
	p.mu.Lock()
	defer p.mu.Unlock()
	ids, err := p.matchTenantsLocked(tenant)
	if err != nil {
		return nil, err
	}
	out := make([]api.PluginInstallationStatusData, 0, 8)
	for _, tid := range ids {
		t := p.tenants[tid]
		if t == nil || !t.loaded {
			continue
		}
		for _, ep := range t.edges {
			for _, in := range ep.installations {
				out = append(out, in)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PluginID != out[j].PluginID {
			return out[i].PluginID < out[j].PluginID
		}
		return out[i].Version < out[j].Version
	})
	return out, nil
}

// Instances 返回租户可见的插件实例合成事实：desired 与 observed 分别取自
// Server 权威存储与 Edge 上报投影，Drift/Stale 由真实 revision 与上报时间计算。
func (pr pluginProjection) Instances(tenant string) ([]plugincatalog.ProjectionInstance, error) {
	s := pr.s
	p := s.plugin
	if !p.enabled() {
		return []plugincatalog.ProjectionInstance{}, nil
	}
	online := s.pluginEdgeOnline()
	now := p.now().Unix()
	staleAfter := int64(p.staleAfter / time.Second)

	p.mu.Lock()
	defer p.mu.Unlock()
	ids, err := p.matchTenantsLocked(tenant)
	if err != nil {
		return nil, err
	}
	out := make([]plugincatalog.ProjectionInstance, 0, 8)
	for _, tid := range ids {
		t := p.tenants[tid]
		if t == nil {
			continue
		}
		for edgeID, ep := range t.edges {
			edgeOnline := online[pluginEdgeKey{tenantID: tid, edgeID: edgeID}]
			stale := !edgeOnline || (now-ep.lastReportAt) > staleAfter
			drift := ep.desiredRevision != ep.appliedRevision
			seen := map[string]bool{}
			for k, row := range t.instances {
				if k.edgeID != edgeID {
					continue
				}
				seen[k.instanceID] = true
				pi := plugincatalog.ProjectionInstance{
					TenantID: tid, Tenant: t.slug, EdgeID: edgeID, InstanceID: row.InstanceID,
					PluginID: row.PluginID, Version: row.Version, Enabled: row.Enabled,
					Isolation: row.Isolation, SecretRefs: decodeSecretRefs(row.SecretRefs),
					Config: decodeConfigJSON(row.ConfigJSON), ConfigPresent: len(decodeConfigJSON(row.ConfigJSON)) > 0,
					EdgeOnline: edgeOnline, DesiredRevision: ep.desiredRevision,
					AppliedRevision: ep.appliedRevision, Drift: drift, Stale: stale,
					LastAckAt: ep.lastAckAt, UpdatedAt: row.UpdatedAt, RowRevision: row.Revision,
				}
				if o, ok := ep.observed[row.InstanceID]; ok {
					pi.HasObserved = true
					pi.ObservedVersion = o.Version
					pi.State, pi.Health, pi.Detail = o.State, o.Health, o.Detail
					pi.RestartCount, pi.LastHealthy = o.RestartCount, o.LastHealthy
					pi.ReportedAt, pi.MessageRate = ep.lastReportAt, o.MessageRate
				}
				out = append(out, pi)
			}
			// Edge 上报了 Server 期望态里没有的实例：真实存在的不一致，必须暴露而不是隐藏。
			for id, o := range ep.observed {
				if seen[id] {
					continue
				}
				// 只有 observed、没有 desired 的实例：desired 子视图保持零值，
				// 上报事实只经 Observed 呈现（不变量 5：两者绝不互相冒充）。
				out = append(out, plugincatalog.ProjectionInstance{
					TenantID: tid, Tenant: t.slug, EdgeID: edgeID, InstanceID: id,
					Isolation: "", Enabled: false, ObservedVersion: o.Version,
					EdgeOnline: edgeOnline, DesiredRevision: ep.desiredRevision,
					AppliedRevision: ep.appliedRevision, Drift: true, Stale: stale,
					LastAckAt: ep.lastAckAt, HasObserved: true,
					State: o.State, Health: o.Health, Detail: o.Detail,
					RestartCount: o.RestartCount, LastHealthy: o.LastHealthy,
					ReportedAt: ep.lastReportAt, MessageRate: o.MessageRate,
				})
			}
		}
	}
	return out, nil
}

// matchTenantsLocked 解析读面租户过滤：空 slug = 全部已加载租户。
// 未加载的租户不会被枚举（懒加载语义）；调用方持 p.mu。
func (p *pluginPlane) matchTenantsLocked(slug string) ([]int64, error) {
	ids := make([]int64, 0, len(p.tenants))
	for id, t := range p.tenants {
		if slug != "" && t.slug != slug {
			continue
		}
		if _, err := p.ensureLoadedLocked(id, t.slug); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}

// pluginEdgeKey 是 (tenant, edge) 的在线态查询键。
type pluginEdgeKey struct {
	tenantID int64
	edgeID   string
}

// pluginEdgeOnline 在 s.mu 下取一次在线 edge 快照（锁纪律：先 s.mu 后 p.mu，绝不嵌套）。
func (s *Server) pluginEdgeOnline() map[pluginEdgeKey]bool {
	out := map[pluginEdgeKey]bool{}
	s.mu.RLock()
	for edgeID, l := range s.edges {
		if l == nil {
			continue
		}
		out[pluginEdgeKey{tenantID: l.tenantID, edgeID: edgeID}] = true
	}
	s.mu.RUnlock()
	return out
}
