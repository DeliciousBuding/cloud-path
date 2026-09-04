package store

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// writeLegacyV6DB 手工构造一个 user_version=6 的库（v7/v8 之前最后一次已发布状态），
// 并塞入 v3–v6 各表真实数据，用于验证 v6→v7→v8 迁移不丢数据。
func writeLegacyV6DB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legacy-v6.db")
	db := rawSQLite(t, path, true)
	execAll(t, db, schemaV1, schemaV2, schemaV3, v4AlterSQL, schemaV4, schemaV5, schemaV6)
	execAll(t, db,
		`INSERT INTO devices(id,edge_id,adapter,name,port,first_seen,last_seen,tenant_id) VALUES('e1/d1','e1','stcb','d1','COM3',100,100,1)`,
		`INSERT INTO users(id,tenant_id,username,name,role,password_hash,created_at,disabled) VALUES(1,1,'admin','管理员','admin','hash',100,0)`,
		`INSERT INTO tenant_tokens(tenant_id,name,prefix,hash,scopes,created_at) VALUES(1,'svc','cp_','h','["read"]',100)`,
		`INSERT INTO audit_events(tenant_id,actor_type,actor_id,action,target_type,target_id,outcome,created_at)
		 VALUES(1,'user',1,'device.created','device','e1/d1','success',100)`,
		`PRAGMA user_version = 6`,
	)
	db.Close()
	return path
}

var v7Tables = []string{
	"plugin_desired_instances", "plugin_edge_revisions", "plugin_installations", "plugin_observations",
}

// TestMigrationV6ToV9 锁定：v6 库一次 Open 直达 v9，六张新表全部建齐，
// v3–v6 既有数据一行不丢且仍能通过既有 API 读到，外键完整性自检通过。
func TestMigrationV6ToV9(t *testing.T) {
	path := writeLegacyV6DB(t)
	s, err := Open(path)
	if err != nil {
		t.Fatalf("v6 -> v9 迁移失败: %v", err)
	}
	defer s.Close()

	if s.Version() != 9 {
		t.Fatalf("Version() = %d, want 9", s.Version())
	}
	db := rawSQLite(t, path, true)
	defer db.Close()
	if v := userVersion(t, db); v != 9 {
		t.Fatalf("user_version = %d, want 9", v)
	}
	for _, table := range append(append([]string{}, v7Tables...), "tenant_policies", "app_domain_records") {
		if tableMissing(t, s.db, table) {
			t.Fatalf("表 %s 未创建", table)
		}
	}
	// v6 数据完好
	devs, err := s.ListDevicesTenant(1)
	if err != nil || len(devs) != 1 || devs[0].ID != "e1/d1" {
		t.Fatalf("devices 丢失: %+v err=%v", devs, err)
	}
	if u, err := s.GetUserByID(1); err != nil || u.Username != "admin" {
		t.Fatalf("users 丢失: %+v err=%v", u, err)
	}
	toks, err := s.ListTenantTokens(1)
	if err != nil || len(toks) != 1 {
		t.Fatalf("tenant_tokens 丢失: %+v err=%v", toks, err)
	}
	audits, err := s.ListAuditEvents(1, 0, "", 10)
	if err != nil || len(audits) != 1 || audits[0].Action != "device.created" {
		t.Fatalf("audit_events 丢失: %+v err=%v", audits, err)
	}
	if err := foreignKeyCheckDB(s.db); err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
	// 迁移后新表可立即读写（不是空壳）
	if _, err := s.CreatePluginInstance(desiredRow(1, "e1", "i1", "p1")); err != nil {
		t.Fatalf("迁移后写入失败: %v", err)
	}
	if n, _ := s.CountPluginInstances(1); n != 1 {
		t.Fatalf("count = %d, want 1", n)
	}
}

// TestReopenDoesNotRerunV7V9 锁定「重复 Open 不重复迁移」：把 v7/v8/v9 的提交前钩子设成
// 必然失败，二次 Open 仍必须成功——只有迁移体被完整跳过才可能如此。数据同样不丢。
func TestReopenDoesNotRerunV7V9(t *testing.T) {
	path := writeLegacyV6DB(t)
	s1, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s1.CreatePluginInstance(desiredRow(1, "e1", "i1", "p1")); err != nil {
		t.Fatal(err)
	}
	if err := s1.SetTenantPolicy(1, TenantPolicyRow{QuotaPluginInstances: 42}); err != nil {
		t.Fatal(err)
	}
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}

	migrationTestHook = func(v int, phase string) error {
		if v >= 7 {
			return errors.New("v7/v8/v9 迁移不应在二次 Open 时重跑")
		}
		return nil
	}
	defer func() { migrationTestHook = nil }()

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("二次 Open 重跑了迁移: %v", err)
	}
	defer s2.Close()
	if s2.Version() != 9 {
		t.Fatalf("version = %d, want 9", s2.Version())
	}
	if n, _ := s2.CountPluginInstances(1); n != 1 {
		t.Fatalf("期望态丢失: %d, want 1", n)
	}
	pol, _ := s2.GetTenantPolicy(1)
	if pol.QuotaPluginInstances != 42 {
		t.Fatalf("策略丢失: %+v", pol)
	}
	// 第三次 Open 同样跳过
	s3, err := Open(path)
	if err != nil {
		t.Fatalf("三次 Open 重跑了迁移: %v", err)
	}
	s3.Close()
}

// TestMigrationV7FailureAtomic 锁定 v7 的 DDL 与 user_version 原子性：提交前注入故障后
// 四张表与版本号必须一起回滚（停在 v6），清故障重开可继续到 v8。
func TestMigrationV7FailureAtomic(t *testing.T) {
	path := writeLegacyV6DB(t)
	migrationTestHook = func(v int, phase string) error {
		if v == 7 && phase == "before_commit" {
			return errors.New("injected v7 commit failure")
		}
		return nil
	}
	if _, err := Open(path); err == nil {
		t.Fatal("Open 应失败（v7 注入）")
	}
	migrationTestHook = nil

	db := rawSQLite(t, path, true)
	if v := userVersion(t, db); v != 6 {
		t.Fatalf("user_version = %d, want 6（v7 DDL 与版本必须一起回滚）", v)
	}
	for _, table := range v7Tables {
		if !tableMissing(t, db, table) {
			t.Fatalf("回滚后 %s 不应存在", table)
		}
	}
	if !tableMissing(t, db, "tenant_policies") {
		t.Fatal("v7 失败后不应越级创建 tenant_policies")
	}
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("清故障后恢复失败: %v", err)
	}
	defer s.Close()
	if s.Version() != 9 {
		t.Fatalf("version = %d, want 9", s.Version())
	}
	if devs, err := s.ListDevicesTenant(1); err != nil || len(devs) != 1 {
		t.Fatalf("恢复后数据丢失: %+v err=%v", devs, err)
	}
	if err := foreignKeyCheckDB(s.db); err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
}

// TestMigrationV8FailureAtomic 锁定 v8 失败只回滚 v8：停在 v7，v7 四表保留，
// tenant_policies 不留残骸，重开可补齐到 v8。
func TestMigrationV8FailureAtomic(t *testing.T) {
	path := writeLegacyV6DB(t)
	migrationTestHook = func(v int, phase string) error {
		if v == 8 && phase == "before_commit" {
			return errors.New("injected v8 commit failure")
		}
		return nil
	}
	if _, err := Open(path); err == nil {
		t.Fatal("Open 应失败（v8 注入）")
	}
	migrationTestHook = nil

	db := rawSQLite(t, path, true)
	if v := userVersion(t, db); v != 7 {
		t.Fatalf("user_version = %d, want 7（v7 已提交，v8 已回滚）", v)
	}
	for _, table := range v7Tables {
		if tableMissing(t, db, table) {
			t.Fatalf("v7 表 %s 不应随 v8 回滚消失", table)
		}
	}
	if !tableMissing(t, db, "tenant_policies") {
		t.Fatal("v8 回滚后 tenant_policies 不应残留")
	}
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("恢复失败: %v", err)
	}
	defer s.Close()
	if s.Version() != 9 || tableMissing(t, s.db, "tenant_policies") || tableMissing(t, s.db, "app_domain_records") {
		t.Fatalf("恢复不完整: version=%d", s.Version())
	}
	if err := s.SetTenantPolicy(1, TenantPolicyRow{RetentionEventsDays: 15}); err != nil {
		t.Fatalf("恢复后写入失败: %v", err)
	}
}

// TestRecoverV7DDLAppliedVersionStale 是半迁移恢复反向验证：v7 DDL 已落库但 user_version
// 仍是 6（进程在提交版本前被杀，或人工只跑了一半 DDL），下次 Open 必须自愈到 v8，
// 既已存在的表与其中的数据一行不丢，缺失的表补齐。
func TestRecoverV7DDLAppliedVersionStale(t *testing.T) {
	path := writeLegacyV6DB(t)
	db := rawSQLite(t, path, true)
	execAll(t, db, schemaV7)
	// 人工制造「部分表缺失」：删掉两张 v7 表，模拟只跑了一半 DDL 的库
	execAll(t, db,
		`DROP TABLE plugin_observations`,
		`DROP TABLE plugin_installations`,
		`INSERT INTO plugin_desired_instances(tenant_id,edge_id,instance_id,plugin_id,version,enabled,
			isolation,config_json,secret_refs,revision,created_at,updated_at)
		 VALUES(1,'e1','i1','p1','1.0.0',1,'process','{}','[]',1,100,100)`,
		`INSERT INTO plugin_edge_revisions(tenant_id,edge_id,desired_revision) VALUES(1,'e1',1)`,
		`PRAGMA user_version = 6`,
	)
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("半迁移恢复失败: %v", err)
	}
	defer s.Close()
	if s.Version() != 9 {
		t.Fatalf("version = %d, want 9", s.Version())
	}
	for _, table := range append(append([]string{}, v7Tables...), "tenant_policies", "app_domain_records") {
		if tableMissing(t, s.db, table) {
			t.Fatalf("补齐失败：%s 缺失", table)
		}
	}
	// 既有期望态行完好，且 revision 游标连续（下一次写入必须是 2，不得重置或跳号）
	row, ok, err := s.GetPluginInstance(1, "e1", "i1")
	if err != nil || !ok {
		t.Fatalf("半迁移前写入的期望态丢失: ok=%v err=%v", ok, err)
	}
	if row.PluginID != "p1" || row.Revision != 1 {
		t.Fatalf("行内容损坏: %+v", row)
	}
	rev, err := s.CreatePluginInstance(desiredRow(1, "e1", "i2", "p1"))
	if err != nil {
		t.Fatal(err)
	}
	if rev != 2 {
		t.Fatalf("恢复后 revision = %d, want 2（游标必须连续）", rev)
	}
	if err := foreignKeyCheckDB(s.db); err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
}

// TestRecoverV8DDLAppliedVersionStale 锁定 v8 同类半迁移：tenant_policies 已建且有数据但
// user_version 仍是 7，Open 只补版本不重建、策略数据不被清空。
func TestRecoverV8DDLAppliedVersionStale(t *testing.T) {
	path := writeLegacyV6DB(t)
	db := rawSQLite(t, path, true)
	execAll(t, db, schemaV7, schemaV8)
	execAll(t, db,
		`INSERT INTO tenant_policies(tenant_id,quota_plugin_instances,retention_events_days,updated_at)
		 VALUES(1,33,20,100)`,
		`PRAGMA user_version = 7`,
	)
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("半迁移恢复失败: %v", err)
	}
	defer s.Close()
	if s.Version() != 9 {
		t.Fatalf("version = %d, want 9", s.Version())
	}
	pol, err := s.GetTenantPolicy(1)
	if err != nil {
		t.Fatal(err)
	}
	if pol.QuotaPluginInstances != 33 || pol.RetentionEventsDays != 20 {
		t.Fatalf("策略数据被迁移清空: %+v", pol)
	}
	// 配额立即生效：33 上限下第 34 个实例必须被拒
	if err := s.SetTenantPolicy(1, TenantPolicyRow{QuotaPluginInstances: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreatePluginInstance(desiredRow(1, "e1", "i1", "p1")); err != nil {
		t.Fatalf("首个实例应成功: %v", err)
	}
	if _, err := s.CreatePluginInstance(desiredRow(1, "e1", "i2", "p1")); !errors.Is(err, ErrPluginQuotaExceeded) {
		t.Fatalf("恢复后配额未生效: err = %v", err)
	}
}

// TestPluginTablesEnforceTenantForeignKey 锁定 v7/v8 的租户外键真实生效
// （DSN 开 foreign_keys=1）：伪造 tenant_id 的直写必须被拒，不会造出无主行。
func TestPluginTablesEnforceTenantForeignKey(t *testing.T) {
	s := openTest(t)
	queries := []string{
		`INSERT INTO plugin_desired_instances(tenant_id,edge_id,instance_id,plugin_id,created_at,updated_at)
		 VALUES(9999,'e1','i1','p1',1,1)`,
		`INSERT INTO plugin_edge_revisions(tenant_id,edge_id) VALUES(9999,'e1')`,
		`INSERT INTO plugin_installations(tenant_id,edge_id,plugin_id) VALUES(9999,'e1','p1')`,
		`INSERT INTO plugin_observations(tenant_id,edge_id,instance_id) VALUES(9999,'e1','i1')`,
		`INSERT INTO tenant_policies(tenant_id,quota_devices) VALUES(9999,10)`,
		`INSERT INTO app_domain_records(tenant_id,instance_id,record_type,record_id,data_json,version,updated_at) VALUES(9999,'i1','t','r','{}','',1)`,
	}
	for _, q := range queries {
		if _, err := s.db.Exec(q); err == nil {
			t.Fatalf("外键未拦住无主行: %s", strings.TrimSpace(q))
		}
	}
	for _, table := range append(append([]string{}, v7Tables...), "tenant_policies", "app_domain_records") {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("表 %s 留下了 %d 行无主数据", table, n)
		}
	}
	if err := foreignKeyCheckDB(s.db); err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
}
