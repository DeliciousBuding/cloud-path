package store

import (
	"context"
	"database/sql"
)

// migrateV8 建租户策略表 tenant_policies（docs/architecture/tenant-security-policy.md §3、§4、§5）：
// 保留期 + 配额单表，字段可 NULL（继承默认），每字段带 DB CHECK，主键即 tenant_id。
// 纯新增表，不改写既有表；与 v7 由同一 lane 顺序编号，避免两条 lane 同时声明 schema v7。
func migrateV8(ctx context.Context, conn *sql.Conn) error {
	return applyIdempotentDDL(ctx, conn, 8, schemaV8, []string{"tenant_policies"})
}
