package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// writeLegacyV3DB 手工构造一个 user_version=3 的库，带 1 台 default 租户设备及其状态/事件/命令。
func writeLegacyV3DB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legacy-v3.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, ddl := range []string{schemaV1, schemaV2, schemaV3} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatalf("exec legacy schema: %v", err)
		}
	}
	stmts := []string{
		`INSERT INTO tenant(id,slug,name,created_at) VALUES(1,'default','default',1)`,
		`INSERT INTO devices(id,edge_id,adapter,name,port,first_seen,last_seen) VALUES('e1/d1','e1','stcb','d1','COM3',100,100)`,
		`INSERT INTO device_state(device_id,state,online,updated_at) VALUES('e1/d1','{"clock":"08:00"}',1,100)`,
		`INSERT INTO events(device_id,ts,type,payload) VALUES('e1/d1',100,'BOOT','{}')`,
		`INSERT INTO commands(device_id,cmd,args,status,created_at) VALUES('e1/d1','sync','','pending',100)`,
		`PRAGMA user_version = 3`,
	}
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("seed legacy db: %v", err)
		}
	}
	return path
}

// TestSchemaTenantMigration 锁定 v3→v4 迁移：加 tenant_id、回填 default、保留数据、二次打开不丢。
func TestSchemaTenantMigration(t *testing.T) {
	path := writeLegacyV3DB(t)

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Version() != schemaVersion {
		t.Fatalf("version = %d, want %d", s.Version(), schemaVersion)
	}
	var got int64
	if err := s.db.QueryRow(`SELECT tenant_id FROM devices WHERE id='e1/d1'`).Scan(&got); err != nil || got != 1 {
		t.Fatalf("devices tenant backfill = %d err=%v", got, err)
	}
	if err := s.db.QueryRow(`SELECT tenant_id FROM device_state WHERE device_id='e1/d1'`).Scan(&got); err != nil || got != 1 {
		t.Fatalf("device_state tenant backfill = %d err=%v", got, err)
	}
	if err := s.db.QueryRow(`SELECT tenant_id FROM events WHERE device_id='e1/d1'`).Scan(&got); err != nil || got != 1 {
		t.Fatalf("events tenant backfill = %d err=%v", got, err)
	}
	if err := s.db.QueryRow(`SELECT tenant_id FROM commands WHERE device_id='e1/d1'`).Scan(&got); err != nil || got != 1 {
		t.Fatalf("commands tenant backfill = %d err=%v", got, err)
	}
	devs, err := s.ListDevicesTenant(1)
	if err != nil || len(devs) != 1 || devs[0].TenantSlug != "default" {
		t.Fatalf("devices after migration = %+v err=%v", devs, err)
	}
	evs, err := s.ListEventsTenant(1, "", 0, 100)
	if err != nil || len(evs) != 1 {
		t.Fatalf("events after migration = %+v err=%v", evs, err)
	}
	cmds, err := s.ListCommandsTenant(1, "", "", 100)
	if err != nil || len(cmds) != 1 {
		t.Fatalf("commands after migration = %+v err=%v", cmds, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// 二次打开：迁移跳过、数据保留
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	devs2, err := s2.ListDevicesTenant(1)
	if err != nil || len(devs2) != 1 {
		t.Fatalf("reopen lost devices: %+v err=%v", devs2, err)
	}
}

// TestDefaultTenantBackfill 反向锁定既有业务行在迁移后全部归 default 租户。
func TestDefaultTenantBackfill(t *testing.T) {
	path := writeLegacyV3DB(t)
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var defID int64
	if err := s.db.QueryRow(`SELECT id FROM tenant WHERE slug='default'`).Scan(&defID); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`SELECT tenant_id FROM devices WHERE id='e1/d1'`,
		`SELECT tenant_id FROM device_state WHERE device_id='e1/d1'`,
		`SELECT tenant_id FROM events WHERE device_id='e1/d1'`,
		`SELECT tenant_id FROM commands WHERE device_id='e1/d1'`,
	} {
		var got int64
		if err := s.db.QueryRow(q).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != defID {
			t.Fatalf("%s backfill = %d, want %d", q, got, defID)
		}
	}
}

// TestStoreTenantIsolation 锁定 store 侧租户隔离查询与写入继承。
func TestStoreTenantIsolation(t *testing.T) {
	s := openTest(t)

	mkTenant := func(slug string) int64 {
		t.Helper()
		res, err := s.db.Exec(`INSERT INTO tenant(slug,name,created_at) VALUES(?,?,1)`, slug, slug)
		if err != nil {
			t.Fatal(err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	a := mkTenant("tenant-a")
	b := mkTenant("tenant-b")

	if err := s.UpsertDeviceTenant("a/d1", "a", "stcb", "A", "COM1", a); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertDeviceTenant("b/d2", "b", "stcb", "B", "COM2", b); err != nil {
		t.Fatal(err)
	}
	if err := s.SetState("a/d1", `{"x":1}`, true, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.SetState("b/d2", `{"x":2}`, true, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddEvent("a/d1", "BOOT", "{}", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddEvent("b/d2", "MISSED", "{}", 2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateCommandTenant("a/d1", "sync", "", a); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateCommandTenant("b/d2", "dump", "", b); err != nil {
		t.Fatal(err)
	}

	devA, err := s.ListDevicesTenant(a)
	if err != nil || len(devA) != 1 || devA[0].ID != "a/d1" {
		t.Fatalf("tenant-a devices = %+v err=%v", devA, err)
	}
	devB, err := s.ListDevicesTenant(b)
	if err != nil || len(devB) != 1 || devB[0].ID != "b/d2" {
		t.Fatalf("tenant-b devices = %+v err=%v", devB, err)
	}
	evA, err := s.ListEventsTenant(a, "", 0, 100)
	if err != nil || len(evA) != 1 || evA[0].DeviceID != "a/d1" {
		t.Fatalf("tenant-a events = %+v err=%v", evA, err)
	}
	evB, err := s.ListEventsTenant(b, "", 0, 100)
	if err != nil || len(evB) != 1 || evB[0].DeviceID != "b/d2" {
		t.Fatalf("tenant-b events = %+v err=%v", evB, err)
	}
	// tenant A 即便知道 tenant B 的 device key，也拿不到 B 的事件/命令。
	if ev, _ := s.ListEventsTenant(a, "b/d2", 0, 100); len(ev) != 0 {
		t.Fatalf("tenant-a saw tenant-b events via device filter: %+v", ev)
	}
	cmdA, err := s.ListCommandsTenant(a, "", "", 100)
	if err != nil || len(cmdA) != 1 || cmdA[0].DeviceID != "a/d1" {
		t.Fatalf("tenant-a commands = %+v err=%v", cmdA, err)
	}
	if cm, _ := s.ListCommandsTenant(a, "b/d2", "", 100); len(cm) != 0 {
		t.Fatalf("tenant-a saw tenant-b commands via device filter: %+v", cm)
	}
	statsA, err := s.StatsTenant(a)
	if err != nil || statsA.Devices != 1 || statsA.Events != 1 || statsA.Commands != 1 {
		t.Fatalf("tenant-a stats = %+v err=%v", statsA, err)
	}
	statsB, err := s.StatsTenant(b)
	if err != nil || statsB.Devices != 1 || statsB.Events != 1 || statsB.Commands != 1 {
		t.Fatalf("tenant-b stats = %+v err=%v", statsB, err)
	}
}
