package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/DeliciousBuding/cloud-path/internal/api"
)

// Edge 上报的只读投影（docs/architecture/control-plane-sync.md §3.3、§5）。
//
// 边界：Edge 是 observed 唯一权威，Server 只保存投影供 Catalog/UI 读；本文件永不写
// plugin_desired_instances / 永不推进 desired_revision。投影过期只由读侧标记 stale，
// 不得凭空改写 desired（§2 不变量 5）。
//
// 写入语义是**声明式全量快照替换**（scoped 到单个 (tenant_id, edge_id)）：本次上报出现的行
// 写入/刷新，未出现的行删除。这与 api.PluginStatusData「全量插件实际态快照」一致，
// 也是暗卷「无 observed 时必须显式呈现未上报/stale」的前提——纯 upsert 会留下幽灵投影行，
// 让 UI 把早已消失的实例渲染成 observed。DELETE 恒带 tenant_id + edge_id 双 predicate，
// 因此既不触碰其他 edge，也绝不触碰其他租户。
//
// 租户安全：tenant_id 只取自已鉴权的函数参数，永不取自 payload 自报身份；主键含 tenant_id
// 且写入是 delete+insert（无任何 UPDATE 既有行的路径），结构上不可能改写既有行租户归属。

// PluginObservationRow 是 plugin_observations 投影一行：Edge 上报的实例实际态 + 归属坐标。
// 内嵌 api.PluginObservedInstanceData（instance/plugin/version/host_online/state/health/
// detail/restart_count/last_healthy/message_rate），读侧可直接复用 DTO 组装
// api.PluginInstanceObservedView。boot/sequence/applied_revision 不在此重复存储——
// 它们是 per-edge 事实，唯一来源是 plugin_edge_revisions（GetPluginEdgeRevision）。
type PluginObservationRow struct {
	api.PluginObservedInstanceData
	TenantID   int64
	EdgeID     string
	ReportedAt int64
}

// PluginInstallationRow 是 plugin_installations 投影一行：Edge 上报的已安装插件公开事实。
// 内嵌 api.PluginInstallationStatusData。本表只存公开 manifest 字段，
// 永不含本地路径、启动参数、环境变量或 secret 值（§7、tenant-security-policy.md §2.4）。
type PluginInstallationRow struct {
	api.PluginInstallationStatusData
	TenantID   int64
	EdgeID     string
	ReportedAt int64
}

// marshalProjectionJSON 把嵌套 DTO 字段序列化成存储用 JSON；nil 归一为 {} / []，
// 与 DB DEFAULT 一致，保证读侧反序列化永不遇到 "null"。
func marshalProjectionJSON(v any, emptyArray bool) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("store: marshal plugin projection field: %w", err)
	}
	if string(b) == "null" {
		if emptyArray {
			return "[]", nil
		}
		return "{}", nil
	}
	return string(b), nil
}

// unmarshalProjectionJSON 反序列化投影 JSON 列。空串按对应空值处理；
// 非法 JSON 直接返回错误（fail-closed）：投影被篡改/损坏时宁可报错，
// 也不把错误事实当作 Edge 真实上报呈现给 UI。
func unmarshalProjectionJSON(s string, out any) error {
	if s == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(s), out); err != nil {
		return fmt.Errorf("store: plugin projection JSON 损坏: %w", err)
	}
	return nil
}

// UpsertPluginObservations 写入某 (tenant, edge) 的 observed 实例全量快照。
// reportedAt<=0 时用 now()。rows 为该 edge 的完整快照：nil/空表示该 edge 当前无实例运行，
// 会清空该 (tenant,edge) 的投影（不影响其他 edge / 其他租户）。
// edge 已绑定其他租户 → ErrEdgeTenantMismatch 且整批不写入（暗卷：伪造上报不污染任一租户投影）。
func (s *Store) UpsertPluginObservations(tenantID int64, edgeID string, rows []api.PluginObservedInstanceData, reportedAt int64) error {
	tid, err := s.normalizeTenantID(tenantID)
	if err != nil {
		return err
	}
	if edgeID == "" {
		return fmt.Errorf("%w: edge_id 不可为空", ErrPluginIdentityIncomplete)
	}
	seen := make(map[string]struct{}, len(rows))
	for i := range rows {
		if rows[i].InstanceID == "" {
			return fmt.Errorf("%w: observed instance_id 不可为空", ErrPluginIdentityIncomplete)
		}
		if _, dup := seen[rows[i].InstanceID]; dup {
			return fmt.Errorf("%w: 同一次上报含重复 instance_id %q", ErrPluginIdentityIncomplete, rows[i].InstanceID)
		}
		seen[rows[i].InstanceID] = struct{}{}
	}
	ts := reportedAt
	if ts <= 0 {
		ts = now()
	}

	ctx := context.Background()
	return s.withWriteConn(ctx, func(conn *sql.Conn) error {
		if err := assertEdgeNotBoundToOtherTenant(ctx, conn, edgeID, tid); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx,
			`DELETE FROM plugin_observations WHERE tenant_id=? AND edge_id=?`, tid, edgeID); err != nil {
			return fmt.Errorf("store: clear plugin observations: %w", err)
		}
		for i := range rows {
			o := rows[i]
			hostOnline := 0
			if o.HostOnline {
				hostOnline = 1
			}
			if _, err := conn.ExecContext(ctx, `
				INSERT INTO plugin_observations(tenant_id, edge_id, instance_id, plugin_id, version,
					host_online, state, health, detail, restart_count, last_healthy, message_rate, reported_at)
				VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				tid, edgeID, o.InstanceID, o.PluginID, o.Version, hostOnline, o.State, o.Health,
				o.Detail, o.RestartCount, o.LastHealthy, o.MessageRate, ts); err != nil {
				return fmt.Errorf("store: insert plugin observation: %w", err)
			}
		}
		return nil
	})
}

// UpsertPluginInstallations 写入某 (tenant, edge) 的已安装插件全量快照（语义同上）。
// 时间戳取 now()（契约未传 reportedAt）；per-edge 的 last_report_at 由 SetPluginEdgeReport 记录。
func (s *Store) UpsertPluginInstallations(tenantID int64, edgeID string, rows []api.PluginInstallationStatusData) error {
	tid, err := s.normalizeTenantID(tenantID)
	if err != nil {
		return err
	}
	if edgeID == "" {
		return fmt.Errorf("%w: edge_id 不可为空", ErrPluginIdentityIncomplete)
	}
	type staged struct {
		pluginID, version, kind, digest, trustMode, publisher string
		protocol                                              int
		verified                                              int
		perms, contribs, caps                                 string
	}
	seen := make(map[string]struct{}, len(rows))
	stagedRows := make([]staged, 0, len(rows))
	for i := range rows {
		in := rows[i]
		if in.PluginID == "" {
			return fmt.Errorf("%w: installation plugin_id 不可为空", ErrPluginIdentityIncomplete)
		}
		if _, dup := seen[in.PluginID]; dup {
			return fmt.Errorf("%w: 同一次上报含重复 plugin_id %q", ErrPluginIdentityIncomplete, in.PluginID)
		}
		seen[in.PluginID] = struct{}{}
		perms, err := marshalProjectionJSON(in.Permissions, false)
		if err != nil {
			return err
		}
		contribs, err := marshalProjectionJSON(in.Contributions, false)
		if err != nil {
			return err
		}
		caps, err := marshalProjectionJSON(in.Capabilities, true)
		if err != nil {
			return err
		}
		verified := 0
		if in.Verified {
			verified = 1
		}
		stagedRows = append(stagedRows, staged{
			pluginID: in.PluginID, version: in.Version, kind: in.Kind, digest: in.Digest,
			trustMode: in.TrustMode, publisher: in.VerifiedPublisher, protocol: in.Protocol,
			verified: verified, perms: perms, contribs: contribs, caps: caps,
		})
	}
	ts := now()

	ctx := context.Background()
	return s.withWriteConn(ctx, func(conn *sql.Conn) error {
		if err := assertEdgeNotBoundToOtherTenant(ctx, conn, edgeID, tid); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx,
			`DELETE FROM plugin_installations WHERE tenant_id=? AND edge_id=?`, tid, edgeID); err != nil {
			return fmt.Errorf("store: clear plugin installations: %w", err)
		}
		for _, r := range stagedRows {
			if _, err := conn.ExecContext(ctx, `
				INSERT INTO plugin_installations(tenant_id, edge_id, plugin_id, version, kind, protocol,
					digest, trust_mode, verified, verified_publisher, permissions_json,
					contributions_json, capabilities_json, reported_at)
				VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				tid, edgeID, r.pluginID, r.version, r.kind, r.protocol, r.digest, r.trustMode,
				r.verified, r.publisher, r.perms, r.contribs, r.caps, ts); err != nil {
				return fmt.Errorf("store: insert plugin installation: %w", err)
			}
		}
		return nil
	})
}

// ListPluginObservationsTenant 返回本租户全部 observed 投影，按 (edge_id, instance_id) 有序。
// 恒带 tenant predicate；tenantID<=0 解析为 default 租户，绝不当作「不过滤」。
func (s *Store) ListPluginObservationsTenant(tenantID int64) ([]PluginObservationRow, error) {
	tid, err := s.normalizeTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT tenant_id, edge_id, instance_id, plugin_id, version, host_online,
		state, health, detail, restart_count, last_healthy, message_rate, reported_at
		FROM plugin_observations WHERE tenant_id=? ORDER BY edge_id, instance_id`, tid)
	if err != nil {
		return nil, fmt.Errorf("store: list plugin observations: %w", err)
	}
	defer rows.Close()
	var out []PluginObservationRow
	for rows.Next() {
		var (
			r          PluginObservationRow
			hostOnline int
		)
		if err := rows.Scan(&r.TenantID, &r.EdgeID, &r.InstanceID, &r.PluginID, &r.Version, &hostOnline,
			&r.State, &r.Health, &r.Detail, &r.RestartCount, &r.LastHealthy, &r.MessageRate,
			&r.ReportedAt); err != nil {
			return nil, fmt.Errorf("store: scan plugin observation: %w", err)
		}
		r.HostOnline = hostOnline != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListPluginInstallationsTenant 返回本租户全部安装物投影，按 (edge_id, plugin_id) 有序。
func (s *Store) ListPluginInstallationsTenant(tenantID int64) ([]PluginInstallationRow, error) {
	tid, err := s.normalizeTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT tenant_id, edge_id, plugin_id, version, kind, protocol, digest,
		trust_mode, verified, verified_publisher, permissions_json, contributions_json,
		capabilities_json, reported_at
		FROM plugin_installations WHERE tenant_id=? ORDER BY edge_id, plugin_id`, tid)
	if err != nil {
		return nil, fmt.Errorf("store: list plugin installations: %w", err)
	}
	defer rows.Close()
	var out []PluginInstallationRow
	for rows.Next() {
		var (
			r                     PluginInstallationRow
			verified              int
			perms, contribs, caps string
		)
		if err := rows.Scan(&r.TenantID, &r.EdgeID, &r.PluginID, &r.Version, &r.Kind, &r.Protocol,
			&r.Digest, &r.TrustMode, &verified, &r.VerifiedPublisher, &perms, &contribs, &caps,
			&r.ReportedAt); err != nil {
			return nil, fmt.Errorf("store: scan plugin installation: %w", err)
		}
		r.Verified = verified != 0
		if err := unmarshalProjectionJSON(perms, &r.Permissions); err != nil {
			return nil, err
		}
		if err := unmarshalProjectionJSON(contribs, &r.Contributions); err != nil {
			return nil, err
		}
		if err := unmarshalProjectionJSON(caps, &r.Capabilities); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
