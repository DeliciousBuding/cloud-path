package storeport

import (
	"sort"
	"sync"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/api"
)

// Memory 是 PluginStore 的进程内实现：Server lane 的测试替身，也是 Store v7/v8
// 接线前的显式降级形态（**不持久化**，进程退出即丢；生产必须由 SQLite 适配器替换）。
//
// 它完整实现端口语义不变量：租户作用域、revision 单调、失败不写入不增 revision、
// 配额原子拒绝、purge 语义、绝不改写既有行 tenant_id。
type Memory struct {
	mu      sync.Mutex
	now     func() time.Time
	inst    map[instKey]PluginInstanceRow
	rev     map[edgeKey]uint64
	edgeRev map[edgeKey]PluginEdgeRevisionRow
	obs     map[edgeKey]map[string]api.PluginObservedInstanceData
	obsAt   map[edgeKey]int64
	insts   map[edgeKey]map[string]api.PluginInstallationStatusData
	policy  map[int64]TenantPolicyRow
}

type instKey struct {
	tenantID   int64
	edgeID     string
	instanceID string
}

type edgeKey struct {
	tenantID int64
	edgeID   string
}

// NewMemory 构造空的进程内插件控制面存储。
func NewMemory() *Memory {
	return &Memory{
		now:     time.Now,
		inst:    map[instKey]PluginInstanceRow{},
		rev:     map[edgeKey]uint64{},
		edgeRev: map[edgeKey]PluginEdgeRevisionRow{},
		obs:     map[edgeKey]map[string]api.PluginObservedInstanceData{},
		obsAt:   map[edgeKey]int64{},
		insts:   map[edgeKey]map[string]api.PluginInstallationStatusData{},
		policy:  map[int64]TenantPolicyRow{},
	}
}

// SetNow 注入时钟（测试可复现 stale/过期判定）。
func (m *Memory) SetNow(now func() time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if now != nil {
		m.now = now
	}
}

func (m *Memory) timestamp() int64 { return m.now().Unix() }

// CreatePluginInstance 写入新期望态行并在同一临界区内把 desired revision +1。
// 已存在返回 ErrConflict；配额已满返回 ErrQuota（不写入、不增 revision）。
func (m *Memory) CreatePluginInstance(row PluginInstanceRow) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := instKey{tenantID: row.TenantID, edgeID: row.EdgeID, instanceID: row.InstanceID}
	if _, ok := m.inst[k]; ok {
		return 0, ErrConflict
	}
	if err := m.admitLocked(row.TenantID); err != nil {
		return 0, err
	}
	ek := edgeKey{tenantID: row.TenantID, edgeID: row.EdgeID}
	rev := m.rev[ek] + 1
	m.rev[ek] = rev
	row.Revision = rev
	if row.CreatedAt == 0 {
		row.CreatedAt = m.timestamp()
	}
	row.UpdatedAt = m.timestamp()
	m.inst[k] = row
	m.syncEdgeRevisionLocked(ek, rev)
	return rev, nil
}

// UpdatePluginInstance 覆盖既有期望态行并 +1 revision。缺行返回 ErrNotFound；
// 行归属其他租户返回 ErrTenantMismatch（fail-closed，绝不改写归属）。
func (m *Memory) UpdatePluginInstance(row PluginInstanceRow) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := instKey{tenantID: row.TenantID, edgeID: row.EdgeID, instanceID: row.InstanceID}
	old, ok := m.inst[k]
	if !ok {
		return 0, ErrNotFound
	}
	if old.TenantID != row.TenantID {
		return 0, ErrTenantMismatch
	}
	ek := edgeKey{tenantID: row.TenantID, edgeID: row.EdgeID}
	rev := m.rev[ek] + 1
	m.rev[ek] = rev
	row.Revision = rev
	row.CreatedAt = old.CreatedAt
	row.UpdatedAt = m.timestamp()
	m.inst[k] = row
	m.syncEdgeRevisionLocked(ek, rev)
	return rev, nil
}

// DeletePluginInstance 删除期望态行并 +1 revision。purge=true 才连带删除该实例的
// observed 投影；purge=false 保留投影（读面标 stale），审计永不被删除。
func (m *Memory) DeletePluginInstance(tenantID int64, edgeID, instanceID string, purge bool) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := instKey{tenantID: tenantID, edgeID: edgeID, instanceID: instanceID}
	if _, ok := m.inst[k]; !ok {
		return 0, ErrNotFound
	}
	delete(m.inst, k)
	ek := edgeKey{tenantID: tenantID, edgeID: edgeID}
	rev := m.rev[ek] + 1
	m.rev[ek] = rev
	if purge {
		if set, ok := m.obs[ek]; ok {
			delete(set, instanceID)
		}
	}
	m.syncEdgeRevisionLocked(ek, rev)
	return rev, nil
}

// ListPluginInstancesTenant 返回租户全部期望态行（按 edge/instance 排序，稳定输出）。
func (m *Memory) ListPluginInstancesTenant(tenantID int64) ([]PluginInstanceRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]PluginInstanceRow, 0, len(m.inst))
	for k, row := range m.inst {
		if k.tenantID != tenantID {
			continue
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].EdgeID != out[j].EdgeID {
			return out[i].EdgeID < out[j].EdgeID
		}
		return out[i].InstanceID < out[j].InstanceID
	})
	return out, nil
}

// GetPluginInstance 返回单个期望态行；不存在返回 ok=false（跨租户天然不可见）。
func (m *Memory) GetPluginInstance(tenantID int64, edgeID, instanceID string) (PluginInstanceRow, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.inst[instKey{tenantID: tenantID, edgeID: edgeID, instanceID: instanceID}]
	return row, ok, nil
}

// PluginDesiredRevision 返回 tenant/edge 当前 desired revision（无记录为 0）。
func (m *Memory) PluginDesiredRevision(tenantID int64, edgeID string) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rev[edgeKey{tenantID: tenantID, edgeID: edgeID}], nil
}

// GetPluginEdgeRevision 返回 tenant/edge 的 revision/applied/boot/sequence 投影。
func (m *Memory) GetPluginEdgeRevision(tenantID int64, edgeID string) (PluginEdgeRevisionRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ek := edgeKey{tenantID: tenantID, edgeID: edgeID}
	row := m.edgeRev[ek]
	row.TenantID, row.EdgeID = tenantID, edgeID
	if row.DesiredRevision == 0 {
		row.DesiredRevision = m.rev[ek]
	}
	return row, nil
}

// SetPluginEdgeApplied 记录 Edge 已成功应用的 revision（只有 applied ack 才调用）。
func (m *Memory) SetPluginEdgeApplied(tenantID int64, edgeID, bootID string, seq, appliedRevision uint64, at int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ek := edgeKey{tenantID: tenantID, edgeID: edgeID}
	row := m.edgeRev[ek]
	row.TenantID, row.EdgeID = tenantID, edgeID
	row.BootID, row.LastSequence = bootID, seq
	row.AppliedRevision = appliedRevision
	row.LastAckAt = at
	m.edgeRev[ek] = row
	return nil
}

// SetPluginEdgeReport 记录一次 observed 上报（boot/sequence/时间），不改 applied。
func (m *Memory) SetPluginEdgeReport(tenantID int64, edgeID, bootID string, seq uint64, at int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ek := edgeKey{tenantID: tenantID, edgeID: edgeID}
	row := m.edgeRev[ek]
	row.TenantID, row.EdgeID = tenantID, edgeID
	row.BootID, row.LastSequence, row.LastReportAt = bootID, seq, at
	if row.DesiredRevision == 0 {
		row.DesiredRevision = m.rev[ek]
	}
	m.edgeRev[ek] = row
	return nil
}

// UpsertPluginInstallations 用 Edge 上报的安装物集合整体替换 tenant/edge 投影。
func (m *Memory) UpsertPluginInstallations(tenantID int64, edgeID string, rows []api.PluginInstallationStatusData) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	set := make(map[string]api.PluginInstallationStatusData, len(rows))
	for _, r := range rows {
		set[r.PluginID] = r
	}
	m.insts[edgeKey{tenantID: tenantID, edgeID: edgeID}] = set
	return nil
}

// UpsertPluginObservations 用 Edge 上报的实例实际态集合整体替换 tenant/edge 投影。
func (m *Memory) UpsertPluginObservations(tenantID int64, edgeID string, rows []api.PluginObservedInstanceData, reportedAt int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	set := make(map[string]api.PluginObservedInstanceData, len(rows))
	for _, r := range rows {
		set[r.InstanceID] = r
	}
	ek := edgeKey{tenantID: tenantID, edgeID: edgeID}
	m.obs[ek] = set
	m.obsAt[ek] = reportedAt
	return nil
}

// ListPluginObservationsTenant 返回租户全部 observed 投影行（稳定排序）。
func (m *Memory) ListPluginObservationsTenant(tenantID int64) ([]PluginObservationRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]PluginObservationRow, 0, len(m.obs))
	for ek, set := range m.obs {
		if ek.tenantID != tenantID {
			continue
		}
		at := m.obsAt[ek]
		for id, o := range set {
			out = append(out, PluginObservationRow{
				TenantID: ek.tenantID, EdgeID: ek.edgeID, InstanceID: id,
				PluginID: o.PluginID, Version: o.Version, HostOnline: o.HostOnline,
				State: o.State, Health: o.Health, Detail: o.Detail,
				RestartCount: o.RestartCount, LastHealthy: o.LastHealthy,
				MessageRate: o.MessageRate, ReportedAt: at,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].EdgeID != out[j].EdgeID {
			return out[i].EdgeID < out[j].EdgeID
		}
		return out[i].InstanceID < out[j].InstanceID
	})
	return out, nil
}

// ListPluginInstallationsTenant 返回租户全部安装物投影行（稳定排序）。
func (m *Memory) ListPluginInstallationsTenant(tenantID int64) ([]PluginInstallationRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]PluginInstallationRow, 0, len(m.insts))
	for ek, set := range m.insts {
		if ek.tenantID != tenantID {
			continue
		}
		for id, in := range set {
			out = append(out, PluginInstallationRow{
				TenantID: ek.tenantID, EdgeID: ek.edgeID, PluginID: id,
				Version: in.Version, Kind: in.Kind, Protocol: in.Protocol,
				Digest: in.Digest, TrustMode: in.TrustMode, Verified: in.Verified,
				VerifiedPublisher: in.VerifiedPublisher,
				PermissionsJSON:   jsonOf(in.Permissions),
				ContributionsJSON: jsonOf(in.Contributions),
				CapabilitiesJSON:  jsonOf(in.Capabilities),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].EdgeID != out[j].EdgeID {
			return out[i].EdgeID < out[j].EdgeID
		}
		return out[i].PluginID < out[j].PluginID
	})
	return out, nil
}

// GetTenantPolicy 返回租户策略行；未设置返回零值行（调用方按「继承默认」解释）。
func (m *Memory) GetTenantPolicy(tenantID int64) (TenantPolicyRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row := m.policy[tenantID]
	row.TenantID = tenantID
	return row, nil
}

// SetTenantPolicy 保存租户策略行。
func (m *Memory) SetTenantPolicy(tenantID int64, p TenantPolicyRow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p.TenantID = tenantID
	m.policy[tenantID] = p
	return nil
}

// CountPluginInstances 返回租户期望态实例数（配额判定用）。
func (m *Memory) CountPluginInstances(tenantID int64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for k := range m.inst {
		if k.tenantID == tenantID {
			n++
		}
	}
	return n, nil
}

// admitLocked 在写入临界区内做配额判定：QuotaPluginInstances<=0 表示本实现不设限
// （Server 侧仍会按 tenantpolicy 默认值先判一次）。调用方必须持 m.mu。
func (m *Memory) admitLocked(tenantID int64) error {
	limit := m.policy[tenantID].QuotaPluginInstances
	if limit <= 0 {
		return nil
	}
	n := 0
	for k := range m.inst {
		if k.tenantID == tenantID {
			n++
		}
	}
	if n >= limit {
		return ErrQuota
	}
	return nil
}

// syncEdgeRevisionLocked 让 edge revision 投影跟随 desired revision（调用方持 m.mu）。
func (m *Memory) syncEdgeRevisionLocked(ek edgeKey, rev uint64) {
	row := m.edgeRev[ek]
	row.TenantID, row.EdgeID = ek.tenantID, ek.edgeID
	row.DesiredRevision = rev
	m.edgeRev[ek] = row
}
