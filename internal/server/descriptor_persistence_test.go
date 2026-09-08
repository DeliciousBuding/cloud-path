package server

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/DeliciousBuding/cloud-path/internal/application"
	"github.com/DeliciousBuding/cloud-path/internal/model"
	"github.com/DeliciousBuding/cloud-path/internal/store"
)

func TestHydrateRestoresDescriptorForOfflineBinding(t *testing.T) {
	const cap = "example.test/capability/input@1"
	path := filepath.Join(t.TempDir(), "descriptor.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	tenantID, err := st.EnsureDefaultTenant()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertDeviceTenant("e1/d1", "e1", "demo", "节点1", "COM3", tenantID); err != nil {
		t.Fatal(err)
	}
	desc := model.Descriptor{
		DeviceID: "e1/d1", ExternalID: "d1", Status: model.DeviceOffline,
		Entities: []model.Entity{{
			EntityID: "input", UniqueKey: "input", Category: model.EntitySensor,
			Capabilities: []string{cap},
		}},
	}
	raw, err := json.Marshal(desc)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetDeviceDescriptor("e1/d1", string(raw)); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertDeviceTenant("e1/bad", "e1", "demo", "bad", "", tenantID); err != nil {
		t.Fatal(err)
	}
	if err := st.SetDeviceDescriptor("e1/bad", `{"device_id":"e1/bad","external_id":"bad","status":"offline","entities":null}`); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st2, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	srv := New(Config{Store: st2, Version: "test"})

	srv.mu.RLock()
	dev := srv.devices["e1/d1"]
	got, restored := srv.descriptors["e1/d1"]
	_, badRestored := srv.descriptors["e1/bad"]
	srv.mu.RUnlock()
	if dev == nil || dev.Online {
		t.Fatalf("hydrated device = %+v, want offline", dev)
	}
	if !restored || got.DeviceID != "e1/d1" || got.ExternalID != "d1" || len(got.Entities) != 1 {
		t.Fatalf("hydrated descriptor = %+v restored=%v", got, restored)
	}
	if badRestored {
		t.Fatal("invalid descriptor must not be hydrated")
	}

	candidates := srv.appCandidates(tenantID)
	bindings, err := selectApplicationBindings(
		application.Binder{ApplicationID: "test-app", PluginInstanceID: "instance-1", TenantID: strconv.FormatInt(tenantID, 10)},
		[]application.Requirement{{ID: "input", Capability: cap, Cardinality: application.CardinalityOne}},
		candidates, "{}",
	)
	if err != nil {
		t.Fatalf("offline binding failed: %v (candidates=%+v)", err, candidates)
	}
	if len(bindings.Bindings) != 1 || bindings.Bindings[0].EntityID != "input" {
		t.Fatalf("bindings = %+v", bindings.Bindings)
	}
}
