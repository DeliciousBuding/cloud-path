package store

import (
	"context"
	"database/sql"
	"errors"
)

// migrateV5 是 v5 表重建迁移：把 users 的全局 UNIQUE(username) 改为 UNIQUE(tenant_id, username)
// （docs/api.md §3.2 username 租户内唯一），并创建 tenant_tokens。
// SQLite 无法 ALTER 删除唯一约束，按官方 12 步法在专用连接上临时关外键后重建 users 表，
// 保留 id/FK/数据与 sessions 引用完整性；重建后跑 foreign_key_check 验证。
// PRAGMA user_version=5 与重建在同一写事务内原子提交；并按列/索引形状探测 users/users_new/tenant_tokens，
// 兼容“DDL 已应用但 user_version 未前进”与 users_new 残留的半迁移库，不盲目 DROP 正常表。
func migrateV5(ctx context.Context, conn *sql.Conn) error {
	// 外键开关是连接级且不能在事务内切换：先在专用连接上关闭，defer 保证所有错误路径恢复。
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		return err
	}
	defer func() {
		_, _ = conn.ExecContext(ctx, `PRAGMA foreign_keys=ON`)
	}()

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
	if cur >= 5 {
		return nil
	}

	usersExists, err := tableExists(ctx, conn, "users")
	if err != nil {
		return err
	}
	usersNewExists, err := tableExists(ctx, conn, "users_new")
	if err != nil {
		return err
	}

	scoped := false
	if usersExists {
		scoped, err = usersHasScopedUnique(ctx, conn)
		if err != nil {
			return err
		}
	}

	switch {
	case usersExists && scoped:
		// 已重建为目标形状：清掉残留 users_new，只补 tenant_tokens。
		if usersNewExists {
			if _, err := conn.ExecContext(ctx, `DROP TABLE users_new`); err != nil {
				return err
			}
		}
	case usersExists:
		// 旧形状：重建。重建前清掉残留 users_new，避免 CREATE TABLE 冲突。
		if usersNewExists {
			if _, err := conn.ExecContext(ctx, `DROP TABLE users_new`); err != nil {
				return err
			}
		}
		if _, err := conn.ExecContext(ctx, migrateV5UsersSQL); err != nil {
			return err
		}
	case usersNewExists:
		// users 已 DROP、users_new 已就绪：改名补齐。
		if _, err := conn.ExecContext(ctx, `ALTER TABLE users_new RENAME TO users`); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_users_tenant ON users(tenant_id)`); err != nil {
			return err
		}
	default:
		return errors.New("store: schema v5: users 表缺失")
	}

	if _, err := conn.ExecContext(ctx, schemaV5); err != nil {
		return err
	}
	if err := setUserVersion(ctx, conn, 5); err != nil {
		return err
	}
	if err := callMigrationHook(5, "before_commit"); err != nil {
		return err
	}
	if err := commit(ctx, conn); err != nil {
		return err
	}
	committed = true

	// 完整性自检：任何外键悬空都视为迁移失败。
	return foreignKeyCheck(ctx, conn)
}

// migrateV5UsersSQL 是 users 表重建 DDL（保留 id，重建 UNIQUE(tenant_id, username)）。
const migrateV5UsersSQL = `
CREATE TABLE users_new(
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  tenant_id INTEGER NOT NULL REFERENCES tenant(id),
  username TEXT NOT NULL,
  name TEXT NOT NULL DEFAULT '',
  role TEXT NOT NULL DEFAULT 'operator',
  password_hash TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  disabled INTEGER NOT NULL DEFAULT 0,
  UNIQUE(tenant_id, username)
);
INSERT INTO users_new(id,tenant_id,username,name,role,password_hash,created_at,disabled)
  SELECT id,tenant_id,username,name,role,password_hash,created_at,disabled FROM users;
DROP TABLE users;
ALTER TABLE users_new RENAME TO users;
CREATE INDEX IF NOT EXISTS idx_users_tenant ON users(tenant_id);
`
