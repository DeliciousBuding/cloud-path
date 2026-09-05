package store

import (
	"database/sql"
	"errors"
	"testing"
)

// TestMigrationV9FailureAtomic 锁定 v9 失败只回滚 v9：停在 v8，v7/v8 表保留，
// app_domain_records 不留残骸，重开补齐到 v9。
func TestMigrationV9FailureAtomic(t *testing.T) {
	path := writeLegacyV6DB(t)
	migrationTestHook = func(v int, phase string) error {
		if v == 9 && phase == "before_commit" {
			return errors.New("injected v9 commit failure")
		}
		return nil
	}
	if _, err := Open(path); err == nil {
		t.Fatal("Open 应失败（v9 注入）")
	}
	migrationTestHook = nil

	db := rawSQLite(t, path, true)
	if v := userVersion(t, db); v != 8 {
		t.Fatalf("user_version = %d, want 8（v8 已提交，v9 已回滚）", v)
	}
	if !tableMissing(t, db, "app_domain_records") {
		t.Fatal("v9 回滚后 app_domain_records 不应残留")
	}
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("恢复失败: %v", err)
	}
	defer s.Close()
	if s.Version() != schemaVersion || tableMissing(t, s.db, "app_domain_records") {
		t.Fatalf("恢复不完整: version=%d", s.Version())
	}
	// 恢复后领域记录可立即读写
	if err := s.UpsertAppDomainRecord(1, "box1", "window", "w1", `{"state":"opened"}`, "1", 100); err != nil {
		t.Fatalf("恢复后写入失败: %v", err)
	}
	rows, err := s.ListAppDomainRecords(1, "box1", 10)
	if err != nil || len(rows) != 1 || rows[0].RecordID != "w1" {
		t.Fatalf("领域记录读回失败: %+v err=%v", rows, err)
	}
}

// TestRecoverV9DDLAppliedVersionStale 锁定 v9 半迁移自愈：表已建且有数据但
// user_version 停在 8，Open 只补版本，记录数据不被清空。
func TestRecoverV9DDLAppliedVersionStale(t *testing.T) {
	path := writeLegacyV6DB(t)
	db := rawSQLite(t, path, true)
	execAll(t, db, schemaV7, schemaV8, schemaV9)
	execAll(t, db,
		`INSERT INTO app_domain_records(tenant_id,instance_id,record_type,record_id,data_json,version,updated_at)
		 VALUES(1,'box1','window','w-keep','{"state":"completed"}','1',100)`,
		`PRAGMA user_version = 8`,
	)
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("半迁移恢复失败: %v", err)
	}
	defer s.Close()
	if s.Version() != schemaVersion {
		t.Fatalf("version = %d, want %d", s.Version(), schemaVersion)
	}
	rows, err := s.ListAppDomainRecords(1, "box1", 10)
	if err != nil || len(rows) != 1 || rows[0].RecordID != "w-keep" {
		t.Fatalf("半迁移前的领域记录丢失: %+v err=%v", rows, err)
	}
	// 同键 upsert 覆盖而非追加
	if err := s.UpsertAppDomainRecord(1, "box1", "window", "w-keep", `{"state":"missed"}`, "2", 200); err != nil {
		t.Fatal(err)
	}
	rows, _ = s.ListAppDomainRecords(1, "box1", 10)
	if len(rows) != 1 || rows[0].Version != "2" || rows[0].UpdatedAt != 200 {
		t.Fatalf("upsert 语义错误: %+v", rows)
	}
}

// TestAppDomainRecordsTenantIsolation 锁定租户隔离：tenant 2 读不到 tenant 1 的记录，
// 错租户的读请求按空集返回而非报错（与其它读面一致）。
func TestAppDomainRecordsTenantIsolation(t *testing.T) {
	s := openTest(t)
	if err := s.UpsertAppDomainRecord(1, "box1", "window", "w1", "{}", "1", 1); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListAppDomainRecords(2, "box1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("跨租户泄漏: %+v", rows)
	}
	if _, err := s.GetAppDomainRecord(1, "box1", "window", "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing record: %v", err)
	}
}
