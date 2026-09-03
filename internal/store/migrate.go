package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// migrationTestHook 是仅测试用的故障注入点（生产恒为 nil）。
var migrationTestHook func(version int, phase string) error

func callMigrationHook(version int, phase string) error {
	if migrationTestHook == nil {
		return nil
	}
	return migrationTestHook(version, phase)
}

// migrateStore 在专用连接上把 DB 迁移到 schemaVersion。
// 每次迁移在 BEGIN IMMEDIATE 写锁内原子完成 DDL + PRAGMA user_version，崩溃不留半迁移；
// 同一 DB 的并发 Open 会在此串行收敛（SQLite 写锁 + busy_timeout）。
func migrateStore(db *sql.DB) error {
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("store: acquire migration conn: %w", err)
	}
	defer conn.Close()

	for {
		cur, err := readUserVersion(ctx, conn)
		if err != nil {
			return fmt.Errorf("store: read user_version: %w", err)
		}
		if cur >= schemaVersion {
			break
		}
		m := migrationFor(cur)
		if m == nil {
			return fmt.Errorf("store: no migration for version %d", cur)
		}
		if err := m.apply(ctx, conn); err != nil {
			return fmt.Errorf("store: schema v%d: %w", m.version, err)
		}
	}
	if err := foreignKeyCheck(ctx, conn); err != nil {
		return fmt.Errorf("store: foreign_key_check: %w", err)
	}
	return nil
}

// migrationFor 返回第一个 version > 当前版本的迁移；无则 nil。
func migrationFor(version int) *migration {
	for i := range migrations {
		if migrations[i].version > version {
			return &migrations[i]
		}
	}
	return nil
}

func (m migration) apply(ctx context.Context, conn *sql.Conn) error {
	if m.custom != nil {
		return m.custom(ctx, conn)
	}
	return applyDDL(ctx, conn, m.version, m.ddl)
}

// applyDDL 在 BEGIN IMMEDIATE 写锁内执行普通 DDL 并把 user_version 一起原子提交。
// 锁内重读 user_version，避免并发推进后重复执行已应用迁移。
func applyDDL(ctx context.Context, conn *sql.Conn, version int, ddl string) error {
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
	if cur >= version {
		return nil // 并发已推进：空回滚释放写锁
	}
	if _, err := conn.ExecContext(ctx, ddl); err != nil {
		return err
	}
	if err := setUserVersion(ctx, conn, version); err != nil {
		return err
	}
	if err := callMigrationHook(version, "before_commit"); err != nil {
		return err
	}
	if err := commit(ctx, conn); err != nil {
		return err
	}
	committed = true
	return nil
}

func beginImmediate(ctx context.Context, conn *sql.Conn) error {
	_, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`)
	return err
}

func rollback(ctx context.Context, conn *sql.Conn) {
	_, _ = conn.ExecContext(ctx, `ROLLBACK`)
}

func commit(ctx context.Context, conn *sql.Conn) error {
	_, err := conn.ExecContext(ctx, `COMMIT`)
	return err
}

func readUserVersion(ctx context.Context, conn *sql.Conn) (int, error) {
	var v int
	if err := conn.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&v); err != nil {
		return 0, err
	}
	return v, nil
}

func setUserVersion(ctx context.Context, conn *sql.Conn, version int) error {
	_, err := conn.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, version))
	return err
}

func foreignKeyCheck(ctx context.Context, conn *sql.Conn) error {
	rows, err := conn.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		return errors.New("store: foreign_key_check failed")
	}
	return rows.Err()
}

func tableExists(ctx context.Context, conn *sql.Conn, name string) (bool, error) {
	var n string
	err := conn.QueryRowContext(ctx,
		`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func hasColumn(ctx context.Context, conn *sql.Conn, table, column string) (bool, error) {
	rows, err := conn.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

// uniqueColumns 返回表中唯一约束自索引（origin='u'）的列集：index -> 有序列名。
func uniqueColumns(ctx context.Context, conn *sql.Conn, table string) (map[string][]string, error) {
	rows, err := conn.QueryContext(ctx, `PRAGMA index_list(`+table+`)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var seq, unique, partial int
		var name, origin string
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			return nil, err
		}
		if unique == 0 || origin != "u" {
			continue
		}
		cols, err := indexColumns(ctx, conn, name)
		if err != nil {
			return nil, err
		}
		out[name] = cols
	}
	return out, rows.Err()
}

func indexColumns(ctx context.Context, conn *sql.Conn, index string) ([]string, error) {
	rows, err := conn.QueryContext(ctx, `PRAGMA index_info(`+index+`)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var seqno, cid int
		var name string
		if err := rows.Scan(&seqno, &cid, &name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// usersHasScopedUnique 判断 users 是否已是目标形状：存在 UNIQUE(tenant_id, username)。
func usersHasScopedUnique(ctx context.Context, conn *sql.Conn) (bool, error) {
	sets, err := uniqueColumns(ctx, conn, "users")
	if err != nil {
		return false, err
	}
	for _, cols := range sets {
		if len(cols) == 2 && cols[0] == "tenant_id" && cols[1] == "username" {
			return true, nil
		}
	}
	return false, nil
}
