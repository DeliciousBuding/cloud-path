package store

import (
	"fmt"
	"strings"
)

// MarkCommandsHandled 将指定的 failed/timeout 命令标记为已处理。
// 只影响未处理行，返回实际更新数；tenantID 非 nil 时严格限定租户。
func (s *Store) MarkCommandsHandled(tenantID *int64, ids []int64, handledAt int64) (int64, error) {
	if len(ids) == 0 {
		return 0, fmt.Errorf("store: mark commands handled: no ids")
	}
	placeholders := make([]string, len(ids))
	args := make([]any, 0, len(ids)+2)
	args = append(args, handledAt)
	for i, id := range ids {
		placeholders[i] = "?"
		args = append(args, id)
	}
	q := `UPDATE commands SET handled_at=? WHERE handled_at IS NULL AND status IN ('failed','timeout') AND id IN (` + strings.Join(placeholders, ",") + `)`
	if tenantID != nil {
		q += ` AND tenant_id=?`
		args = append(args, *tenantID)
	}
	res, err := s.exec(q, args...)
	if err != nil {
		return 0, fmt.Errorf("store: mark commands handled: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: mark commands handled rows: %w", err)
	}
	return n, nil
}

// MarkFailedCommandsHandled 将时间窗内全部未处理的 failed/timeout 命令标记为已处理。
func (s *Store) MarkFailedCommandsHandled(tenantID *int64, since, until, handledAt int64) (int64, error) {
	q := `UPDATE commands SET handled_at=? WHERE handled_at IS NULL AND status IN ('failed','timeout') AND COALESCE(acked_at, created_at) BETWEEN ? AND ?`
	args := []any{handledAt, since, until}
	if tenantID != nil {
		q += ` AND tenant_id=?`
		args = append(args, *tenantID)
	}
	res, err := s.exec(q, args...)
	if err != nil {
		return 0, fmt.Errorf("store: mark failed commands handled: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: mark failed commands handled rows: %w", err)
	}
	return n, nil
}
