package store

import "testing"

func TestMigrationV12AddsHandledAt(t *testing.T) {
	s := openTest(t)
	var name string
	if err := s.db.QueryRow(`SELECT name FROM pragma_table_info('commands') WHERE name='handled_at'`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "handled_at" {
		t.Fatalf("handled_at column = %q", name)
	}
}
