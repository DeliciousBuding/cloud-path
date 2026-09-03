package application

import (
	"context"
	"errors"
	"os"
	"testing"
)

const (
	capAlarm      = "cloudpath.dev/capability/alarm@1"
	capContact    = "cloudpath.dev/capability/contact@1"
	capDisplay    = "cloudpath.dev/capability/display-text@1"
	capAlarmV2    = "cloudpath.dev/capability/alarm@2"
	capContactOld = "cloudpath.dev/capability/contact@0"
)

func scRequirements() []Requirement {
	return []Requirement{
		{ID: "reminder-output", Capability: capAlarm, Cardinality: CardinalityOne},
		{ID: "compartments", Capability: capContact, Cardinality: CardinalityOneOrMore, MinItems: 3},
		{ID: "local-display", Capability: capDisplay, Cardinality: CardinalityZeroOrOne},
	}
}

func scCandidates() []Candidate {
	return []Candidate{
		{EntityID: "compartment-1", TenantID: "tenant-a", Capabilities: []string{capContact}},
		{EntityID: "compartment-2", TenantID: "tenant-a", Capabilities: []string{capContact}},
		{EntityID: "compartment-3", TenantID: "tenant-a", Capabilities: []string{capContact}},
		{EntityID: "alarm-1", TenantID: "tenant-a", Capabilities: []string{capAlarm}},
		{EntityID: "display-1", TenantID: "tenant-a", Capabilities: []string{capDisplay}},
	}
}

func TestMatchRequirements(t *testing.T) {
	binder := Binder{ApplicationID: "app-sc", PluginInstanceID: "inst-1", TenantID: "tenant-a"}
	bs, err := binder.Match(scRequirements(), scCandidates())
	if err != nil {
		t.Fatalf("Match returned error: %v", err)
	}

	byReq := map[string][]string{}
	for _, b := range bs.Bindings {
		byReq[b.RequirementID] = append(byReq[b.RequirementID], b.EntityID)
	}

	if got := byReq["reminder-output"]; len(got) != 1 || got[0] != "alarm-1" {
		t.Fatalf("reminder-output = %v, want [alarm-1]", got)
	}
	if got := byReq["compartments"]; len(got) != 3 {
		t.Fatalf("compartments = %v, want 3 entities", got)
	}
	if got := byReq["local-display"]; len(got) != 1 || got[0] != "display-1" {
		t.Fatalf("local-display = %v, want [display-1]", got)
	}

	res := binder.Validate(scRequirements(), scCandidates(), bs.Bindings)
	if !res.Valid {
		t.Fatalf("matched binding set should validate: %+v", res.Issues)
	}

	if bs.TenantID != "tenant-a" {
		t.Fatalf("binding set tenant = %q, want tenant-a", bs.TenantID)
	}
}

func TestMinimumCardinality(t *testing.T) {
	binder := Binder{TenantID: "tenant-a"}
	reqs := []Requirement{
		{ID: "compartments", Capability: capContact, Cardinality: CardinalityOneOrMore, MinItems: 3},
	}
	candidates := []Candidate{
		{EntityID: "c1", TenantID: "tenant-a", Capabilities: []string{capContact}},
		{EntityID: "c2", TenantID: "tenant-a", Capabilities: []string{capContact}},
	}

	_, err := binder.Match(reqs, candidates)
	var berr *BindingError
	if !errors.As(err, &berr) {
		t.Fatalf("expected *BindingError, got %v", err)
	}
	if !berr.Result.HasCode(CodeBelowMinItems) {
		t.Fatalf("expected CodeBelowMinItems, got %+v", berr.Result.Issues)
	}

	// Explicit validation of a too-small binding set.
	bindings := []Binding{
		{RequirementID: "compartments", EntityID: "c1"},
		{RequirementID: "compartments", EntityID: "c2"},
	}
	res := binder.Validate(reqs, candidates, bindings)
	if res.Valid {
		t.Fatalf("2 entities for minItems 3 must be invalid")
	}
	if !res.HasCode(CodeBelowMinItems) {
		t.Fatalf("expected CodeBelowMinItems, got %+v", res.Issues)
	}
}

func TestOptionalRequirement(t *testing.T) {
	binder := Binder{TenantID: "tenant-a"}
	reqs := []Requirement{
		{ID: "local-display", Capability: capDisplay, Cardinality: CardinalityZeroOrOne},
	}

	// Present: the optional requirement binds it.
	withDisplay := []Candidate{{EntityID: "display-1", TenantID: "tenant-a", Capabilities: []string{capDisplay}}}
	bs, err := binder.Match(reqs, withDisplay)
	if err != nil {
		t.Fatalf("Match with display returned error: %v", err)
	}
	if len(bs.Bindings) != 1 || bs.Bindings[0].EntityID != "display-1" {
		t.Fatalf("optional bindings = %+v, want [display-1]", bs.Bindings)
	}

	// Absent: zero-or-one stays unbound and the set is still valid.
	noDisplay := []Candidate{{EntityID: "alarm-1", TenantID: "tenant-a", Capabilities: []string{capAlarm}}}
	bs2, err := binder.Match(reqs, noDisplay)
	if err != nil {
		t.Fatalf("Match without display returned error: %v", err)
	}
	if len(bs2.Bindings) != 0 {
		t.Fatalf("optional with no candidate should bind nothing, got %+v", bs2.Bindings)
	}
	if res := binder.Validate(reqs, noDisplay, bs2.Bindings); !res.Valid {
		t.Fatalf("empty optional binding must validate: %+v", res.Issues)
	}
}

func TestStableEntityBinding(t *testing.T) {
	ctx := context.Background()
	reqs := []Requirement{
		{ID: "reminder-output", Capability: capAlarm, Cardinality: CardinalityOne},
	}
	binder := Binder{ApplicationID: "app", PluginInstanceID: "inst", TenantID: "tenant-a"}

	c1 := Candidate{EntityID: "alarm-1", Name: "Alarm", TenantID: "tenant-a", Capabilities: []string{capAlarm}, DeviceID: "dev-1", DriverID: "stcb"}
	bs, err := binder.Match(reqs, []Candidate{c1})
	if err != nil {
		t.Fatalf("first Match error: %v", err)
	}
	if got := bs.Bindings[0].EntityID; got != "alarm-1" {
		t.Fatalf("binding entity = %q, want alarm-1", got)
	}

	// The same physical entity, after a rename and an Edge reconnection on a
	// different device/driver, must produce the same stable entity binding.
	c2 := Candidate{EntityID: "alarm-1", Name: "Renamed Alarm", TenantID: "tenant-a", Capabilities: []string{capAlarm}, DeviceID: "dev-2", DriverID: "other"}
	bs2, err := binder.Match(reqs, []Candidate{c2})
	if err != nil {
		t.Fatalf("second Match error: %v", err)
	}
	if got := bs2.Bindings[0].EntityID; got != "alarm-1" {
		t.Fatalf("binding changed after rename/reconnect: got %q, want alarm-1", got)
	}

	// Repository persists only the stable entity_id.
	repo := NewMemoryRepository()
	if err := repo.SaveBindingSet(ctx, bs); err != nil {
		t.Fatalf("SaveBindingSet error: %v", err)
	}
	got, err := repo.LoadBindingSet(ctx, "app", "inst")
	if err != nil {
		t.Fatalf("LoadBindingSet error: %v", err)
	}
	if len(got.Bindings) != 1 || got.Bindings[0].EntityID != "alarm-1" {
		t.Fatalf("repo binding = %+v, want stable entity alarm-1", got.Bindings)
	}
}

func TestRejectDriverCoupling(t *testing.T) {
	binder := Binder{TenantID: "tenant-t"}
	req := Requirement{ID: "r", Capability: capAlarm, Cardinality: CardinalityOne}

	// Same capability, different Driver and Device: matching must select purely
	// on capability and tenant, not on driver/device.
	candidates := []Candidate{
		{EntityID: "a", TenantID: "tenant-t", Capabilities: []string{capAlarm}, DriverID: "stcb", DeviceID: "dev-1"},
		{EntityID: "b", TenantID: "tenant-t", Capabilities: []string{capAlarm}, DriverID: "other", DeviceID: "dev-2"},
	}
	bs, err := binder.Match([]Requirement{req}, candidates)
	if err != nil {
		t.Fatalf("matching should ignore driver/device, got error: %v", err)
	}
	if len(bs.Bindings) != 1 {
		t.Fatalf("want one binding, got %+v", bs.Bindings)
	}

	// A candidate with the "right" driver but the wrong capability must NOT match.
	wrongCap := []Candidate{
		{EntityID: "c", TenantID: "tenant-t", Capabilities: []string{capContact}, DriverID: "stcb", DeviceID: "dev-1"},
	}
	_, err = binder.Match([]Requirement{req}, wrongCap)
	var berr *BindingError
	if !errors.As(err, &berr) {
		t.Fatalf("expected *BindingError for capability mismatch, got %v", err)
	}
	if !berr.Result.HasCode(CodeMissingRequired) {
		t.Fatalf("expected CodeMissingRequired, got %+v", berr.Result.Issues)
	}
}

func TestRejectCrossTenantBinding(t *testing.T) {
	binder := Binder{TenantID: "tenant-a"}
	req := Requirement{ID: "r", Capability: capAlarm, Cardinality: CardinalityOne}

	// A candidate that belongs to another tenant must not be matched.
	foreignCandidate := Candidate{EntityID: "alarm-b", TenantID: "tenant-b", Capabilities: []string{capAlarm}}
	_, err := binder.Match([]Requirement{req}, []Candidate{foreignCandidate})
	var berr *BindingError
	if !errors.As(err, &berr) {
		t.Fatalf("expected *BindingError, got %v", err)
	}
	if !berr.Result.HasCode(CodeMissingRequired) {
		t.Fatalf("expected CodeMissingRequired, got %+v", berr.Result.Issues)
	}

	// Explicit validation: a binding that references the cross-tenant entity
	// must fail with CodeCrossTenant.
	bindings := []Binding{{RequirementID: "r", EntityID: "alarm-b"}}
	res := binder.Validate([]Requirement{req}, []Candidate{foreignCandidate}, bindings)
	if res.Valid {
		t.Fatalf("cross-tenant binding must be invalid")
	}
	if !res.HasCode(CodeCrossTenant) {
		t.Fatalf("expected CodeCrossTenant, got %+v", res.Issues)
	}
}

func TestScheduledCompartmentBinding(t *testing.T) {
	data, err := os.ReadFile("../../examples/scheduled-compartment/requirements.yaml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	reqs, err := ParseRequirements(data)
	if err != nil {
		t.Fatalf("ParseRequirements: %v", err)
	}
	if len(reqs) != 3 {
		t.Fatalf("expected 3 requirements, got %d", len(reqs))
	}

	binder := Binder{
		ApplicationID:    "io.github.deliciousbuding.cloud-path-app-scheduled-compartment",
		PluginInstanceID: "inst-1",
		TenantID:         "tenant-a",
	}
	bs, err := binder.Match(reqs, scCandidates())
	if err != nil {
		t.Fatalf("scheduled-compartment Match error: %v", err)
	}
	if res := binder.Validate(reqs, scCandidates(), bs.Bindings); !res.Valid {
		t.Fatalf("scheduled-compartment binding must validate: %+v", res.Issues)
	}

	counts := map[string]int{}
	for _, b := range bs.Bindings {
		counts[b.RequirementID]++
	}
	if counts["reminder-output"] != 1 {
		t.Fatalf("reminder-output count = %d, want 1", counts["reminder-output"])
	}
	if counts["compartments"] < 3 {
		t.Fatalf("compartments count = %d, want >= 3", counts["compartments"])
	}
}

func TestVersionCompatibility(t *testing.T) {
	binder := Binder{TenantID: "tenant-a"}

	// A newer capability version satisfies an older requirement.
	req1 := Requirement{ID: "r", Capability: capAlarm, Cardinality: CardinalityOne}
	newer := Candidate{EntityID: "e", TenantID: "tenant-a", Capabilities: []string{capAlarmV2}}
	if _, err := binder.Match([]Requirement{req1}, []Candidate{newer}); err != nil {
		t.Fatalf("requiring alarm@1 should accept alarm@2, got error: %v", err)
	}

	// An older capability version is incompatible.
	req2 := Requirement{ID: "r", Capability: capAlarmV2, Cardinality: CardinalityOne}
	older := Candidate{EntityID: "e", TenantID: "tenant-a", Capabilities: []string{capAlarm}}
	_, err := binder.Match([]Requirement{req2}, []Candidate{older})
	var berr *BindingError
	if !errors.As(err, &berr) {
		t.Fatalf("expected *BindingError, got %v", err)
	}
	res := berr.Result
	if !res.HasCode(CodeMissingRequired) && !res.HasCode(CodeVersionIncompatible) {
		t.Fatalf("expected version-incompatible outcome, got %+v", res.Issues)
	}

	// Explicit validation surfaces a precise version_incompatible code.
	val := binder.Validate([]Requirement{req2}, []Candidate{older}, []Binding{{RequirementID: "r", EntityID: "e"}})
	if !val.HasCode(CodeVersionIncompatible) {
		t.Fatalf("expected CodeVersionIncompatible, got %+v", val.Issues)
	}

	// A wrong capability family is a distinct mismatch, not a version problem.
	wrongFamily := Candidate{EntityID: "e", TenantID: "tenant-a", Capabilities: []string{capContactOld}}
	val2 := binder.Validate([]Requirement{req1}, []Candidate{wrongFamily}, []Binding{{RequirementID: "r", EntityID: "e"}})
	if !val2.HasCode(CodeCapabilityMismatch) {
		t.Fatalf("expected CodeCapabilityMismatch, got %+v", val2.Issues)
	}
}

func TestCandidateWithMixedCapabilityVersions(t *testing.T) {
	binder := Binder{TenantID: "tenant-a"}
	req := Requirement{ID: "r", Capability: capAlarmV2, Cardinality: CardinalityOne}

	// The entity provides an old (incompatible) and a new (compatible) version;
	// the compatible one must win.
	candidate := Candidate{EntityID: "e", TenantID: "tenant-a", Capabilities: []string{capAlarm, capAlarmV2}}
	if _, err := binder.Match([]Requirement{req}, []Candidate{candidate}); err != nil {
		t.Fatalf("a compatible alarm@2 capability must win over alarm@1, got error: %v", err)
	}
}
