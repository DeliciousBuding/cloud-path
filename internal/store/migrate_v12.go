package store

import (
	"context"
	"database/sql"
)

// schemaV12 为 commands 增加“已处理”时间。只用于人工确认失败/超时操作已查看或已处理，
// 不改变命令自身的执行状态；原始记录仍保留在运行记录中。
const schemaV12 = `ALTER TABLE commands ADD COLUMN handled_at INTEGER`

// migrateV12 幂等补齐 handled_at。SQLite 不支持 ADD COLUMN IF NOT EXISTS，
// 因此先按列探测；DDL 与 PRAGMA user_version 在同一 BEGIN IMMEDIATE 事务内提交。
func migrateV12(ctx context.Context, conn *sql.Conn) error {
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
	if cur >= 12 {
		return nil
	}
	ok, err := hasColumn(ctx, conn, "commands", "handled_at")
	if err != nil {
		return err
	}
	if !ok {
		if _, err := conn.ExecContext(ctx, schemaV12); err != nil {
			return err
		}
	}
	if err := setUserVersion(ctx, conn, 12); err != nil {
		return err
	}
	if err := callMigrationHook(12, "before_commit"); err != nil {
		return err
	}
	if err := commit(ctx, conn); err != nil {
		return err
	}
	committed = true
	return nil
}
