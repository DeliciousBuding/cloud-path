package store

import (
	"errors"
	"testing"
)

func writeLegacyV10DB(t *testing.T) string {
	t.Helper()
	path := writeLegacyV6DB(t)
	db := rawSQLite(t, path, true)
	execAll(t, db, schemaV7, schemaV8, schemaV9, schemaV10, `PRAGMA user_version = 10`)
	db.Close()
	return path
}

func TestMigrationV10ToV11PreservesDevices(t *testing.T) {
	path := writeLegacyV10DB(t)
	s, err := Open(path)
	if err != nil {
		t.Fatalf("v10 -> v11 migration: %v", err)
	}
	defer s.Close()
	if s.Version() != 11 {
		t.Fatalf("version = %d, want 11", s.Version())
	}
	devs, err := s.ListDevicesTenant(1)
	if err != nil || len(devs) != 1 {
		t.Fatalf("devices after migration = %+v err=%v", devs, err)
	}
	if devs[0].ID != "e1/d1" || devs[0].Name != "d1" || devs[0].DescriptorJSON != "" {
		t.Fatalf("device data/descriptor default = %+v", devs[0])
	}
	var typ string
	var notNull, pk int
	var dflt any
	found := false
	rows, err := s.db.Query(`PRAGMA table_info(devices)`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var cid int
		var name string
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		if name == "descriptor_json" {
			found = true
			break
		}
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if !found || typ != "TEXT" || notNull != 1 {
		t.Fatalf("descriptor_json column missing/wrong: found=%v type=%q notnull=%d", found, typ, notNull)
	}
}

func TestRecoverV11DDLAppliedVersionStale(t *testing.T) {
	path := writeLegacyV10DB(t)
	db := rawSQLite(t, path, true)
	execAll(t, db, schemaV11, `PRAGMA user_version = 10`)
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("half migration recovery: %v", err)
	}
	defer s.Close()
	if s.Version() != 11 {
		t.Fatalf("version = %d, want 11", s.Version())
	}
	devs, err := s.ListDevicesTenant(1)
	if err != nil || len(devs) != 1 || devs[0].ID != "e1/d1" {
		t.Fatalf("device data lost during recovery: %+v err=%v", devs, err)
	}
	if err := s.SetDeviceDescriptor("e1/d1", `{"device_id":"e1/d1"}`); err != nil {
		t.Fatalf("recovered column not writable: %v", err)
	}
}

func TestMigrationV11FailureAtomic(t *testing.T) {
	path := writeLegacyV10DB(t)
	migrationTestHook = func(v int, phase string) error {
		if v == 11 && phase == "before_commit" {
			return errors.New("injected v11 commit failure")
		}
		return nil
	}
	defer func() { migrationTestHook = nil }()
	if _, err := Open(path); err == nil {
		t.Fatal("Open should fail when v11 commit is injected")
	}
	migrationTestHook = nil

	db := rawSQLite(t, path, true)
	if v := userVersion(t, db); v != 10 {
		t.Fatalf("user_version = %d, want 10 after rollback", v)
	}
	var id string
	if err := db.QueryRow(`SELECT id FROM devices WHERE id='e1/d1'`).Scan(&id); err != nil || id != "e1/d1" {
		t.Fatalf("device row after rollback: id=%q err=%v", id, err)
	}
	if err := db.QueryRow(`SELECT descriptor_json FROM devices LIMIT 1`).Scan(&id); err == nil {
		t.Fatal("descriptor_json must not survive v11 rollback")
	}
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("recovery after v11 rollback: %v", err)
	}
	defer s.Close()
	if s.Version() != 11 {
		t.Fatalf("version = %d, want 11", s.Version())
	}
	if err := s.SetDeviceDescriptor("e1/d1", `{"device_id":"e1/d1"}`); err != nil {
		t.Fatalf("recovered column not writable: %v", err)
	}
}
