package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/DeliciousBuding/cloud-path/internal/tenantpolicy"
)

// 插件控制面期望态与同步游标持久化（docs/architecture/control-plane-sync.md §2、§3、§5、§8）。
//
// 权威划分：Server 是 desired / revision 唯一权威，本文件所有写方法只服务 Server 写方向；
// Edge 上报的 observed 投影在 plugin_projection.go，两者永不互相改写。
//
// 三条结构性保证（不靠调用方自觉）：
//  1. 期望态的每次写入都在 BEGIN IMMEDIATE 单写事务内完成「改行 + 单调推进 (tenant,edge)
//     desired_revision」，因此 revision 严格递增且无空洞：失败整体回滚，不消费 revision。
//  2. 所有表主键首列是 tenant_id，且 UPDATE 的 SET 列表从不包含 tenant_id，
//     所以任何 upsert 都不可能改写既有行的租户归属；读路径恒带 tenant predicate。
//  3. secret 只以 config_json 内的 `secret://<name>` handle 与 secret_refs 名称存在，
//     本包不建任何明文列，也不解析 handle（tenant-security-policy.md §2.2）。

// 插件控制面稳定 sentinel 错误：调用方用 errors.Is 判断并映射到 api.PluginErr* 稳定码，
// 不要依赖错误文本。
var (
	// ErrPluginInstanceNotFound 表示 (tenant, edge, instance) 期望态行不存在 → api.PluginErrNotFound。
	ErrPluginInstanceNotFound = errors.New("store: plugin instance not found")
	// ErrPluginInstanceConflict 表示 Create 目标实例已存在 → api.PluginErrConflict。
	ErrPluginInstanceConflict = errors.New("store: plugin instance already exists")
	// ErrPluginQuotaExceeded 表示租户 plugin instance 配额已满：写在事务内被原子拒绝，
	// revision 未推进、无任何半状态 → api.PluginErrQuota（tenant-security-policy.md §4.1、§6.2）。
	ErrPluginQuotaExceeded = errors.New("store: plugin instance quota exceeded")
	// ErrPluginIdentityIncomplete 表示 edge_id / instance_id / plugin_id / boot_id 缺失，fail-closed。
	ErrPluginIdentityIncomplete = errors.New("store: plugin identity incomplete")
	// ErrPluginConfigInvalid 表示 config_json / secret_refs 不是契约要求的 JSON 形状
	// → api.PluginErrInvalidConfig。
	ErrPluginConfigInvalid = errors.New("store: plugin config or secret refs invalid")
	// ErrPluginImmutableField 表示试图改写不可变字段（实例的 plugin_id 归属）；
	// 换插件是新实例语义，必须走 Create，不得由 Update 静默改写。
	ErrPluginImmutableField = errors.New("store: plugin instance immutable field changed")
)

// PluginInstanceRow 是 plugin_desired_instances 一行（Server 权威期望态）。
// ConfigJSON 是 map[string]string 的 JSON，值只含非敏感标量或 secret:// handle；
// SecretRefs 是 JSON []string，只含 handle 名称。Revision 是该行最后一次写入时所属
// (tenant_id, edge_id) 的 desired revision（写入时由 store 赋值，调用方传入值被忽略）。
type PluginInstanceRow struct {
	TenantID   int64
	EdgeID     string
	InstanceID string
	PluginID   string
	Version    string
	Enabled    bool
	Isolation  string
	ConfigJSON string
	SecretRefs string
	Revision   uint64
	CreatedAt  int64
	UpdatedAt  int64
}

// PluginEdgeRevisionRow 是 plugin_edge_revisions 一行：每 (tenant, edge) 的同步游标。
type PluginEdgeRevisionRow struct {
	TenantID        int64
	EdgeID          string
	DesiredRevision uint64
	AppliedRevision uint64
	BootID          string
	LastSequence    uint64
	LastReportAt    int64
	LastAckAt       int64
}

const pluginInstanceColumns = `tenant_id, edge_id, instance_id, plugin_id, version, enabled,
	isolation, config_json, secret_refs, revision, created_at, updated_at`

// u64NonNeg 把 SQLite INTEGER（int64）转成契约要求的 uint64。
// 所有 revision/sequence 列都有 CHECK(... >= 0)，负值不可能落库；此处夹到 0 只是纵深防御，
// 且方向安全（偏小的 revision 会被 Edge 当作 stale 拒绝，不会误应用）。
func u64NonNeg(v int64) uint64 {
	if v < 0 {
		return 0
	}
	return uint64(v)
}

func scanPluginInstance(sc interface{ Scan(...any) error }) (PluginInstanceRow, error) {
	var (
		r        PluginInstanceRow
		enabled  int
		revision int64
	)
	err := sc.Scan(&r.TenantID, &r.EdgeID, &r.InstanceID, &r.PluginID, &r.Version, &enabled,
		&r.Isolation, &r.ConfigJSON, &r.SecretRefs, &revision, &r.CreatedAt, &r.UpdatedAt)
	r.Enabled = enabled != 0
	r.Revision = u64NonNeg(revision)
	return r, err
}

func scanPluginEdgeRevision(sc interface{ Scan(...any) error }) (PluginEdgeRevisionRow, error) {
	var (
		r       PluginEdgeRevisionRow
		desired int64
		applied int64
		seq     int64
	)
	err := sc.Scan(&r.TenantID, &r.EdgeID, &desired, &applied, &r.BootID, &seq,
		&r.LastReportAt, &r.LastAckAt)
	r.DesiredRevision = u64NonNeg(desired)
	r.AppliedRevision = u64NonNeg(applied)
	r.LastSequence = u64NonNeg(seq)
	return r, err
}

// withWriteConn 取一条专用连接跑 IMMEDIATE 写事务：commit 前任何错误整体回滚，
// 保证「不留半状态、不消费 revision」。返回的 commit 由调用方在全部写入成功后调用。
func (s *Store) withWriteConn(ctx context.Context, fn func(conn *sql.Conn) error) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("store: acquire write conn: %w", err)
	}
	defer conn.Close()
	if err := beginImmediate(ctx, conn); err != nil {
		return fmt.Errorf("store: begin immediate: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			rollback(ctx, conn)
		}
	}()
	if err := fn(conn); err != nil {
		return err
	}
	if err := commit(ctx, conn); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	committed = true
	return nil
}

// assertEdgeNotBoundToOtherTenant 校验 edgeID 未被其他租户占用，否则整批写入 fail-closed。
// 本仓没有独立 edges 注册表：edge 归属由 devices 与插件控制面四表既有的 (tenant_id, edge_id)
// 绑定共同推导；任一来源显示该 edge 属于别的租户即拒绝，且不改动任何租户的既有行
// （暗卷「Tenant B 伪造 Tenant A 的 edge id 上报」要求：不写入任何投影、不驱逐 A）。
// 错误文本只回显调用方自己提供的 edge_id，绝不回显其他租户的行键，避免跨租户信息泄漏。
func assertEdgeNotBoundToOtherTenant(ctx context.Context, conn *sql.Conn, edgeID string, tid int64) error {
	queries := []string{
		`SELECT 1 FROM devices WHERE edge_id=? AND tenant_id<>? LIMIT 1`,
		`SELECT 1 FROM plugin_desired_instances WHERE edge_id=? AND tenant_id<>? LIMIT 1`,
		`SELECT 1 FROM plugin_edge_revisions WHERE edge_id=? AND tenant_id<>? LIMIT 1`,
		`SELECT 1 FROM plugin_observations WHERE edge_id=? AND tenant_id<>? LIMIT 1`,
		`SELECT 1 FROM plugin_installations WHERE edge_id=? AND tenant_id<>? LIMIT 1`,
	}
	for _, q := range queries {
		var one int
		err := conn.QueryRowContext(ctx, q, edgeID, tid).Scan(&one)
		if err == nil {
			return fmt.Errorf("%w: edge %q", ErrEdgeTenantMismatch, edgeID)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("store: check edge tenant binding: %w", err)
		}
	}
	return nil
}

// ensureEdgeRevisionTx 幂等建 (tenant, edge) 游标行；ON CONFLICT DO NOTHING 保证既有
// revision / boot 身份不被覆盖（重入安全）。
func ensureEdgeRevisionTx(ctx context.Context, conn *sql.Conn, tid int64, edgeID string) error {
	_, err := conn.ExecContext(ctx, `
		INSERT INTO plugin_edge_revisions(tenant_id, edge_id) VALUES(?,?)
		ON CONFLICT(tenant_id, edge_id) DO NOTHING`, tid, edgeID)
	if err != nil {
		return fmt.Errorf("store: ensure plugin edge revision: %w", err)
	}
	return nil
}

// bumpDesiredRevisionTx 在写事务内把 (tenant, edge) desired_revision +1 并返回新值。
// 必须与期望态行改动同事务提交：这是 revision 单调且无空洞的唯一来源。
func bumpDesiredRevisionTx(ctx context.Context, conn *sql.Conn, tid int64, edgeID string) (uint64, error) {
	var cur int64
	if err := conn.QueryRowContext(ctx,
		`SELECT desired_revision FROM plugin_edge_revisions WHERE tenant_id=? AND edge_id=?`,
		tid, edgeID).Scan(&cur); err != nil {
		return 0, fmt.Errorf("store: read desired revision: %w", err)
	}
	next := cur + 1
	if _, err := conn.ExecContext(ctx,
		`UPDATE plugin_edge_revisions SET desired_revision=? WHERE tenant_id=? AND edge_id=?`,
		next, tid, edgeID); err != nil {
		return 0, fmt.Errorf("store: bump desired revision: %w", err)
	}
	return u64NonNeg(next), nil
}

// validatePluginIdentity 要求 edge/instance/plugin 三个身份段都非空（fail-closed）。
func validatePluginIdentity(edgeID, instanceID, pluginID string) error {
	if edgeID == "" || instanceID == "" || pluginID == "" {
		return fmt.Errorf("%w: edge_id/instance_id/plugin_id 均不可为空", ErrPluginIdentityIncomplete)
	}
	return nil
}

// normalizePluginPayload 校验并规范化 config_json / secret_refs。
// 契约形状：config_json 是 map[string]string 的 JSON（值只含非敏感标量或 secret:// handle），
// secret_refs 是 []string 的 JSON（只含 handle 名称）。非字符串值、嵌套对象、数组或 null 一律拒绝——
// 这从结构上堵死「把明文 secret 藏进嵌套 JSON」的路径，也保证读侧永远能反序列化。
// 空串归一为 {} / []；通过校验后按 Go 规范形式（key 有序）重写，使同内容存储字节稳定。
func normalizePluginPayload(configJSON, secretRefs string) (string, string, error) {
	cfg := configJSON
	if cfg == "" {
		cfg = "{}"
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(cfg), &m); err != nil {
		return "", "", fmt.Errorf("%w: config_json 必须是 map[string]string 的 JSON", ErrPluginConfigInvalid)
	}
	if m == nil {
		cfg = "{}"
	} else {
		b, err := json.Marshal(m)
		if err != nil {
			return "", "", fmt.Errorf("%w: config_json 规范化失败", ErrPluginConfigInvalid)
		}
		cfg = string(b)
	}

	refs := secretRefs
	if refs == "" {
		refs = "[]"
	}
	var arr []string
	if err := json.Unmarshal([]byte(refs), &arr); err != nil {
		return "", "", fmt.Errorf("%w: secret_refs 必须是 []string 的 JSON", ErrPluginConfigInvalid)
	}
	for _, r := range arr {
		if r == "" {
			return "", "", fmt.Errorf("%w: secret_refs 含空 handle 名称", ErrPluginConfigInvalid)
		}
	}
	if arr == nil {
		refs = "[]"
	} else {
		b, err := json.Marshal(arr)
		if err != nil {
			return "", "", fmt.Errorf("%w: secret_refs 规范化失败", ErrPluginConfigInvalid)
		}
		refs = string(b)
	}
	return cfg, refs, nil
}

// ListPluginInstancesTenant 返回本租户全部期望态实例，按 (edge_id, instance_id) 有序。
// 恒带 tenant predicate；tenantID<=0 解析为 default 租户，绝不当作「不过滤」。
func (s *Store) ListPluginInstancesTenant(tenantID int64) ([]PluginInstanceRow, error) {
	tid, err := s.normalizeTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT `+pluginInstanceColumns+` FROM plugin_desired_instances
		WHERE tenant_id=? ORDER BY edge_id, instance_id`, tid)
	if err != nil {
		return nil, fmt.Errorf("store: list plugin instances: %w", err)
	}
	defer rows.Close()
	var out []PluginInstanceRow
	for rows.Next() {
		r, err := scanPluginInstance(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan plugin instance: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListPluginInstancesAll 返回全部租户的全部期望态实例（Server 侧 AppHost reconcile 用；
// 按 tenant/edge/instance 有序）。Edge 侧读面禁止使用本方法（Edge 必须走租户过滤版本）。
func (s *Store) ListPluginInstancesAll() ([]PluginInstanceRow, error) {
	rows, err := s.db.Query(`SELECT ` + pluginInstanceColumns + ` FROM plugin_desired_instances
		ORDER BY tenant_id, edge_id, instance_id`)
	if err != nil {
		return nil, fmt.Errorf("store: list plugin instances (all): %w", err)
	}
	defer rows.Close()
	var out []PluginInstanceRow
	for rows.Next() {
		r, err := scanPluginInstance(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan plugin instance: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetPluginInstance 读单个期望态实例；不存在返回 (zero, false, nil)。
func (s *Store) GetPluginInstance(tenantID int64, edgeID, instanceID string) (PluginInstanceRow, bool, error) {
	tid, err := s.normalizeTenantID(tenantID)
	if err != nil {
		return PluginInstanceRow{}, false, err
	}
	row, err := scanPluginInstance(s.db.QueryRow(`SELECT `+pluginInstanceColumns+`
		FROM plugin_desired_instances WHERE tenant_id=? AND edge_id=? AND instance_id=?`,
		tid, edgeID, instanceID))
	if errors.Is(err, sql.ErrNoRows) {
		return PluginInstanceRow{}, false, nil
	}
	if err != nil {
		return PluginInstanceRow{}, false, fmt.Errorf("store: get plugin instance: %w", err)
	}
	return row, true, nil
}

// CreatePluginInstance 新建期望态实例并在同一事务内原子推进 (tenant,edge) desired revision，
// 返回新 revision。row.Revision / CreatedAt / UpdatedAt 由 store 赋值（CreatedAt<=0 用 now()）。
//
// 拒绝路径（全部整体回滚，revision 不消费、无半状态）：
//   - 身份段缺失 → ErrPluginIdentityIncomplete
//   - config/secret_refs 形状非法 → ErrPluginConfigInvalid
//   - edge 已绑定其他租户 → ErrEdgeTenantMismatch
//   - 配额已满 → ErrPluginQuotaExceeded（计数与写入同事务，杜绝「先 COUNT 后 INSERT」竞态）
//   - 实例已存在 → ErrPluginInstanceConflict
func (s *Store) CreatePluginInstance(row PluginInstanceRow) (uint64, error) {
	tid, err := s.normalizeTenantID(row.TenantID)
	if err != nil {
		return 0, err
	}
	if err := validatePluginIdentity(row.EdgeID, row.InstanceID, row.PluginID); err != nil {
		return 0, err
	}
	cfg, refs, err := normalizePluginPayload(row.ConfigJSON, row.SecretRefs)
	if err != nil {
		return 0, err
	}

	enabled := 0
	if row.Enabled {
		enabled = 1
	}
	var rev uint64
	ctx := context.Background()
	err = s.withWriteConn(ctx, func(conn *sql.Conn) error {
		if err := assertEdgeNotBoundToOtherTenant(ctx, conn, row.EdgeID, tid); err != nil {
			return err
		}
		// 配额原子准入：tenant-security-policy.md §4 规定拒绝点是「desired instance 创建事务内」。
		limit, err := pluginInstanceQuotaTx(ctx, conn, tid)
		if err != nil {
			return err
		}
		var count int
		if err := conn.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM plugin_desired_instances WHERE tenant_id=?`, tid).Scan(&count); err != nil {
			return fmt.Errorf("store: count plugin instances: %w", err)
		}
		if count >= limit {
			return fmt.Errorf("%w: tenant=%d resource=%s usage=%d limit=%d", ErrPluginQuotaExceeded,
				tid, tenantpolicy.ResourcePluginInstances, count, limit)
		}

		var dup int
		err = conn.QueryRowContext(ctx, `SELECT 1 FROM plugin_desired_instances
			WHERE tenant_id=? AND edge_id=? AND instance_id=?`, tid, row.EdgeID, row.InstanceID).Scan(&dup)
		if err == nil {
			return fmt.Errorf("%w: %s/%s", ErrPluginInstanceConflict, row.EdgeID, row.InstanceID)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("store: check plugin instance exists: %w", err)
		}

		if err := ensureEdgeRevisionTx(ctx, conn, tid, row.EdgeID); err != nil {
			return err
		}
		next, err := bumpDesiredRevisionTx(ctx, conn, tid, row.EdgeID)
		if err != nil {
			return err
		}
		rev = next

		ts := row.CreatedAt
		if ts <= 0 {
			ts = now()
		}
		updated := row.UpdatedAt
		if updated <= 0 {
			updated = ts
		}
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO plugin_desired_instances(tenant_id, edge_id, instance_id, plugin_id, version,
				enabled, isolation, config_json, secret_refs, revision, created_at, updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
			tid, row.EdgeID, row.InstanceID, row.PluginID, row.Version, enabled, row.Isolation,
			cfg, refs, int64(next), ts, updated); err != nil {
			return fmt.Errorf("store: insert plugin instance: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return rev, nil
}

// UpdatePluginInstance 更新既有实例并原子推进 revision，返回新 revision。
// 不可变字段：tenant_id（结构性不可改）、created_at（不改写历史）、plugin_id（换插件属新实例，
// 传空表示保留，传不同值 → ErrPluginImmutableField）。不存在 → ErrPluginInstanceNotFound。
// 更新不占配额（没有新增实例），因此不做配额准入。
func (s *Store) UpdatePluginInstance(row PluginInstanceRow) (uint64, error) {
	tid, err := s.normalizeTenantID(row.TenantID)
	if err != nil {
		return 0, err
	}
	if row.EdgeID == "" || row.InstanceID == "" {
		return 0, fmt.Errorf("%w: edge_id/instance_id 均不可为空", ErrPluginIdentityIncomplete)
	}
	cfg, refs, err := normalizePluginPayload(row.ConfigJSON, row.SecretRefs)
	if err != nil {
		return 0, err
	}

	enabled := 0
	if row.Enabled {
		enabled = 1
	}
	var rev uint64
	ctx := context.Background()
	err = s.withWriteConn(ctx, func(conn *sql.Conn) error {
		// created_at 刻意不读也不写：更新不改写历史。
		var curPluginID string
		err := conn.QueryRowContext(ctx, `SELECT plugin_id FROM plugin_desired_instances
			WHERE tenant_id=? AND edge_id=? AND instance_id=?`,
			tid, row.EdgeID, row.InstanceID).Scan(&curPluginID)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: %s/%s", ErrPluginInstanceNotFound, row.EdgeID, row.InstanceID)
		}
		if err != nil {
			return fmt.Errorf("store: read plugin instance for update: %w", err)
		}
		if row.PluginID != "" && row.PluginID != curPluginID {
			return fmt.Errorf("%w: plugin_id %q -> %q", ErrPluginImmutableField, curPluginID, row.PluginID)
		}

		if err := ensureEdgeRevisionTx(ctx, conn, tid, row.EdgeID); err != nil {
			return err
		}
		next, err := bumpDesiredRevisionTx(ctx, conn, tid, row.EdgeID)
		if err != nil {
			return err
		}
		rev = next

		updated := row.UpdatedAt
		if updated <= 0 {
			updated = now()
		}
		// SET 列表刻意不含 tenant_id 与 created_at。
		res, err := conn.ExecContext(ctx, `
			UPDATE plugin_desired_instances SET
				version=?, enabled=?, isolation=?, config_json=?, secret_refs=?, revision=?, updated_at=?
			WHERE tenant_id=? AND edge_id=? AND instance_id=?`,
			row.Version, enabled, row.Isolation, cfg, refs, int64(next), updated,
			tid, row.EdgeID, row.InstanceID)
		if err != nil {
			return fmt.Errorf("store: update plugin instance: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("store: update plugin instance rows: %w", err)
		}
		if n != 1 {
			return fmt.Errorf("%w: %s/%s", ErrPluginInstanceNotFound, row.EdgeID, row.InstanceID)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return rev, nil
}

// DeletePluginInstance 删除期望态实例并原子推进 revision（Edge 需靠新 revision 收敛「实例已移除」），
// 返回新 revision。不存在 → ErrPluginInstanceNotFound（不消费 revision）。
//
// purge=false（默认）：只删期望态，保留 Edge 上报的 observed 投影与插件安装事实。
// purge=true：额外删除该实例在 plugin_observations 的投影行（API 语义「显式 purge 才删数据」）。
// 两种情况都**绝不**删除 audit_events（契约：删除期望态不删审计），也不删 plugin_installations
// ——安装物是 per-plugin 事实，可能被同 edge 的其他实例共享。
func (s *Store) DeletePluginInstance(tenantID int64, edgeID, instanceID string, purge bool) (uint64, error) {
	tid, err := s.normalizeTenantID(tenantID)
	if err != nil {
		return 0, err
	}
	if edgeID == "" || instanceID == "" {
		return 0, fmt.Errorf("%w: edge_id/instance_id 均不可为空", ErrPluginIdentityIncomplete)
	}
	var rev uint64
	ctx := context.Background()
	err = s.withWriteConn(ctx, func(conn *sql.Conn) error {
		res, err := conn.ExecContext(ctx, `DELETE FROM plugin_desired_instances
			WHERE tenant_id=? AND edge_id=? AND instance_id=?`, tid, edgeID, instanceID)
		if err != nil {
			return fmt.Errorf("store: delete plugin instance: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("store: delete plugin instance rows: %w", err)
		}
		if n == 0 {
			return fmt.Errorf("%w: %s/%s", ErrPluginInstanceNotFound, edgeID, instanceID)
		}
		if purge {
			// 双 predicate（tenant_id + edge_id）锁定范围：不触碰其他 edge / 其他租户投影。
			if _, err := conn.ExecContext(ctx, `DELETE FROM plugin_observations
				WHERE tenant_id=? AND edge_id=? AND instance_id=?`, tid, edgeID, instanceID); err != nil {
				return fmt.Errorf("store: purge plugin observation: %w", err)
			}
		}
		if err := ensureEdgeRevisionTx(ctx, conn, tid, edgeID); err != nil {
			return err
		}
		next, err := bumpDesiredRevisionTx(ctx, conn, tid, edgeID)
		if err != nil {
			return err
		}
		rev = next
		return nil
	})
	if err != nil {
		return 0, err
	}
	return rev, nil
}

// PluginDesiredRevision 返回 (tenant, edge) 当前 desired revision；从未写过返回 0。
func (s *Store) PluginDesiredRevision(tenantID int64, edgeID string) (uint64, error) {
	tid, err := s.normalizeTenantID(tenantID)
	if err != nil {
		return 0, err
	}
	var rev int64
	err = s.db.QueryRow(`SELECT desired_revision FROM plugin_edge_revisions
		WHERE tenant_id=? AND edge_id=?`, tid, edgeID).Scan(&rev)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("store: read plugin desired revision: %w", err)
	}
	return u64NonNeg(rev), nil
}

// GetPluginEdgeRevision 读 (tenant, edge) 同步游标。从未同步过返回全零行（TenantID/EdgeID 已填）
// 且 error 为 nil，调用方无需处理 sql.ErrNoRows。
func (s *Store) GetPluginEdgeRevision(tenantID int64, edgeID string) (PluginEdgeRevisionRow, error) {
	tid, err := s.normalizeTenantID(tenantID)
	if err != nil {
		return PluginEdgeRevisionRow{}, err
	}
	row, err := scanPluginEdgeRevision(s.db.QueryRow(`SELECT tenant_id, edge_id, desired_revision,
		applied_revision, boot_id, last_sequence, last_report_at, last_ack_at
		FROM plugin_edge_revisions WHERE tenant_id=? AND edge_id=?`, tid, edgeID))
	if errors.Is(err, sql.ErrNoRows) {
		return PluginEdgeRevisionRow{TenantID: tid, EdgeID: edgeID}, nil
	}
	if err != nil {
		return PluginEdgeRevisionRow{}, fmt.Errorf("store: get plugin edge revision: %w", err)
	}
	return row, nil
}

// pluginEdgeCursor 是写侧读取的游标内部形态（含 prev_boot_id，不外泄到契约结构体）。
type pluginEdgeCursor struct {
	desiredRevision int64
	appliedRevision int64
	bootID          string
	prevBootID      string
	lastSequence    int64
}

func readPluginEdgeCursorTx(ctx context.Context, conn *sql.Conn, tid int64, edgeID string) (pluginEdgeCursor, error) {
	var c pluginEdgeCursor
	err := conn.QueryRowContext(ctx, `SELECT desired_revision, applied_revision, boot_id,
		prev_boot_id, last_sequence FROM plugin_edge_revisions WHERE tenant_id=? AND edge_id=?`,
		tid, edgeID).Scan(&c.desiredRevision, &c.appliedRevision, &c.bootID, &c.prevBootID, &c.lastSequence)
	if err != nil {
		return c, fmt.Errorf("store: read plugin edge cursor: %w", err)
	}
	return c, nil
}

// applyBootSequenceRules 判定一次 Edge 上报/ack 是否应被接受，并给出接受后的游标值。
// 规则来自 control-plane-sync.md §4.1（同 boot sequence 单调递增，重复/倒序忽略；新 boot 可从 1 开始）
// 与 §8（旧 boot_id 的更大 sequence 在新 boot 上线后迟到必须忽略）。
// 返回 accepted=false 时调用方必须不写任何列（幂等忽略，返回 nil error）。
func applyBootSequenceRules(cur pluginEdgeCursor, bootID string, seq uint64) (accepted bool, prevBootID string) {
	switch {
	case cur.prevBootID != "" && bootID == cur.prevBootID:
		// 被取代的上一代 boot 迟到消息：忽略，绝不复活旧 boot 或改写其序号。
		return false, cur.prevBootID
	case cur.bootID != "" && bootID == cur.bootID:
		// 同一 boot：sequence 必须严格递增，重复与倒序一律忽略。
		if seq <= u64NonNeg(cur.lastSequence) {
			return false, cur.prevBootID
		}
		return true, cur.prevBootID
	default:
		// 未见过的 boot_id = 进程重启（新 boot 可从 sequence 1 开始）：接受并轮换，
		// 把当前 boot 记入 prev_boot_id 以便丢弃它的迟到消息。
		return true, cur.bootID
	}
}

// SetPluginEdgeReport 记录 Edge 一次 plugin_status 上报（boot 身份 + sequence + 时间），
// 不改 applied_revision。重复/倒序/旧 boot 迟到消息按契约幂等忽略（返回 nil，不写任何列）。
func (s *Store) SetPluginEdgeReport(tenantID int64, edgeID, bootID string, seq uint64, at int64) error {
	return s.setPluginEdgeCursor(tenantID, edgeID, bootID, seq, 0, at, false)
}

// SetPluginEdgeApplied 在 Edge ack=applied 后推进 applied_revision 并记 last_ack_at。
// 除 boot/sequence 规则外再加两条单调守卫（不满足即幂等忽略，不写任何列）：
//   - applied_revision 不得回退（Server 侧记录只增，避免 UI/下发出现回归）；
//   - applied_revision 不得超过本 (tenant,edge) 的 desired_revision（Edge 不能声称应用了
//     Server 从未下发过的 revision，control-plane-sync.md §2 不变量 2、3）。
func (s *Store) SetPluginEdgeApplied(tenantID int64, edgeID, bootID string, seq, appliedRevision uint64, at int64) error {
	return s.setPluginEdgeCursor(tenantID, edgeID, bootID, seq, appliedRevision, at, true)
}

func (s *Store) setPluginEdgeCursor(tenantID int64, edgeID, bootID string, seq, appliedRevision uint64, at int64, isAck bool) error {
	tid, err := s.normalizeTenantID(tenantID)
	if err != nil {
		return err
	}
	if edgeID == "" || bootID == "" {
		return fmt.Errorf("%w: edge_id/boot_id 均不可为空", ErrPluginIdentityIncomplete)
	}
	ts := at
	if ts <= 0 {
		ts = now()
	}
	ctx := context.Background()
	return s.withWriteConn(ctx, func(conn *sql.Conn) error {
		if err := assertEdgeNotBoundToOtherTenant(ctx, conn, edgeID, tid); err != nil {
			return err
		}
		if err := ensureEdgeRevisionTx(ctx, conn, tid, edgeID); err != nil {
			return err
		}
		cur, err := readPluginEdgeCursorTx(ctx, conn, tid, edgeID)
		if err != nil {
			return err
		}
		accepted, prevBootID := applyBootSequenceRules(cur, bootID, seq)
		if !accepted {
			return nil // 幂等忽略：不写任何列
		}

		applied := cur.appliedRevision
		if isAck {
			incoming := int64(appliedRevision)
			switch {
			case incoming < cur.appliedRevision:
				return nil // applied 回退：忽略
			case cur.desiredRevision > 0 && incoming > cur.desiredRevision:
				return nil // 声称应用了未下发的 revision：忽略
			default:
				applied = incoming
			}
		}

		q := `UPDATE plugin_edge_revisions SET boot_id=?, prev_boot_id=?, last_sequence=?, last_report_at=?`
		args := []any{bootID, prevBootID, int64(seq), ts}
		if isAck {
			q += `, applied_revision=?, last_ack_at=?`
			args = append(args, applied, ts)
		}
		q += ` WHERE tenant_id=? AND edge_id=?`
		args = append(args, tid, edgeID)
		// SET 列表不含 tenant_id / desired_revision：上报方向永不改写归属与 Server 权威期望态。
		if _, err := conn.ExecContext(ctx, q, args...); err != nil {
			return fmt.Errorf("store: update plugin edge cursor: %w", err)
		}
		return nil
	})
}
