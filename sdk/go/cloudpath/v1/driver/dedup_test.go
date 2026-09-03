package driver

import "testing"

// TestSequenceDedup verifies the Core boundary rule: duplicates and stale
// (out-of-order) sequences are dropped per (instance, device) scope, while
// independent scopes advance independently.
func TestSequenceDedup(t *testing.T) {
	tr := NewSequenceTracker()

	if !tr.Accept("inst-a", "dev-1", 1) {
		t.Fatal("first sequence 1 must be accepted")
	}
	if !tr.Accept("inst-a", "dev-1", 2) {
		t.Fatal("sequence 2 must be accepted")
	}
	if tr.Accept("inst-a", "dev-1", 2) {
		t.Fatal("duplicate sequence 2 must be dropped")
	}
	if tr.Accept("inst-a", "dev-1", 1) {
		t.Fatal("stale sequence 1 must be dropped")
	}
	if tr.Accept("inst-a", "dev-1", 0) {
		t.Fatal("sequence 0 must be dropped")
	}
	if got := tr.Last("inst-a", "dev-1"); got != 2 {
		t.Fatalf("last = %d, want 2", got)
	}

	// Different device: independent scope.
	if !tr.Accept("inst-a", "dev-2", 1) {
		t.Fatal("dev-2 sequence 1 must be accepted")
	}
	// Different instance: independent scope.
	if !tr.Accept("inst-b", "dev-1", 1) {
		t.Fatal("inst-b sequence 1 must be accepted")
	}
	if got := tr.Count(); got != 3 {
		t.Fatalf("scope count = %d, want 3", got)
	}

	// Out-of-order recovery: a later higher sequence is still accepted.
	if !tr.Accept("inst-a", "dev-1", 5) {
		t.Fatal("sequence 5 after stale 1 must be accepted")
	}
	if tr.Accept("inst-a", "dev-1", 3) {
		t.Fatal("sequence 3 below last 5 must be dropped")
	}

	tr.Reset()
	if got := tr.Count(); got != 0 {
		t.Fatalf("scope count after reset = %d, want 0", got)
	}
	if !tr.Accept("inst-a", "dev-1", 1) {
		t.Fatal("after reset sequence 1 must be accepted in the fresh space")
	}
}

func TestValidateDriverMessage(t *testing.T) {
	base := func() *DriverMessage {
		return &DriverMessage{
			PluginInstanceID: "inst-1", Sequence: 1, SchemaVersion: "1", DeviceID: "dev-1",
			Union: &DeviceUpsert{Device: Device{DeviceID: "dev-1"}},
		}
	}
	if err := ValidateDriverMessage(base()); err != nil {
		t.Fatalf("valid message rejected: %v", err)
	}
	m := base()
	m.Sequence = 0
	if err := ValidateDriverMessage(m); err == nil {
		t.Fatal("zero sequence must be rejected")
	}
	m = base()
	m.Union = nil
	if err := ValidateDriverMessage(m); err == nil {
		t.Fatal("missing body must be rejected")
	}
}
