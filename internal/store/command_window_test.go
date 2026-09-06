package store

import (
	"fmt"
	"testing"
)

func TestFailedCommandsWindowExactCountAndLimit(t *testing.T) {
	s := openTest(t)
	a, err := s.CreateTenant("window-a", "Window A")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateTenant("window-b", "Window B")
	if err != nil {
		t.Fatal(err)
	}
	const sampledAt int64 = 1_800_000_000
	const count = 2107 // Both statuses exceed the old per-status 1000-row counting cap.
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 0; i < count; i++ {
		status := "failed"
		if i%2 == 1 {
			status = "timeout"
		}
		// Higher IDs deliberately have older failure times; order must not follow insertion alone.
		if _, err := tx.Exec(`INSERT INTO commands(tenant_id, device_id, cmd, status, created_at, acked_at)
			VALUES(?, 'edge/device', 'ping', ?, ?, ?)`, a, status, sampledAt-90_000, sampledAt-int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO commands(tenant_id, device_id, cmd, status, created_at, acked_at)
		VALUES(?, 'other/device', 'ping', 'failed', ?, ?)`, b, sampledAt, sampledAt); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	for _, limit := range []int{1, 20, 1000, 0, 1001} {
		t.Run(fmt.Sprintf("limit-%d", limit), func(t *testing.T) {
			rows, total, err := s.FailedCommandsWindow(&a, sampledAt-86_400, sampledAt, limit)
			if err != nil {
				t.Fatal(err)
			}
			wantLen := limit
			if limit <= 0 || limit > 1000 {
				wantLen = 100
			}
			if total != count || len(rows) != wantLen {
				t.Fatalf("total=%d rows=%d, want total=%d rows=%d", total, len(rows), count, wantLen)
			}
			for i, row := range rows {
				if row.DeviceID != "edge/device" || row.AckedAt.Int64 != sampledAt-int64(i) {
					t.Fatalf("row %d is out of scope or failure-time order: %+v", i, row)
				}
			}
		})
	}
	rows, total, err := s.FailedCommandsWindow(nil, sampledAt-86_400, sampledAt, 20)
	if err != nil || total != count+1 || len(rows) != 20 || rows[0].DeviceID != "other/device" {
		t.Fatalf("unscoped window/tied failure-time ordering: total=%d rows=%+v err=%v", total, rows, err)
	}
	rows, total, err = s.FailedCommandsWindow(&a, sampledAt+1, sampledAt+86_400, 20)
	if err != nil || total != 0 || rows == nil || len(rows) != 0 {
		t.Fatalf("empty window: total=%d rows=%+v err=%v", total, rows, err)
	}
	for _, invalidTenant := range []int64{0, -1} {
		rows, total, err := s.FailedCommandsWindow(&invalidTenant, sampledAt-86_400, sampledAt, 20)
		if err != nil || total != 0 || len(rows) != 0 {
			t.Fatalf("invalid tenant %d became unscoped: total=%d rows=%+v err=%v", invalidTenant, total, rows, err)
		}
	}
}

func TestFailedCommandsWindowClosedStore(t *testing.T) {
	s := openTest(t)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	rows, total, err := s.FailedCommandsWindow(nil, 1, 2, 20)
	if err == nil || total != 0 || len(rows) != 0 {
		t.Fatalf("closed store returned a partial result: total=%d rows=%+v err=%v", total, rows, err)
	}
}
