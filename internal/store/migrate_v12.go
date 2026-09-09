package store

import (
	"context"
	"database/sql"
)

// schemaV12 建通用数值采样历史表 observation_samples。
//
// 边界：本表只保存“设备上报过的数值事实”，不把 raw 字段名解释成设备语义，
// 也不替代当前状态表 device_state。主键 (tenant_id, device_id, series_key, ts)
// 把每台设备、每条序列的历史采样限制到每秒一条；同一秒重复上报时以最后一次为准。
const schemaV12 = `
CREATE TABLE IF NOT EXISTS observation_samples(
  tenant_id INTEGER NOT NULL REFERENCES tenant(id),
  device_id TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
  series_key TEXT NOT NULL,
  ts INTEGER NOT NULL,
  value REAL NOT NULL,
  quality TEXT NOT NULL DEFAULT 'good',
  PRIMARY KEY(tenant_id, device_id, series_key, ts)
);
CREATE INDEX IF NOT EXISTS idx_observation_samples_device_key_ts
  ON observation_samples(tenant_id, device_id, series_key, ts);
CREATE INDEX IF NOT EXISTS idx_observation_samples_tenant_ts
  ON observation_samples(tenant_id, ts);
`

// migrateV12 幂等新增数值采样历史表。纯新增表，不改写既有设备/事件/命令数据。
func migrateV12(ctx context.Context, conn *sql.Conn) error {
	return applyIdempotentDDL(ctx, conn, 12, schemaV12, []string{"observation_samples"})
}
