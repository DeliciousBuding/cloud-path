package store

import (
	"path/filepath"
	"testing"
	"time"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestMigrationReachesCurrentVersion(t *testing.T) {
	s := openTest(t)
	var v int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != schemaVersion {
		t.Fatalf("user_version = %d, want %d", v, schemaVersion)
	}
	if s.Version() != schemaVersion {
		t.Fatalf("Version() = %d, want %d", s.Version(), schemaVersion)
	}
	// v2 索引确实建起来了
	rows, err := s.db.Query(`SELECT name FROM sqlite_master WHERE type='index' AND name LIKE 'idx_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	want := []string{
		"idx_app_domain_records_tenant",
		"idx_audit_events_tenant_action", "idx_audit_events_tenant_created",
		"idx_commands_device", "idx_commands_status",
		"idx_commands_tenant_device", "idx_commands_tenant_status",
		"idx_device_state_tenant", "idx_devices_tenant",
		"idx_events_device_ts", "idx_events_tenant_device_ts", "idx_events_tenant_ts", "idx_events_ts",
		"idx_plugin_observations_tenant_reported",
		"idx_sessions_expires", "idx_sessions_user",
		"idx_tenant_tokens_hash", "idx_tenant_tokens_prefix", "idx_tenant_tokens_tenant",
		"idx_users_tenant",
	}
	if len(names) != len(want) {
		t.Fatalf("indexes = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("indexes = %v, want %v", names, want)
		}
	}
}

func TestMigrationIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "twice.db")
	s1, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s1.UpsertDevice("e1/d1", "e1", "stcb", "节点1", "COM3"); err != nil {
		t.Fatal(err)
	}
	if _, err := s1.AddEvent("e1/d1", "BOOT", "{}", time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}
	// 二次打开：迁移跳过、数据保留
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	devs, err := s2.ListDevices()
	if err != nil || len(devs) != 1 {
		t.Fatalf("devices lost on reopen: %d err=%v", len(devs), err)
	}
	st, err := s2.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if st.Events != 1 || st.Devices != 1 {
		t.Fatalf("stats after reopen = %+v", st)
	}
}

func TestDeviceLifecycle(t *testing.T) {
	s := openTest(t)
	if err := s.UpsertDevice("e1/d1", "e1", "stcb", "节点1", "COM3"); err != nil {
		t.Fatal(err)
	}
	// 重复 upsert = 更新而非报错
	if err := s.UpsertDevice("e1/d1", "e1", "stcb", "节点1改", "COM3"); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListDevices()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Name != "节点1改" || rows[0].EdgeID != "e1" {
		t.Fatalf("unexpected devices: %+v", rows)
	}

	if err := s.SetState("e1/d1", `{"hour":12}`, true, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	states, err := s.GetStates()
	if err != nil {
		t.Fatal(err)
	}
	st, ok := states["e1/d1"]
	if !ok || !st.Online || st.State != `{"hour":12}` {
		t.Fatalf("unexpected state: %+v", states)
	}
}

func TestEvents(t *testing.T) {
	s := openTest(t)
	_ = s.UpsertDevice("e1/d1", "e1", "stcb", "", "")
	base := time.Now().Unix()
	for i, typ := range []string{"BOOT", "REMIND", "TAKEN"} {
		if _, err := s.AddEvent("e1/d1", typ, "{}", base+int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.AddEvent("e1/d2", "MISSED", "{}", base+10); err != nil {
		t.Fatal(err)
	}

	all, err := s.ListEvents("", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 {
		t.Fatalf("want 4 events, got %d", len(all))
	}
	if all[0].Type != "MISSED" { // id DESC → 最新在前
		t.Fatalf("want newest first, got %s", all[0].Type)
	}

	only1, err := s.ListEvents("e1/d1", base+1, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(only1) != 2 || only1[0].Type != "TAKEN" || only1[1].Type != "REMIND" {
		t.Fatalf("filter failed: %+v", only1)
	}

	lim, err := s.ListEvents("", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(lim) != 1 {
		t.Fatalf("limit failed: %d", len(lim))
	}
}

func TestPruneEvents(t *testing.T) {
	s := openTest(t)
	base := time.Now().Unix()
	// 5 条事件：ts = base-10 .. base-6
	for i := 0; i < 5; i++ {
		if _, err := s.AddEvent("e1/d1", "BOOT", "{}", base-int64(10-i)); err != nil {
			t.Fatal(err)
		}
	}
	// 保留 ts >= base-8 → 删掉 base-10 / base-9 两条
	n, err := s.PruneEvents(base - 8)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("pruned = %d, want 2", n)
	}
	left, err := s.ListEvents("", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 3 || left[2].Ts != base-8 {
		t.Fatalf("unexpected survivors: %+v", left)
	}
}

func TestCommands(t *testing.T) {
	s := openTest(t)
	id, err := s.CreateCommand("e1/d1", "sync", "")
	if err != nil {
		t.Fatal(err)
	}
	if id <= 0 {
		t.Fatalf("bad id %d", id)
	}
	if _, err := s.CreateCommand("e1/d2", "dump", ""); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListCommands("", "pending", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Status != "pending" {
		t.Fatalf("unexpected: %+v", rows)
	}
	// 按设备过滤
	rows, err = s.ListCommands("e1/d1", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != id {
		t.Fatalf("device filter failed: %+v", rows)
	}
	if err := s.UpdateCommandStatus(id, "ok", "done"); err != nil {
		t.Fatal(err)
	}
	rows, err = s.ListCommands("", "ok", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Result != "done" || !rows[0].AckedAt.Valid {
		t.Fatalf("ack not recorded: %+v", rows)
	}
}

func TestTimeoutStaleCommands(t *testing.T) {
	s := openTest(t)
	id, _ := s.CreateCommand("e1/d1", "dump", "")
	// 把 created_at 拨老 2 分钟
	if _, err := s.db.Exec(`UPDATE commands SET created_at = created_at - 120 WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	n, err := s.TimeoutStaleCommands(90 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("want 1 timed out, got %d", n)
	}
	rows, _ := s.ListCommands("", "timeout", 10)
	if len(rows) != 1 || rows[0].ID != id {
		t.Fatalf("unexpected: %+v", rows)
	}
}

func TestPruneCommandsKeepsInFlight(t *testing.T) {
	s := openTest(t)
	old, _ := s.CreateCommand("e1/d1", "dump", "")
	pending, _ := s.CreateCommand("e1/d1", "sync", "")
	if err := s.UpdateCommandStatus(old, "ok", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE commands SET created_at = created_at - 100000`); err != nil {
		t.Fatal(err)
	}
	n, err := s.PruneCommands(time.Now().Unix() - 3600)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("pruned = %d, want 1（只清终态）", n)
	}
	rows, _ := s.ListCommands("", "", 10)
	if len(rows) != 1 || rows[0].ID != pending {
		t.Fatalf("in-flight command pruned: %+v", rows)
	}
}

func TestStats(t *testing.T) {
	s := openTest(t)
	_ = s.UpsertDevice("e1/d1", "e1", "stcb", "", "")
	base := time.Now().Unix()
	if _, err := s.AddEvent("e1/d1", "BOOT", "{}", base); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateCommand("e1/d1", "dump", ""); err != nil {
		t.Fatal(err)
	}
	st, err := s.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if st.Devices != 1 || st.Events != 1 || st.Commands != 1 || st.OldestEvent != base {
		t.Fatalf("unexpected stats: %+v", st)
	}
	if st.SchemaVer != schemaVersion {
		t.Fatalf("schema version = %d, want %d", st.SchemaVer, schemaVersion)
	}
}

func TestClampLimit(t *testing.T) {
	cases := map[int]int{0: 100, -5: 100, 50: 50, 1000: 1000, 5000: 100}
	for in, want := range cases {
		if got := clampLimit(in); got != want {
			t.Errorf("clampLimit(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestMemoryDSN(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.UpsertDevice("m/1", "m", "stcb", "", ""); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListDevices()
	if err != nil || len(rows) != 1 {
		t.Fatalf("shared-cache memory db broken: rows=%d err=%v", len(rows), err)
	}
}
