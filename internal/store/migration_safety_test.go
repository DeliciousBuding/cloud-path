package store

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// v4AlterSQL 是 v4 迁移中非幂等的 ADD COLUMN 部分（测试构造半迁移库用）。
const v4AlterSQL = `
ALTER TABLE devices ADD COLUMN tenant_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE device_state ADD COLUMN tenant_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE events ADD COLUMN tenant_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE commands ADD COLUMN tenant_id INTEGER NOT NULL DEFAULT 0;
`

func rawSQLite(t *testing.T, path string, foreignKeys bool) *sql.DB {
	t.Helper()
	dsn := "file:" + filepath.ToSlash(path)
	if foreignKeys {
		dsn += "?_pragma=foreign_keys(1)"
	} else {
		dsn += "?_pragma=foreign_keys(0)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func execAll(t *testing.T, db *sql.DB, stmts ...string) {
	t.Helper()
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}
}

func userVersion(t *testing.T, db *sql.DB) int {
	t.Helper()
	var v int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func tableMissing(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var n string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n)
	if err == sql.ErrNoRows {
		return true
	}
	if err != nil {
		t.Fatal(err)
	}
	return false
}

// TestMigrationVersionAtomicWithDDL 锁定：普通迁移的 DDL 与 user_version 在同一事务内，
// 注入提交前故障后，DDL 与版本必须一起回滚；清故障重开可到 v6。
func TestMigrationVersionAtomicWithDDL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "atomic.db")
	migrationTestHook = func(v int, phase string) error {
		if v == 2 && phase == "before_commit" {
			return errors.New("injected v2 commit failure")
		}
		return nil
	}
	defer func() { migrationTestHook = nil }()

	if _, err := Open(path); err == nil {
		t.Fatal("Open 应失败（v2 注入）")
	}

	db := rawSQLite(t, path, true)
	defer db.Close()
	if v := userVersion(t, db); v != 1 {
		t.Fatalf("user_version=%d, want 1（v2 DDL 与版本必须一起回滚）", v)
	}
	for _, idx := range []string{"idx_events_ts", "idx_commands_device"} {
		if !tableMissing(t, db, idx) {
			t.Fatalf("v2 索引 %s 不应存在（回滚失败）", idx)
		}
	}
	db.Close()

	migrationTestHook = nil
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Version() != schemaVersion {
		t.Fatalf("version=%d, want %d", s.Version(), schemaVersion)
	}
}

// TestRecoverV4DDLAppliedVersionStale 是反向验证：人工构造 user_version=3 但 v4 列已存在
// （半迁移），下次 Open 必须成功到 v6 且数据不丢、索引补齐、tenant_id 回填。
func TestRecoverV4DDLAppliedVersionStale(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v4-stale.db")
	db := rawSQLite(t, path, true)
	execAll(t, db, schemaV1, schemaV2, schemaV3, v4AlterSQL)
	execAll(t, db,
		`INSERT INTO tenant(id,slug,name,created_at) VALUES(1,'default','default',1)`,
		`INSERT INTO devices(id,edge_id,adapter,name,port,first_seen,last_seen,tenant_id) VALUES('e1/d1','e1','stcb','d1','COM3',100,100,0)`,
		`INSERT INTO device_state(device_id,tenant_id,state,online,updated_at) VALUES('e1/d1',0,'{"clock":"08:00"}',1,100)`,
		`INSERT INTO events(device_id,tenant_id,ts,type,payload) VALUES('e1/d1',0,100,'BOOT','{}')`,
		`INSERT INTO commands(device_id,tenant_id,cmd,args,status,created_at) VALUES('e1/d1',0,'sync','','pending',100)`,
		`PRAGMA user_version = 3`,
	)
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Version() != schemaVersion {
		t.Fatalf("version=%d, want %d", s.Version(), schemaVersion)
	}
	for _, q := range []string{
		`SELECT tenant_id FROM devices WHERE id='e1/d1'`,
		`SELECT tenant_id FROM device_state WHERE device_id='e1/d1'`,
		`SELECT tenant_id FROM events WHERE device_id='e1/d1'`,
		`SELECT tenant_id FROM commands WHERE device_id='e1/d1'`,
	} {
		var got int64
		if err := s.db.QueryRow(q).Scan(&got); err != nil || got != 1 {
			t.Fatalf("%s = %d err=%v, want 1", q, got, err)
		}
	}
	devs, err := s.ListDevicesTenant(1)
	if err != nil || len(devs) != 1 || devs[0].ID != "e1/d1" {
		t.Fatalf("devices after recover = %+v err=%v", devs, err)
	}
	if err := foreignKeyCheckDB(s.db); err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
}

// TestRecoverV5PartialUsersRebuild 锁定 v5 半迁移恢复：users 已 DROP、users_new 已就绪时，
// Open 改名补齐 + 建 tenant_tokens，并保留用户/会话 FK 与数据。
func TestRecoverV5PartialUsersRebuild(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v5-partial.db")
	db := rawSQLite(t, path, false)
	execAll(t, db, schemaV1, schemaV2, schemaV3, v4AlterSQL)
	execAll(t, db,
		`INSERT INTO tenant(id,slug,name,created_at) VALUES(1,'default','default',1)`,
	)
	execAll(t, db, schemaV4)
	execAll(t, db,
		`CREATE TABLE users_new(
		  id INTEGER PRIMARY KEY AUTOINCREMENT,
		  tenant_id INTEGER NOT NULL REFERENCES tenant(id),
		  username TEXT NOT NULL,
		  name TEXT NOT NULL DEFAULT '',
		  role TEXT NOT NULL DEFAULT 'operator',
		  password_hash TEXT NOT NULL,
		  created_at INTEGER NOT NULL,
		  disabled INTEGER NOT NULL DEFAULT 0,
		  UNIQUE(tenant_id, username)
		)`,
		`INSERT INTO users_new(id,tenant_id,username,name,role,password_hash,created_at,disabled)
		 VALUES(1,1,'admin','管理员','admin','hash',100,0)`,
		fmt.Sprintf(`INSERT INTO sessions(id,user_id,created_at,expires_at,last_seen_at)
		 VALUES('sid-1',1,100,%d,100)`, time.Now().Unix()+3600),
		`DROP TABLE users`,
		`PRAGMA user_version = 4`,
	)
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Version() != schemaVersion {
		t.Fatalf("version=%d, want %d", s.Version(), schemaVersion)
	}
	u, err := s.GetUserByID(1)
	if err != nil || u.Username != "admin" || u.TenantSlug != "default" {
		t.Fatalf("user after recover = %+v err=%v", u, err)
	}
	su, err := s.UserBySession("sid-1", time.Now().Unix())
	if err != nil || su.Username != "admin" {
		t.Fatalf("session after recover = %+v err=%v", su, err)
	}
	if tableMissing(t, s.db, "tenant_tokens") {
		t.Fatal("tenant_tokens 未补齐")
	}
	if err := foreignKeyCheckDB(s.db); err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
}

// TestMigrationPreservesAllForeignKeys 锁定 v3→v6 全程：tenant/users/sessions/devices/state/events/commands
// 数据与 FK 全部保留，迁移后无悬空外键。
func TestMigrationPreservesAllForeignKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fk.db")
	db := rawSQLite(t, path, true)
	execAll(t, db, schemaV1, schemaV2, schemaV3)
	execAll(t, db,
		`INSERT INTO tenant(id,slug,name,created_at) VALUES(1,'default','default',1)`,
		`INSERT INTO users(id,tenant_id,username,name,role,password_hash,created_at,disabled)
		 VALUES(1,1,'admin','管理员','admin','hash',100,0)`,
		`INSERT INTO users(id,tenant_id,username,name,role,password_hash,created_at,disabled)
		 VALUES(2,1,'op','','operator','hash',100,0)`,
		fmt.Sprintf(`INSERT INTO sessions(id,user_id,created_at,expires_at,last_seen_at)
		 VALUES('sid-1',1,100,%d,100)`, time.Now().Unix()+3600),
		`INSERT INTO devices(id,edge_id,adapter,name,port,first_seen,last_seen)
		 VALUES('e1/d1','e1','stcb','d1','COM3',100,100)`,
		`INSERT INTO device_state(device_id,state,online,updated_at)
		 VALUES('e1/d1','{"clock":"08:00"}',1,100)`,
		`INSERT INTO events(device_id,ts,type,payload) VALUES('e1/d1',100,'BOOT','{}')`,
		`INSERT INTO commands(device_id,cmd,args,status,created_at) VALUES('e1/d1','sync','','pending',100)`,
		`PRAGMA user_version = 3`,
	)
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Version() != schemaVersion {
		t.Fatalf("version=%d, want %d", s.Version(), schemaVersion)
	}
	if err := foreignKeyCheckDB(s.db); err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
	u, err := s.GetUserByID(1)
	if err != nil || u.Username != "admin" || u.Role != "admin" {
		t.Fatalf("user 1 = %+v err=%v", u, err)
	}
	if _, err := s.GetUserByID(2); err != nil {
		t.Fatalf("user 2 lost: %v", err)
	}
	if su, err := s.UserBySession("sid-1", time.Now().Unix()); err != nil || su.Username != "admin" {
		t.Fatalf("session = %+v err=%v", su, err)
	}
	devs, err := s.ListDevicesTenant(1)
	if err != nil || len(devs) != 1 || devs[0].ID != "e1/d1" {
		t.Fatalf("devices = %+v err=%v", devs, err)
	}
	if tableMissing(t, s.db, "tenant_tokens") || tableMissing(t, s.db, "audit_events") {
		t.Fatal("tenant_tokens/audit_events 未创建")
	}
}

// TestConcurrentOpenMigration 锁定同一 DB 并发 Open/迁移在 SQLite 写锁下正确收敛。
func TestConcurrentOpenMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conc.db")
	pre := rawSQLite(t, path, true)
	execAll(t, pre, `PRAGMA journal_mode=WAL`, `PRAGMA user_version=0`)
	pre.Close()

	const n = 8
	start := make(chan struct{})
	errCh := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			s, err := Open(path)
			if err != nil {
				errCh <- err
				return
			}
			s.Close()
		}()
	}
	close(start)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent open: %v", err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Version() != schemaVersion {
		t.Fatalf("version=%d, want %d", s.Version(), schemaVersion)
	}
	if !tableMissing(t, s.db, "users_new") {
		t.Fatal("users_new 残留")
	}
	if err := foreignKeyCheckDB(s.db); err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
	if err := s.UpsertDevice("e1/d1", "e1", "stcb", "d1", "COM3"); err != nil {
		t.Fatalf("upsert after concurrent open: %v", err)
	}
}

// TestMigrationFailureLeavesRecoverableDB 锁定：v5 重建提交前故障必须整段回滚，
// 不留 users_new/tenant_tokens，且下次 Open 可恢复。
func TestMigrationFailureLeavesRecoverableDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fail.db")
	migrationTestHook = func(v int, phase string) error {
		if v == 5 && phase == "before_commit" {
			return errors.New("injected v5 failure")
		}
		return nil
	}
	defer func() { migrationTestHook = nil }()

	if _, err := Open(path); err == nil {
		t.Fatal("Open 应失败（v5 注入）")
	}

	db := rawSQLite(t, path, true)
	if v := userVersion(t, db); v != 4 {
		t.Fatalf("user_version=%d, want 4（v5 已回滚）", v)
	}
	if !tableMissing(t, db, "users_new") {
		t.Fatal("失败后 users_new 不应残留")
	}
	if !tableMissing(t, db, "tenant_tokens") {
		t.Fatal("失败后 tenant_tokens 不应残留")
	}
	db.Close()

	migrationTestHook = nil
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Version() != schemaVersion {
		t.Fatalf("version=%d, want %d", s.Version(), schemaVersion)
	}
	if !tableMissing(t, s.db, "users_new") {
		t.Fatal("恢复后 users_new 应已被消费")
	}
	if err := foreignKeyCheckDB(s.db); err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
}

// TestRecoverV5RebuiltVersionStale 锁定 v5 已重建但 user_version 未前进的恢复：
// users 已是 UNIQUE(tenant_id,username) 且 tenant_tokens 已存在时，Open 只补版本不重建。
func TestRecoverV5RebuiltVersionStale(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v5-rebuilt-stale.db")
	db := rawSQLite(t, path, false)
	execAll(t, db, schemaV1, schemaV2, schemaV3, v4AlterSQL)
	execAll(t, db, `INSERT INTO tenant(id,slug,name,created_at) VALUES(1,'default','default',1)`)
	execAll(t, db, schemaV4)
	execAll(t, db,
		`DROP TABLE users`,
		`CREATE TABLE users(
		  id INTEGER PRIMARY KEY AUTOINCREMENT,
		  tenant_id INTEGER NOT NULL REFERENCES tenant(id),
		  username TEXT NOT NULL,
		  name TEXT NOT NULL DEFAULT '',
		  role TEXT NOT NULL DEFAULT 'operator',
		  password_hash TEXT NOT NULL,
		  created_at INTEGER NOT NULL,
		  disabled INTEGER NOT NULL DEFAULT 0,
		  UNIQUE(tenant_id, username)
		)`,
		`INSERT INTO users(id,tenant_id,username,name,role,password_hash,created_at,disabled)
		 VALUES(1,1,'admin','管理员','admin','hash',100,0)`,
		fmt.Sprintf(`INSERT INTO sessions(id,user_id,created_at,expires_at,last_seen_at)
		 VALUES('sid-1',1,100,%d,100)`, time.Now().Unix()+3600),
	)
	execAll(t, db, schemaV5)
	execAll(t, db, `PRAGMA user_version = 4`)
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Version() != schemaVersion {
		t.Fatalf("version=%d, want %d", s.Version(), schemaVersion)
	}
	u, err := s.GetUserByID(1)
	if err != nil || u.Username != "admin" {
		t.Fatalf("user after recover = %+v err=%v", u, err)
	}
	if su, err := s.UserBySession("sid-1", time.Now().Unix()); err != nil || su.Username != "admin" {
		t.Fatalf("session after recover = %+v err=%v", su, err)
	}
	if err := foreignKeyCheckDB(s.db); err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
}

// foreignKeyCheckDB 对 *sql.DB 跑外键完整性检查（迁移后验收用）。
func foreignKeyCheckDB(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		return errors.New("foreign_key_check failed")
	}
	return rows.Err()
}
