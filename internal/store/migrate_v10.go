package store

import (
	"context"
	"database/sql"
)

// schemaV10 建声明式定时任务表 scheduled_jobs（schedule_job 效果的持久层，
// Durable Scheduler 的 SSOT）。纯新增表，不改写任何既有表，v9 数据零风险。
// 主键首列恒为 tenant_id，与插件控制面各表同一安全模型。
const schemaV10 = `
CREATE TABLE IF NOT EXISTS scheduled_jobs(
  tenant_id     INTEGER NOT NULL REFERENCES tenant(id),
  instance_id   TEXT    NOT NULL,
  schedule_id   TEXT    NOT NULL,
  cron          TEXT    NOT NULL,
  timezone      TEXT    NOT NULL DEFAULT 'UTC',
  payload_json  TEXT    NOT NULL DEFAULT '{}',
  missed_policy TEXT    NOT NULL DEFAULT 'skip',
  next_run_at   INTEGER NOT NULL,
  last_run_at   INTEGER NOT NULL DEFAULT 0,
  last_dispatch TEXT    NOT NULL DEFAULT '',
  state         TEXT    NOT NULL DEFAULT 'active',
  revision      INTEGER NOT NULL DEFAULT 1,
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL,
  PRIMARY KEY(tenant_id, instance_id, schedule_id)
);
CREATE INDEX IF NOT EXISTS idx_scheduled_jobs_due
  ON scheduled_jobs(state, next_run_at);
`

// migrateV10 建声明式定时任务表。幂等 DDL：半迁移库重跑自愈。
func migrateV10(ctx context.Context, conn *sql.Conn) error {
	return applyIdempotentDDL(ctx, conn, 10, schemaV10, []string{"scheduled_jobs"})
}
