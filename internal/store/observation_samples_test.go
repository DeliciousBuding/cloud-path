package store

import "testing"

func TestObservationSamplesUpsertPageAndPrune(t *testing.T) {
	s := openTest(t)
	if err := s.UpsertDevice("e1/d1", "e1", "stcb", "d1", "COM3"); err != nil {
		t.Fatal(err)
	}
	first := []ObservationSampleInput{
		{Key: "temperature", TS: 10, Value: 1, Quality: "good"},
		{Key: "temperature", TS: 20, Value: 2, Quality: "good"},
		{Key: "temperature", TS: 30, Value: 3, Quality: "good"},
		{Key: "temperature", TS: 40, Value: 4, Quality: "good"},
	}
	if err := s.AddObservationSamplesTenant(0, "e1/d1", first); err != nil {
		t.Fatal(err)
	}
	if err := s.AddObservationSamplesTenant(0, "e1/d1", []ObservationSampleInput{
		{Key: "temperature", TS: 40, Value: 4.5, Quality: "bad"},
	}); err != nil {
		t.Fatal(err)
	}

	rows, next, err := s.ListObservationSamples(0, "e1/d1", "temperature", 0, 0, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].TS != 30 || rows[1].TS != 40 || next != 30 {
		t.Fatalf("page1 = %+v next=%d", rows, next)
	}
	if rows[1].Value != 4.5 || rows[1].Quality != "bad" {
		t.Fatalf("same-second upsert lost: %+v", rows[1])
	}
	rows, next, err = s.ListObservationSamples(0, "e1/d1", "temperature", 0, 0, next, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].TS != 10 || rows[1].TS != 20 || next != 0 {
		t.Fatalf("page2 = %+v next=%d", rows, next)
	}

	n, err := s.PruneObservationSamples(25)
	if err != nil || n != 2 {
		t.Fatalf("prune = %d err=%v, want 2", n, err)
	}
	rows, _, err = s.ListObservationSamples(0, "e1/d1", "temperature", 0, 0, 0, 10)
	if err != nil || len(rows) != 2 || rows[0].TS != 30 || rows[1].TS != 40 {
		t.Fatalf("after prune = %+v err=%v", rows, err)
	}
}

func TestObservationSamplesTenantIsolation(t *testing.T) {
	s := openTest(t)
	defaultTenant, err := s.EnsureDefaultTenant()
	if err != nil {
		t.Fatal(err)
	}
	otherTenant, err := s.CreateTenant("samples-other", "Samples Other")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertDeviceTenant("e1/d1", "e1", "stcb", "d1", "COM3", defaultTenant); err != nil {
		t.Fatal(err)
	}
	if err := s.AddObservationSamplesTenant(defaultTenant, "e1/d1", []ObservationSampleInput{
		{Key: "temperature", TS: 100, Value: 25, Quality: "good"},
	}); err != nil {
		t.Fatal(err)
	}
	rows, _, err := s.ListObservationSamples(otherTenant, "e1/d1", "temperature", 0, 0, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("cross-tenant samples leaked: %+v", rows)
	}
}
