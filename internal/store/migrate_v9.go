package store

import (
	"context"
	"database/sql"
)

// schemaV9 建应用领域记录表 app_domain_records（Application Plugin 的
// create_domain_record 效果落点）。纯新增表，不改写任何既有表，v8 数据零风险；
// 主键首列恒为 tenant_id，与插件控制面四表同一安全模型。
const schemaV9 = `
CREATE TABLE IF NOT EXISTS app_domain_records(
  tenant_id   INTEGER NOT NULL REFERENCES tenant(id),
  instance_id TEXT    NOT NULL,
  record_type TEXT    NOT NULL,
  record_id   TEXT    NOT NULL,
  data_json   TEXT    NOT NULL,
  version     TEXT    NOT NULL DEFAULT '',
  updated_at  INTEGER NOT NULL,
  PRIMARY KEY(tenant_id, instance_id, record_type, record_id)
);
CREATE INDEX IF NOT EXISTS idx_app_domain_records_tenant
  ON app_domain_records(tenant_id, instance_id, updated_at);
`

// migrateV9 建应用领域记录表（appruntime create_domain_record 效果的持久层）。
// 幂等 DDL：半迁移库重跑自愈，不重复建表、不丢数据。
func migrateV9(ctx context.Context, conn *sql.Conn) error {
	return applyIdempotentDDL(ctx, conn, 9, schemaV9, []string{"app_domain_records"})
}
