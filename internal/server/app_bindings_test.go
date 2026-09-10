package server

import (
	"encoding/json"
	"testing"

	"github.com/DeliciousBuding/cloud-path/internal/application"
	"github.com/DeliciousBuding/cloud-path/internal/model"
)

func TestApplicationExplicitBindingsPreserveSelectionAndRejectFallback(t *testing.T) {
	cap := "example.test/capability/input@1"
	reqs := []application.Requirement{{ID: "source", Capability: cap, Cardinality: application.CardinalityOne}}
	candidates := []application.Candidate{{EntityID: "first", TenantID: "1", Capabilities: []string{cap}}, {EntityID: "chosen", TenantID: "1", Capabilities: []string{cap}}, {EntityID: "other-tenant", TenantID: "2", Capabilities: []string{cap}}}
	binder := application.Binder{ApplicationID: "example", PluginInstanceID: "a", TenantID: "1"}
	wrap := func(raw string) string {
		b, _ := json.Marshal(map[string]string{appBindingsKey: raw})
		return string(b)
	}
	got, err := selectApplicationBindings(binder, reqs, candidates, wrap(`[{"requirement_id":"source","entity_id":"chosen"}]`))
	if err != nil || len(got.Bindings) != 1 || got.Bindings[0].EntityID != "chosen" {
		t.Fatalf("selection=%+v err=%v", got, err)
	}
	deviceScoped, err := selectApplicationBindings(binder, reqs, []application.Candidate{
		{EntityID: "chosen", DeviceID: "edge/board-1", TenantID: "1", Capabilities: []string{cap}},
		{EntityID: "chosen", DeviceID: "edge/board-2", TenantID: "1", Capabilities: []string{cap}},
	}, wrap(`[{"requirement_id":"source","entity_id":"chosen","device_id":"edge/board-2"}]`))
	if err != nil || len(deviceScoped.Bindings) != 1 || deviceScoped.Bindings[0].DeviceID != "edge/board-2" {
		t.Fatalf("device-scoped selection=%+v err=%v", deviceScoped, err)
	}
	auto, err := selectApplicationBindings(binder, reqs, candidates, "{}")
	if err != nil || auto.Bindings[0].EntityID != "first" {
		t.Fatalf("legacy match=%+v err=%v", auto, err)
	}
	for _, raw := range []string{"", "null", "[]", "{}", `[{"requirement_id":"source","entity_id":"other-tenant"}]`, `[{"requirement_id":"source","entity_id":"missing"}]`, `[{"requirement_id":"source","entity_id":"chosen","unknown":true}]`, `[{"requirement_id":"source","entity_id":"chosen"}] []`} {
		if _, err := selectApplicationBindings(binder, reqs, candidates, wrap(raw)); err == nil {
			t.Errorf("invalid explicit binding fell back: %q", raw)
		}
	}
}

func TestApplicationCandidatesUseActualTenantOwnership(t *testing.T) {
	srv, _ := setup(t)
	other, err := srv.cfg.Store.CreateTenant("second", "Second")
	if err != nil {
		t.Fatal(err)
	}
	srv.mu.Lock()
	srv.descriptors["e1/a"] = model.Descriptor{Entities: []model.Entity{{EntityID: "one", Capabilities: []string{"example.test/capability/input@1"}}}}
	srv.descriptors["e2/b"] = model.Descriptor{Entities: []model.Entity{{EntityID: "two", Capabilities: []string{"example.test/capability/input@1"}}}}
	srv.deviceTenants["e1/a"] = "default"
	srv.deviceTenants["e2/b"] = "second"
	srv.mu.Unlock()
	for tid, entity := range map[int64]string{1: "one", other: "two"} {
		got := srv.appCandidates(tid)
		if len(got) != 1 || got[0].EntityID != entity {
			t.Fatalf("tenant %d candidates leaked: %+v", tid, got)
		}
	}
	if got := srv.appCandidates(999999); len(got) != 0 {
		t.Fatalf("unknown tenant got candidates: %+v", got)
	}
}
