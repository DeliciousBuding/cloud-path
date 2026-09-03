// Package storeadapter 是 Captain 在 STORE / SERVER 两条 lane 合并后的唯一接线点：
// 把 *store.Store 接到 internal/server/storeport.PluginStore 端口。
//
// storeport 刻意不 import internal/store（并行开发期依赖倒置），因此本包是唯一
// 同时认识两者的薄映射层：只做行字段互转与哨兵错误归一，不含任何业务逻辑。
// 语义不变量（事务边界、配额整体失败、purge 语义、secret 只存 handle）全部由
// internal/store 实现并由其暗卷测试覆盖，本适配器不重复也不削弱它们。
package storeadapter

import (
	"errors"
	"fmt"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/server/storeport"
	"github.com/DeliciousBuding/cloud-path/internal/store"
)

// 编译期断言：适配器必须逐字满足端口。
var _ storeport.PluginStore = (*Adapter)(nil)

// Adapter 把 *store.Store 适配为 storeport.PluginStore。
type Adapter struct {
	st *store.Store
}

// New 返回端口实现；st 为 nil 时返回 nil，调用方按「未接线」安全降级。
func New(st *store.Store) storeport.PluginStore {
	if st == nil {
		return nil
	}
	return &Adapter{st: st}
}

// mapErr 把 store 哨兵归一到端口哨兵；Server 据 errors.Is 产出 api.PluginErr* 稳定码。
// 其余错误原样透传（Server 记 500/503，不伪装成业务码）。
func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrPluginQuotaExceeded):
		return fmt.Errorf("%w: %w", storeport.ErrQuota, err)
	case errors.Is(err, store.ErrPluginInstanceNotFound):
		return fmt.Errorf("%w: %w", storeport.ErrNotFound, err)
	case errors.Is(err, store.ErrPluginInstanceConflict):
		return fmt.Errorf("%w: %w", storeport.ErrConflict, err)
	case errors.Is(err, store.ErrEdgeTenantMismatch):
		return fmt.Errorf("%w: %w", storeport.ErrTenantMismatch, err)
	default:
		return err
	}
}

func instToPort(r store.PluginInstanceRow) storeport.PluginInstanceRow {
	return storeport.PluginInstanceRow{
		TenantID: r.TenantID, EdgeID: r.EdgeID, InstanceID: r.InstanceID,
		PluginID: r.PluginID, Version: r.Version, Enabled: r.Enabled,
		Isolation: r.Isolation, ConfigJSON: r.ConfigJSON, SecretRefs: r.SecretRefs,
		Revision: r.Revision, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func instToStore(r storeport.PluginInstanceRow) store.PluginInstanceRow {
	return store.PluginInstanceRow{
		TenantID: r.TenantID, EdgeID: r.EdgeID, InstanceID: r.InstanceID,
		PluginID: r.PluginID, Version: r.Version, Enabled: r.Enabled,
		Isolation: r.Isolation, ConfigJSON: r.ConfigJSON, SecretRefs: r.SecretRefs,
		Revision: r.Revision, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func revToPort(r store.PluginEdgeRevisionRow) storeport.PluginEdgeRevisionRow {
	return storeport.PluginEdgeRevisionRow{
		TenantID: r.TenantID, EdgeID: r.EdgeID,
		DesiredRevision: r.DesiredRevision, AppliedRevision: r.AppliedRevision,
		BootID: r.BootID, LastSequence: r.LastSequence,
		LastReportAt: r.LastReportAt, LastAckAt: r.LastAckAt,
	}
}

func policyToPort(r store.TenantPolicyRow) storeport.TenantPolicyRow {
	return storeport.TenantPolicyRow{
		TenantID:              r.TenantID,
		RetentionEvents:       r.RetentionEventsDays,
		RetentionCommands:     r.RetentionCommandsDays,
		RetentionAudit:        r.RetentionAuditDays,
		RetentionTokens:       r.RetentionRevokedTokensDays,
		RetentionObservations: r.RetentionPluginObservedDays,
		QuotaDevices:          r.QuotaDevices,
		QuotaEdges:            r.QuotaEdges,
		QuotaBrowserWS:        r.QuotaBrowserWS,
		QuotaTokens:           r.QuotaTokens,
		QuotaUsers:            r.QuotaUsers,
		QuotaEventsPerMinute:  r.QuotaEventsPerMinute,
		QuotaPluginInstances:  r.QuotaPluginInstances,
		UpdatedAt:             r.UpdatedAt,
	}
}

func policyToStore(r storeport.TenantPolicyRow) store.TenantPolicyRow {
	return store.TenantPolicyRow{
		TenantID:                    r.TenantID,
		RetentionEventsDays:         r.RetentionEvents,
		RetentionCommandsDays:       r.RetentionCommands,
		RetentionAuditDays:          r.RetentionAudit,
		RetentionRevokedTokensDays:  r.RetentionTokens,
		RetentionPluginObservedDays: r.RetentionObservations,
		QuotaDevices:                r.QuotaDevices,
		QuotaEdges:                  r.QuotaEdges,
		QuotaBrowserWS:              r.QuotaBrowserWS,
		QuotaTokens:                 r.QuotaTokens,
		QuotaUsers:                  r.QuotaUsers,
		QuotaEventsPerMinute:        r.QuotaEventsPerMinute,
		QuotaPluginInstances:        r.QuotaPluginInstances,
		UpdatedAt:                   r.UpdatedAt,
	}
}

// ---- 插件期望态（Server 权威）----

func (a *Adapter) ListPluginInstancesTenant(tenantID int64) ([]storeport.PluginInstanceRow, error) {
	rows, err := a.st.ListPluginInstancesTenant(tenantID)
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]storeport.PluginInstanceRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, instToPort(r))
	}
	return out, nil
}

func (a *Adapter) GetPluginInstance(tenantID int64, edgeID, instanceID string) (storeport.PluginInstanceRow, bool, error) {
	r, ok, err := a.st.GetPluginInstance(tenantID, edgeID, instanceID)
	if err != nil {
		return storeport.PluginInstanceRow{}, false, mapErr(err)
	}
	if !ok {
		return storeport.PluginInstanceRow{}, false, nil
	}
	return instToPort(r), true, nil
}

func (a *Adapter) CreatePluginInstance(row storeport.PluginInstanceRow) (uint64, error) {
	rev, err := a.st.CreatePluginInstance(instToStore(row))
	return rev, mapErr(err)
}

func (a *Adapter) UpdatePluginInstance(row storeport.PluginInstanceRow) (uint64, error) {
	rev, err := a.st.UpdatePluginInstance(instToStore(row))
	return rev, mapErr(err)
}

func (a *Adapter) DeletePluginInstance(tenantID int64, edgeID, instanceID string, purge bool) (uint64, error) {
	rev, err := a.st.DeletePluginInstance(tenantID, edgeID, instanceID, purge)
	return rev, mapErr(err)
}

func (a *Adapter) PluginDesiredRevision(tenantID int64, edgeID string) (uint64, error) {
	rev, err := a.st.PluginDesiredRevision(tenantID, edgeID)
	return rev, mapErr(err)
}

// ---- Edge revision / applied 投影 ----

func (a *Adapter) GetPluginEdgeRevision(tenantID int64, edgeID string) (storeport.PluginEdgeRevisionRow, error) {
	r, err := a.st.GetPluginEdgeRevision(tenantID, edgeID)
	if err != nil {
		return storeport.PluginEdgeRevisionRow{}, mapErr(err)
	}
	return revToPort(r), nil
}

func (a *Adapter) SetPluginEdgeApplied(tenantID int64, edgeID, bootID string, seq, appliedRevision uint64, at int64) error {
	return mapErr(a.st.SetPluginEdgeApplied(tenantID, edgeID, bootID, seq, appliedRevision, at))
}

func (a *Adapter) SetPluginEdgeReport(tenantID int64, edgeID, bootID string, seq uint64, at int64) error {
	return mapErr(a.st.SetPluginEdgeReport(tenantID, edgeID, bootID, seq, at))
}

// ---- Edge 上报的只读投影 ----
// Upsert 两侧签名都直接吃 api 公开 DTO，无需转换；读面把 store 内嵌 DTO 的行
// 经 storeport.New*Row 归一，JSON 空值口径与读侧反序列化一致。

func (a *Adapter) UpsertPluginInstallations(tenantID int64, edgeID string, rows []api.PluginInstallationStatusData) error {
	return mapErr(a.st.UpsertPluginInstallations(tenantID, edgeID, rows))
}

func (a *Adapter) UpsertPluginObservations(tenantID int64, edgeID string, rows []api.PluginObservedInstanceData, reportedAt int64) error {
	return mapErr(a.st.UpsertPluginObservations(tenantID, edgeID, rows, reportedAt))
}

func (a *Adapter) ListPluginObservationsTenant(tenantID int64) ([]storeport.PluginObservationRow, error) {
	rows, err := a.st.ListPluginObservationsTenant(tenantID)
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]storeport.PluginObservationRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, storeport.NewObservationRow(r.TenantID, r.EdgeID, r.PluginObservedInstanceData, r.ReportedAt))
	}
	return out, nil
}

func (a *Adapter) ListPluginInstallationsTenant(tenantID int64) ([]storeport.PluginInstallationRow, error) {
	rows, err := a.st.ListPluginInstallationsTenant(tenantID)
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]storeport.PluginInstallationRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, storeport.NewInstallationRow(r.TenantID, r.EdgeID, r.PluginInstallationStatusData, r.ReportedAt))
	}
	return out, nil
}

// ---- 租户策略 / 配额 / 保留期 ----

func (a *Adapter) GetTenantPolicy(tenantID int64) (storeport.TenantPolicyRow, error) {
	r, err := a.st.GetTenantPolicy(tenantID)
	if err != nil {
		return storeport.TenantPolicyRow{}, mapErr(err)
	}
	return policyToPort(r), nil
}

func (a *Adapter) SetTenantPolicy(tenantID int64, p storeport.TenantPolicyRow) error {
	return mapErr(a.st.SetTenantPolicy(tenantID, policyToStore(p)))
}

func (a *Adapter) CountPluginInstances(tenantID int64) (int, error) {
	n, err := a.st.CountPluginInstances(tenantID)
	return n, mapErr(err)
}
