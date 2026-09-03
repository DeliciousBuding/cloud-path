package store

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestUsernameUniqueWithinTenant(t *testing.T) {
	s := openTest(t)
	defID, err := s.EnsureDefaultTenant()
	if err != nil {
		t.Fatal(err)
	}
	bID, err := s.CreateTenant("tenant-b", "tenant-b")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.CreateUser(defID, "shared", "", "operator", "h"); err != nil {
		t.Fatalf("首次创建 = %v", err)
	}
	// 同租户重复 → 冲突
	if _, err := s.CreateUser(defID, "shared", "", "operator", "h"); !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("同租户重复 = %v, want ErrUsernameTaken", err)
	}
	// 跨租户同 username → 允许
	if _, err := s.CreateUser(bID, "shared", "", "operator", "h"); err != nil {
		t.Fatalf("跨租户同 username = %v, want nil", err)
	}
}

func TestV5MigrationPreservesUsersSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-v3.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	for _, ddl := range []string{schemaV1, schemaV2, schemaV3} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatalf("exec legacy schema: %v", err)
		}
	}
	now := time.Now().Unix()
	seed := []string{
		`INSERT INTO tenant(id,slug,name,created_at) VALUES(1,'default','default',1)`,
		`INSERT INTO users(id,tenant_id,username,name,role,password_hash,created_at,disabled)
		 VALUES(1,1,'admin','管理员','admin','hash',100,0)`,
		`INSERT INTO users(id,tenant_id,username,name,role,password_hash,created_at,disabled)
		 VALUES(2,1,'op','','operator','hash',100,0)`,
		fmt.Sprintf(`INSERT INTO sessions(id,user_id,created_at,expires_at,last_seen_at)
		 VALUES('sid-1',1,%d,%d,%d)`, now, now+3600, now),
		`PRAGMA user_version = 3`,
	}
	for _, q := range seed {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("seed legacy db: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Version() != schemaVersion {
		t.Fatalf("version = %d, want %d", s.Version(), schemaVersion)
	}

	// 用户数据保留（含 id，会话外键仍指向原 id）。
	u, err := s.GetUserByID(1)
	if err != nil || u.Username != "admin" || u.TenantSlug != "default" || u.Role != "admin" {
		t.Fatalf("user 1 after migration = %+v err=%v", u, err)
	}
	if _, err := s.GetUserByID(2); err != nil {
		t.Fatalf("user 2 lost: %v", err)
	}

	// 会话引用完整：既有 session 仍可解析出用户。
	su, err := s.UserBySession("sid-1", now)
	if err != nil || su.Username != "admin" {
		t.Fatalf("session after migration = %+v err=%v", su, err)
	}

	// 外键完整性自检。
	rows, err := s.db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("foreign_key_check 发现悬空外键")
	}

	// 全局 UNIQUE 已移除：同 username 可在其他租户创建；同租户仍冲突。
	bID, err := s.CreateTenant("tenant-b", "tenant-b")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser(bID, "admin", "", "operator", "h"); err != nil {
		t.Fatalf("跨租户 username=admin 应允许: %v", err)
	}
	if _, err := s.CreateUser(1, "op", "", "operator", "h"); !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("同租户 username=op 应冲突: %v", err)
	}
}
