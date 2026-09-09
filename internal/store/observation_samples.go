package store

import (
	"context"
	"strings"
)

// ObservationSampleInput 是一条待写入的数值采样。Key 是通用序列键：
// raw 字段使用原始字段名，typed observation 使用 "<entity_id>.<property>"。
type ObservationSampleInput struct {
	Key     string
	TS      int64
	Value   float64
	Quality string
}

// ObservationSampleRow 是一条已持久化的数值采样。
type ObservationSampleRow struct {
	DeviceID string
	Key      string
	TS       int64
	Value    float64
	Quality  string
}

// AddObservationSamplesTenant 在同一事务内写入一批采样；设备必须属于指定租户。
// 同一 (device, key, ts) 重复上报时覆盖为该秒最后一次值，避免高频 state 把库撑爆。
func (s *Store) AddObservationSamplesTenant(tenantID int64, deviceID string, samples []ObservationSampleInput) error {
	if len(samples) == 0 {
		return nil
	}
	tid, err := s.normalizeTenantID(tenantID)
	if err != nil {
		return err
	}
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var exists int
	if err := tx.QueryRowContext(ctx,
		`SELECT 1 FROM devices WHERE id=? AND tenant_id=?`, deviceID, tid).Scan(&exists); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO observation_samples(tenant_id, device_id, series_key, ts, value, quality)
		VALUES(?,?,?,?,?,?)
		ON CONFLICT(tenant_id, device_id, series_key, ts) DO UPDATE SET
			value=excluded.value,
			quality=excluded.quality`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, sample := range samples {
		key := strings.TrimSpace(sample.Key)
		if key == "" {
			continue
		}
		ts := sample.TS
		if ts <= 0 {
			ts = now()
		}
		quality := strings.TrimSpace(sample.Quality)
		if quality == "" {
			quality = "good"
		}
		if _, err := stmt.ExecContext(ctx, tid, deviceID, key, ts, sample.Value, quality); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

// ListObservationSamples 返回指定序列的一段历史，按时间升序。
// before>0 是向更早翻页的排他游标；返回的 nextBefore 非 0 时表示还有更早数据。
func (s *Store) ListObservationSamples(
	tenantID int64, deviceID, key string, from, to, before int64, limit int,
) ([]ObservationSampleRow, int64, error) {
	limit = clampObservationSampleLimit(limit)
	q := `SELECT device_id, series_key, ts, value, quality
		FROM observation_samples
		WHERE device_id=? AND series_key=?`
	args := []any{deviceID, key}
	if tenantID > 0 {
		q += ` AND tenant_id=?`
		args = append(args, tenantID)
	}
	if from > 0 {
		q += ` AND ts>=?`
		args = append(args, from)
	}
	if to > 0 {
		q += ` AND ts<=?`
		args = append(args, to)
	}
	if before > 0 {
		q += ` AND ts<?`
		args = append(args, before)
	}
	q += ` ORDER BY ts DESC LIMIT ?`
	args = append(args, limit+1)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]ObservationSampleRow, 0, limit+1)
	for rows.Next() {
		var r ObservationSampleRow
		if err := rows.Scan(&r.DeviceID, &r.Key, &r.TS, &r.Value, &r.Quality); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	var nextBefore int64
	if len(out) > limit {
		nextBefore = out[limit-1].TS
		out = out[:limit]
	}
	// 查询按新→旧取页，返回给图表/表格统一按旧→新排列。
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nextBefore, nil
}

// PruneObservationSamples 删除 ts < before 的数值采样，返回删除行数。
func (s *Store) PruneObservationSamples(before int64) (int64, error) {
	res, err := s.exec(`DELETE FROM observation_samples WHERE ts < ?`, before)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func clampObservationSampleLimit(limit int) int {
	if limit <= 0 {
		return 1000
	}
	if limit > 5000 {
		return 5000
	}
	return limit
}
