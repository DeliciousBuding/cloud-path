package store

import "fmt"

// FailedCommandsWindow 返回闭区间 [since, until] 内 failed/timeout 命令及完整计数。
// 失败时间取 acked_at（包括服务端写入的 timeout 时刻），缺失则回退 created_at。
// tenantID 非 nil 时始终限定该租户（包括无效的 0）；只有 nil 表示明确的无鉴权全局视图。
// 列表按失败时间、ID 降序且有界；窗口计数先于 LIMIT，二者来自同一 SQL 读快照。
func (s *Store) FailedCommandsWindow(tenantID *int64, since, until int64, limit int) ([]CommandRow, int, error) {
	limit = clampLimit(limit)
	q := `SELECT id, device_id, cmd, args, status, created_at, acked_at, result, COUNT(*) OVER ()
		FROM commands
		WHERE status IN ('failed', 'timeout') AND COALESCE(acked_at, created_at) BETWEEN ? AND ?`
	args := []any{since, until}
	if tenantID != nil {
		q += ` AND tenant_id = ?`
		args = append(args, *tenantID)
	}
	q += ` ORDER BY COALESCE(acked_at, created_at) DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("store: query failed commands window: %w", err)
	}
	defer rows.Close()
	out := make([]CommandRow, 0, limit)
	var total int
	for rows.Next() {
		var c CommandRow
		if err := rows.Scan(&c.ID, &c.DeviceID, &c.Cmd, &c.Args, &c.Status, &c.CreatedAt, &c.AckedAt, &c.Result, &total); err != nil {
			return nil, 0, fmt.Errorf("store: scan failed commands window: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("store: read failed commands window: %w", err)
	}
	return out, total, nil
}
