package store

import (
	"context"
	"database/sql"
)

// schemaV11 为 devices 增加最后已知 Descriptor 快照。列默认空串，既有设备
// 迁移后保持“无 Descriptor”，不会伪造描述。
const schemaV11 = `ALTER TABLE devices ADD COLUMN descriptor_json TEXT NOT NULL DEFAULT ''`

// migrateV11 幂等补齐 descriptor_json。SQLite 不支持 ADD COLUMN IF NOT EXISTS，
// 因此先按列探测；DDL 与 PRAGMA user_version 在同一 BEGIN IMMEDIATE 事务内提交，
// 兼容“列已应用但 user_version 仍停在 10”的半迁移库。
func migrateV11(ctx context.Context, conn *sql.Conn) error {
	if err := beginImmediate(ctx, conn); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			rollback(ctx, conn)
		}
	}()

	cur, err := readUserVersion(ctx, conn)
	if err != nil {
		return err
	}
	if cur >= 11 {
		return nil
	}
	ok, err := hasColumn(ctx, conn, "devices", "descriptor_json")
	if err != nil {
		return err
	}
	if !ok {
		if _, err := conn.ExecContext(ctx, schemaV11); err != nil {
			return err
		}
	}
	if err := setUserVersion(ctx, conn, 11); err != nil {
		return err
	}
	if err := callMigrationHook(11, "before_commit"); err != nil {
		return err
	}
	if err := commit(ctx, conn); err != nil {
		return err
	}
	committed = true
	return nil
}
