package store

import "testing"

func TestMarkFailedCommandsHandledExcludesOnlyHandledFailures(t *testing.T) {
	s := openTest(t)
	a, err := s.CreateTenant("handled-a", "Handled A")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateTenant("handled-b", "Handled B")
	if err != nil {
		t.Fatal(err)
	}
	const now int64 = 1_800_000_000
	insert := func(tenant int64, device, status string, created int64) int64 {
		t.Helper()
		res, err := s.db.Exec(`INSERT INTO commands(tenant_id, device_id, cmd, status, created_at, acked_at, result)
			VALUES(?, ?, 'ping', ?, ?, ?, 'failed')`, tenant, device, status, created, created)
		if err != nil {
			t.Fatal(err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	failedA := insert(a, "ea/d1", "failed", now-10)
	timeoutA := insert(a, "ea/d1", "timeout", now-20)
	okA := insert(a, "ea/d1", "ok", now-30)
	insert(b, "eb/d1", "failed", now-10)

	n, err := s.MarkFailedCommandsHandled(&a, now-100, now, now)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("handled = %d, want 2", n)
	}
	rows, total, err := s.FailedCommandsWindow(&a, now-100, now, 20)
	if err != nil || total != 0 || len(rows) != 0 {
		t.Fatalf("overview window after handled = total:%d rows:%+v err:%v", total, rows, err)
	}
	handled, err := s.ListCommandsTenantFiltered(a, "", "", "handled", 20)
	if err != nil || len(handled) != 2 {
		t.Fatalf("handled rows = %+v err=%v", handled, err)
	}
	unhandled, err := s.ListCommandsTenantFiltered(a, "", "", "unhandled", 20)
	if err != nil || len(unhandled) != 1 || unhandled[0].ID != okA {
		t.Fatalf("unhandled rows = %+v err=%v, want only ok id=%d", unhandled, err, okA)
	}
	for _, row := range handled {
		if row.ID != failedA && row.ID != timeoutA {
			t.Fatalf("unexpected handled row: %+v", row)
		}
		if !row.HandledAt.Valid {
			t.Fatalf("handled row missing timestamp: %+v", row)
		}
	}
	other, total, err := s.FailedCommandsWindow(&b, now-100, now, 20)
	if err != nil || total != 1 || len(other) != 1 {
		t.Fatalf("tenant isolation: total=%d rows=%+v err=%v", total, other, err)
	}
}

func TestMarkCommandsHandledOnlyTargetsFailedOrTimeout(t *testing.T) {
	s := openTest(t)
	a, err := s.CreateTenant("handled-single", "Handled Single")
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.db.Exec(`INSERT INTO commands(tenant_id, device_id, cmd, status, created_at) VALUES(?, 'ea/d1', 'ping', 'ok', 1)`, a)
	if err != nil {
		t.Fatal(err)
	}
	okID, _ := res.LastInsertId()
	res, err = s.db.Exec(`INSERT INTO commands(tenant_id, device_id, cmd, status, created_at) VALUES(?, 'ea/d1', 'ping', 'failed', 2)`, a)
	if err != nil {
		t.Fatal(err)
	}
	failedID, _ := res.LastInsertId()
	n, err := s.MarkCommandsHandled(&a, []int64{okID, failedID}, 99)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("handled = %d, want 1", n)
	}
	rows, err := s.ListCommandsTenantFiltered(a, "", "", "handled", 10)
	if err != nil || len(rows) != 1 || rows[0].ID != failedID {
		t.Fatalf("handled rows = %+v err=%v", rows, err)
	}
}
