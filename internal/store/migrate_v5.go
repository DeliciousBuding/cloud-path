package store

import (
	"context"
	"database/sql"
	"errors"
)

// migrateV5 是 v5 表重建迁移：把 users 的全局 UNIQUE(username) 改为
// UNIQUE(tenant_id, username)（docs/api.md §3.2 username 租户内唯一），并创建 tenant_tokens。
// SQLite 无法 ALTER 删除唯一约束，按官方 12 步法在专用连接上临时关外键后重建 users 表，
// 保留 id/FK/数据与 sessions 引用完整性；重建后跑 foreign_key_check 验证。
func migrateV5(db *sql.DB) error {
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	// 外键开关是连接级且不能在事务内切换：先在专用连接上关闭。
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		return err
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	ddl := migrateV5UsersSQL + schemaV5
	if _, err := tx.ExecContext(ctx, ddl); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=ON`); err != nil {
		return err
	}

	// 完整性自检：任何外键悬空都视为迁移失败。
	rows, err := conn.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		return errors.New("store: schema v5: foreign_key_check failed")
	}
	return rows.Err()
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
