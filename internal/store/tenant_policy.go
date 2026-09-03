package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/DeliciousBuding/cloud-path/internal/tenantpolicy"
)

// 租户策略持久化：保留期 + 配额（docs/architecture/tenant-security-policy.md §3、§4、§5、§6）。
//
// 约定：TenantPolicyRow 的每个 int 字段 0 = 未设置 = DB NULL = 继承 Server 默认值。
// 这与 §3.1「每个字段可为 NULL，表示继承 Server 默认值；不能用 0 表示无限」一致——
// 0 永远不是「无限」，解析后恒为正数（见 Resolve）。默认值的唯一来源是 tenantpolicy.Defaults()，
// store 不自带第二份默认值副本，避免漂移。
// tenant_policies 主键即 tenant_id，因此 SetTenantPolicy 结构上不可能改写既有行的租户归属。

// TenantPolicyRow 是 tenant_policies 一行。所有天数/上限字段 0 表示继承默认（存 NULL）。
// 字段集与 tenantpolicy.RetentionDays / tenantpolicy.Quotas 一一对应。
type TenantPolicyRow struct {
	TenantID int64

	// 保留期（天）
	RetentionEventsDays         int
	RetentionCommandsDays       int
	RetentionAuditDays          int
	RetentionRevokedTokensDays  int
	RetentionPluginObservedDays int

	// 配额（硬上限）
	QuotaDevices         int
	QuotaEdges           int
	QuotaBrowserWS       int
	QuotaTokens          int
	QuotaUsers           int
	QuotaEventsPerMinute int
	QuotaPluginInstances int

	UpdatedAt int64
}

// tenantPolicyColumns 是读写共用的列序（顺序必须与 scanTenantPolicy 一致）。
const tenantPolicyColumns = `tenant_id,
	retention_events_days, retention_commands_days, retention_audit_days,
	retention_revoked_tokens_days, retention_plugin_observed_days,
	quota_devices, quota_edges, quota_browser_ws, quota_tokens, quota_users,
	quota_events_per_min, quota_plugin_instances, updated_at`

// nullInt 把「0=继承默认」映射成 DB NULL，非 0 映射成具体值。
func nullInt(v int) any {
	if v <= 0 {
		return nil
	}
	return v
}

func scanTenantPolicy(sc interface{ Scan(...any) error }) (TenantPolicyRow, error) {
	var (
		r                          TenantPolicyRow
		re, rc, ra, rt, ro         sql.NullInt64
		qd, qe, qw, qt, qu, qm, qp sql.NullInt64
	)
	err := sc.Scan(&r.TenantID, &re, &rc, &ra, &rt, &ro, &qd, &qe, &qw, &qt, &qu, &qm, &qp, &r.UpdatedAt)
	if err != nil {
		return r, err
	}
	r.RetentionEventsDays = int(re.Int64)
	r.RetentionCommandsDays = int(rc.Int64)
	r.RetentionAuditDays = int(ra.Int64)
	r.RetentionRevokedTokensDays = int(rt.Int64)
	r.RetentionPluginObservedDays = int(ro.Int64)
	r.QuotaDevices = int(qd.Int64)
	r.QuotaEdges = int(qe.Int64)
	r.QuotaBrowserWS = int(qw.Int64)
	r.QuotaTokens = int(qt.Int64)
	r.QuotaUsers = int(qu.Int64)
	r.QuotaEventsPerMinute = int(qm.Int64)
	r.QuotaPluginInstances = int(qp.Int64)
	return r, nil
}

// Resolve 把行解析成 tenantpolicy.Policy：0/NULL 字段填入 tenantpolicy.Defaults()。
// 解析结果恒为可直接执行的正数上限与天数（永不返回 0，因此 0 不可能被当成「无限」）。
func (r TenantPolicyRow) Resolve() tenantpolicy.Policy {
	p := tenantpolicy.Defaults()
	pick := func(stored, fallback int) int {
		if stored > 0 {
			return stored
		}
		return fallback
	}
	p.Retention.Events = pick(r.RetentionEventsDays, p.Retention.Events)
	p.Retention.TerminalCommands = pick(r.RetentionCommandsDays, p.Retention.TerminalCommands)
	p.Retention.Audit = pick(r.RetentionAuditDays, p.Retention.Audit)
	p.Retention.RevokedTokens = pick(r.RetentionRevokedTokensDays, p.Retention.RevokedTokens)
	p.Retention.PluginObservations = pick(r.RetentionPluginObservedDays, p.Retention.PluginObservations)
	p.Quotas.Devices = pick(r.QuotaDevices, p.Quotas.Devices)
	p.Quotas.Edges = pick(r.QuotaEdges, p.Quotas.Edges)
	p.Quotas.BrowserWS = pick(r.QuotaBrowserWS, p.Quotas.BrowserWS)
	p.Quotas.Tokens = pick(r.QuotaTokens, p.Quotas.Tokens)
	p.Quotas.Users = pick(r.QuotaUsers, p.Quotas.Users)
	p.Quotas.EventsPerMinute = pick(r.QuotaEventsPerMinute, p.Quotas.EventsPerMinute)
	p.Quotas.PluginInstances = pick(r.QuotaPluginInstances, p.Quotas.PluginInstances)
	return p
}

// GetTenantPolicy 读本租户策略行。无策略行时返回全 0 行（= 全部继承默认）且 error 为 nil，
// 调用方无需处理 sql.ErrNoRows；用 Resolve() 取得可执行策略。
func (s *Store) GetTenantPolicy(tenantID int64) (TenantPolicyRow, error) {
	tid, err := s.normalizeTenantID(tenantID)
	if err != nil {
		return TenantPolicyRow{}, err
	}
	row, err := scanTenantPolicy(s.db.QueryRow(
		`SELECT `+tenantPolicyColumns+` FROM tenant_policies WHERE tenant_id=?`, tid))
	if err == sql.ErrNoRows {
		return TenantPolicyRow{TenantID: tid}, nil
	}
	if err != nil {
		return TenantPolicyRow{}, fmt.Errorf("store: get tenant policy: %w", err)
	}
	return row, nil
}

// SetTenantPolicy 写（upsert）本租户策略行。0 字段存 NULL（继承默认）。
// 先用 tenantpolicy 的冻结校验规则验解析结果（单一校验来源，不与 store 各写一份范围），
// 非法值 → tenantpolicy.ErrInvalidPolicy 包装错误，且**不写入任何列**；
// DB CHECK 是第二道防线（手工 SQL 也绕不过）。row.TenantID 被忽略，归属恒取 tenantID 参数。
func (s *Store) SetTenantPolicy(tenantID int64, p TenantPolicyRow) error {
	tid, err := s.normalizeTenantID(tenantID)
	if err != nil {
		return err
	}
	resolved := p.Resolve()
	if err := resolved.Validate(); err != nil {
		return fmt.Errorf("store: set tenant policy: %w", err)
	}
	updated := p.UpdatedAt
	if updated <= 0 {
		updated = now()
	}
	_, err = s.db.Exec(`
		INSERT INTO tenant_policies(tenant_id,
			retention_events_days, retention_commands_days, retention_audit_days,
			retention_revoked_tokens_days, retention_plugin_observed_days,
			quota_devices, quota_edges, quota_browser_ws, quota_tokens, quota_users,
			quota_events_per_min, quota_plugin_instances, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(tenant_id) DO UPDATE SET
			retention_events_days=excluded.retention_events_days,
			retention_commands_days=excluded.retention_commands_days,
			retention_audit_days=excluded.retention_audit_days,
			retention_revoked_tokens_days=excluded.retention_revoked_tokens_days,
			retention_plugin_observed_days=excluded.retention_plugin_observed_days,
			quota_devices=excluded.quota_devices,
			quota_edges=excluded.quota_edges,
			quota_browser_ws=excluded.quota_browser_ws,
			quota_tokens=excluded.quota_tokens,
			quota_users=excluded.quota_users,
			quota_events_per_min=excluded.quota_events_per_min,
			quota_plugin_instances=excluded.quota_plugin_instances,
			updated_at=excluded.updated_at`,
		tid,
		nullInt(p.RetentionEventsDays), nullInt(p.RetentionCommandsDays), nullInt(p.RetentionAuditDays),
		nullInt(p.RetentionRevokedTokensDays), nullInt(p.RetentionPluginObservedDays),
		nullInt(p.QuotaDevices), nullInt(p.QuotaEdges), nullInt(p.QuotaBrowserWS),
		nullInt(p.QuotaTokens), nullInt(p.QuotaUsers), nullInt(p.QuotaEventsPerMinute),
		nullInt(p.QuotaPluginInstances), updated)
	if err != nil {
		return fmt.Errorf("store: set tenant policy: %w", err)
	}
	return nil
}

// CountPluginInstances 返回本租户期望态实例数（配额准入与 UI 计数共用同一口径：
// 只数 Server 权威 desired，不数 Edge 上报投影）。
func (s *Store) CountPluginInstances(tenantID int64) (int, error) {
	tid, err := s.normalizeTenantID(tenantID)
	if err != nil {
		return 0, err
	}
	var n int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM plugin_desired_instances WHERE tenant_id=?`, tid).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: count plugin instances: %w", err)
	}
	return n, nil
}

// pluginInstanceQuotaTx 在写事务内解析本租户 plugin instance 配额上限（NULL/无行 → 默认）。
// 必须由调用方在同一事务内紧接着 COUNT + INSERT 使用，禁止跨事务复用（§4.1 反竞态要求）。
func pluginInstanceQuotaTx(ctx context.Context, conn *sql.Conn, tid int64) (int, error) {
	var limit sql.NullInt64
	err := conn.QueryRowContext(ctx,
		`SELECT quota_plugin_instances FROM tenant_policies WHERE tenant_id=?`, tid).Scan(&limit)
	if err != nil && err != sql.ErrNoRows {
		return 0, fmt.Errorf("store: read plugin instance quota: %w", err)
	}
	if err == nil && limit.Valid && limit.Int64 > 0 {
		return int(limit.Int64), nil
	}
	return tenantpolicy.Defaults().Quotas.PluginInstances, nil
}

// PrunePluginObservations 删除本租户 reported_at < before 的 observed 投影行，返回删除行数。
// 这是 §3.1「plugin observed projection 默认 30 天」的执行面，供 Server sweeper 按租户分批调用：
// 恒带 tenant predicate（清理 A 绝不读取/删除 B），且**只清投影**——desired 期望态与 audit
// 审计都不随 observed 清理（§5「删除期望态不删审计」、§3.1「desired 不随 observed 清理」）。
// 本方法是 §3 契约之外的补充（契约未列保留期执行面），Captain 可选择接线或忽略。
func (s *Store) PrunePluginObservations(tenantID int64, before int64) (int64, error) {
	tid, err := s.normalizeTenantID(tenantID)
	if err != nil {
		return 0, err
	}
	if before <= 0 {
		return 0, fmt.Errorf("store: prune plugin observations: before 必须为正")
	}
	res, err := s.db.Exec(
		`DELETE FROM plugin_observations WHERE tenant_id=? AND reported_at < ?`, tid, before)
	if err != nil {
		return 0, fmt.Errorf("store: prune plugin observations: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: prune plugin observations rows: %w", err)
	}
	return n, nil
}
