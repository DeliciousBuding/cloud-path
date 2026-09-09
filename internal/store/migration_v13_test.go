package store

import "testing"

func TestMigrationV11ToV13AddsObservationSamplesAndHandledAt(t *testing.T) {
	path := writeLegacyV10DB(t)
	db := rawSQLite(t, path, true)
	execAll(t, db, schemaV11, `PRAGMA user_version = 11`)
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("v11 -> v13 migration: %v", err)
	}
	defer s.Close()
	if s.Version() != 13 {
		t.Fatalf("version = %d, want 13", s.Version())
	}

	var name string
	if err := s.db.QueryRow(`SELECT name FROM pragma_table_info('commands') WHERE name='handled_at'`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "handled_at" {
		t.Fatalf("handled_at column = %q", name)
	}

	var table string
	if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='observation_samples'`).Scan(&table); err != nil {
		t.Fatal(err)
	}
	if table != "observation_samples" {
		t.Fatalf("observation_samples table = %q", table)
	}
}
