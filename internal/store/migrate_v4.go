package store

import (
	"context"
	"database/sql"
)

// migrateV4 给 devices/device_state/events/commands 补 tenant_id 列并回填 default 租户。
// SQLite 无 ADD COLUMN IF NOT EXISTS：按列探测补齐，兼容“DDL 已应用但 user_version 未前进”的半迁移库。
func migrateV4(ctx context.Context, conn *sql.Conn) error {
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
	if cur >= 4 {
		return nil
	}

	for _, table := range []string{"devices", "device_state", "events", "commands"} {
		ok, err := hasColumn(ctx, conn, table, "tenant_id")
		if err != nil {
			return err
		}
		if ok {
			continue
		}
		if _, err := conn.ExecContext(ctx,
			`ALTER TABLE `+table+` ADD COLUMN tenant_id INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}

	// 幂等部分：default 租户兜底 + 既有行回填 + 隔离查询索引。
	if _, err := conn.ExecContext(ctx, schemaV4); err != nil {
		return err
	}
	if err := setUserVersion(ctx, conn, 4); err != nil {
		return err
	}
	if err := callMigrationHook(4, "before_commit"); err != nil {
		return err
	}
	if err := commit(ctx, conn); err != nil {
		return err
	}
	committed = true
	return nil
}
