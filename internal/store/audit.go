package store

import "fmt"

// AuditEvent 是 audit_events 表一行。append-only：本包只暴露 INSERT 与 tenant-scoped 查询，
// 不暴露任何 update/delete API（安全红线由代码结构锁定）。
type AuditEvent struct {
	ID           int64
	TenantID     int64
	ActorType    string
	ActorID      int64
	ActorName    string
	Action       string
	TargetType   string
	TargetID     string
	Outcome      string
	RequestID    string
	RemoteIP     string
	MetadataJSON string
	CreatedAt    int64
}

// InsertAuditEvent 追加一条审计事件。写失败由调用方消化（audit 失败不得改变业务结果）。
func (s *Store) InsertAuditEvent(ev AuditEvent) error {
	res, err := s.exec(`
		INSERT INTO audit_events(tenant_id, actor_type, actor_id, actor_name, action,
			target_type, target_id, outcome, request_id, remote_ip, metadata_json, created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		ev.TenantID, ev.ActorType, ev.ActorID, ev.ActorName, ev.Action,
		ev.TargetType, ev.TargetID, ev.Outcome, ev.RequestID, ev.RemoteIP,
		ev.MetadataJSON, now())
	if err != nil {
		return fmt.Errorf("store: insert audit event: %w", err)
	}
	if _, err := res.LastInsertId(); err != nil {
		return fmt.Errorf("store: insert audit event id: %w", err)
	}
	return nil
}

// ListAuditEvents 查询本租户审计事件：since>0 只取 created_at>=since；action 非空精确过滤；
// limit 经 clampLimit 归一（<=0 或 >1000 → 100）。恒按 tenant_id 过滤，跨租户不可见。
func (s *Store) ListAuditEvents(tenantID int64, since int64, action string, limit int) ([]AuditEvent, error) {
	limit = clampLimit(limit)
	q := `SELECT id, tenant_id, actor_type, actor_id, actor_name, action,
		target_type, target_id, outcome, request_id, remote_ip, metadata_json, created_at
		FROM audit_events WHERE tenant_id=?`
	args := []any{tenantID}
	if since > 0 {
		q += ` AND created_at >= ?`
		args = append(args, since)
	}
	if action != "" {
		q += ` AND action = ?`
		args = append(args, action)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list audit events: %w", err)
	}
	defer rows.Close()
	var out []AuditEvent
	for rows.Next() {
		var ev AuditEvent
		if err := rows.Scan(&ev.ID, &ev.TenantID, &ev.ActorType, &ev.ActorID, &ev.ActorName,
			&ev.Action, &ev.TargetType, &ev.TargetID, &ev.Outcome, &ev.RequestID,
			&ev.RemoteIP, &ev.MetadataJSON, &ev.CreatedAt); err != nil {
			return nil, fmt.Errorf("store: scan audit event: %w", err)
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}
