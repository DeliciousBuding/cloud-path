package store

import (
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestAuthTables(t *testing.T) {
	s := openTest(t)

	// 首装自动创建 default 租户且幂等
	id1, err := s.EnsureDefaultTenant()
	if err != nil {
		t.Fatal(err)
	}
	id2, err := s.EnsureDefaultTenant()
	if err != nil {
		t.Fatal(err)
	}
	if id1 <= 0 || id1 != id2 {
		t.Fatalf("default tenant id = %d, %d", id1, id2)
	}
	tv, err := s.GetTenantBySlug("default")
	if err != nil || tv.ID != id1 || tv.Slug != "default" {
		t.Fatalf("tenant = %+v err=%v", tv, err)
	}

	// 原子首装：第一次成功，第二次拒绝
	u, created, err := s.CreateInitialAdmin("admin", "管理员", "hash")
	if err != nil || !created {
		t.Fatalf("first setup: created=%v err=%v", created, err)
	}
	if u.Role != "admin" || u.TenantSlug != "default" || u.Username != "admin" {
		t.Fatalf("bad admin: %+v", u)
	}
	if _, created, err = s.CreateInitialAdmin("admin2", "x", "hash2"); err != nil || created {
		t.Fatalf("second setup should fail: created=%v err=%v", created, err)
	}
	if n, _ := s.CountUsers(); n != 1 {
		t.Fatalf("users = %d, want 1", n)
	}

	byName, err := s.GetUserByUsername("admin")
	if err != nil || byName.ID != u.ID || byName.TenantSlug != "default" {
		t.Fatalf("GetUserByUsername = %+v err=%v", byName, err)
	}
	if _, err := s.GetUserByUsername("nope"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing user err = %v", err)
	}

	// 会话：创建/查询/过期/触达/删除
	nowTs := time.Now().Unix()
	if err := s.CreateSession("sid-1", u.ID, nowTs+3600); err != nil {
		t.Fatal(err)
	}
	got, err := s.UserBySession("sid-1", nowTs)
	if err != nil || got.Username != "admin" || got.SessionLastSeen == 0 {
		t.Fatalf("session user = %+v err=%v", got, err)
	}
	if _, err := s.UserBySession("sid-1", nowTs+7200); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("过期会话应 ErrNoRows: %v", err)
	}
	if _, err := s.UserBySession("nope", nowTs); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("不存在会话应 ErrNoRows: %v", err)
	}
	if err := s.TouchSession("sid-1", nowTs+10); err != nil {
		t.Fatal(err)
	}
	got2, err := s.UserBySession("sid-1", nowTs)
	if err != nil || got2.SessionLastSeen != nowTs+10 {
		t.Fatalf("touch 未生效: %+v err=%v", got2, err)
	}
	if err := s.DeleteSession("sid-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UserBySession("sid-1", nowTs); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("删除后应 ErrNoRows: %v", err)
	}

	// 禁用用户 = 会话立即失效
	if _, err := s.db.Exec(`
		INSERT INTO users(tenant_id,username,name,role,password_hash,created_at,disabled)
		VALUES(?,?,?,?,?,?,1)`, id1, "blocked", "", "operator", "hash", nowTs); err != nil {
		t.Fatal(err)
	}
	var blockedID int64
	if err := s.db.QueryRow(`SELECT id FROM users WHERE username='blocked'`).Scan(&blockedID); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSession("sid-blocked", blockedID, nowTs+3600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UserBySession("sid-blocked", nowTs); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("禁用用户会话应 ErrNoRows: %v", err)
	}

	// 过期会话清理
	if err := s.CreateSession("sid-old", u.ID, nowTs-10); err != nil {
		t.Fatal(err)
	}
	n, err := s.PruneSessions(nowTs)
	if err != nil || n != 1 { // 只有已过期的 sid-old；sid-blocked 尚未过期，不清
		t.Fatalf("prune = %d err=%v, want 1", n, err)
	}
}
