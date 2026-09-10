package server

import (
	"strings"
	"testing"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/model"
)

func TestDeviceKeyForEntityUsesUniqueOnlineProvider(t *testing.T) {
	srv, _ := setup(t)
	const entityID = "route-test-entity"
	srv.mu.Lock()
	srv.descriptors["edge-a/device-a"] = model.Descriptor{Entities: []model.Entity{{EntityID: entityID}}}
	srv.descriptors["edge-b/device-b"] = model.Descriptor{Entities: []model.Entity{{EntityID: entityID}}}
	srv.devices["edge-a/device-a"] = &api.DeviceView{ID: "edge-a/device-a", Online: false}
	srv.devices["edge-b/device-b"] = &api.DeviceView{ID: "edge-b/device-b", Online: true}
	srv.mu.Unlock()

	got, err := srv.deviceKeyForEntity(entityID)
	if err != nil || got != "edge-b/device-b" {
		t.Fatalf("offline candidate selected: key=%q err=%v", got, err)
	}

	srv.mu.Lock()
	srv.devices["edge-a/device-a"].Online = true
	srv.mu.Unlock()
	if got, err := srv.deviceKeyForEntity(entityID); err == nil || got != "" || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("multiple online candidates must fail closed: key=%q err=%v", got, err)
	}

	srv.mu.Lock()
	srv.devices["edge-a/device-a"].Online = false
	srv.devices["edge-b/device-b"].Online = false
	srv.mu.Unlock()
	if got, err := srv.deviceKeyForEntity(entityID); err == nil || got != "" || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("offline-only entity must fail explicitly: key=%q err=%v", got, err)
	}

	srv.mu.Lock()
	delete(srv.descriptors, "edge-a/device-a")
	delete(srv.descriptors, "edge-b/device-b")
	srv.mu.Unlock()
	if got, err := srv.deviceKeyForEntity(entityID); err == nil || got != "" || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing entity must fail explicitly: key=%q err=%v", got, err)
	}
}

func TestDeviceKeyForBindingTargetsExplicitProvider(t *testing.T) {
	srv, _ := setup(t)
	const entityID = "buzzer"
	srv.mu.Lock()
	for _, key := range []string{"edge-a/device-a", "edge-b/device-b"} {
		srv.descriptors[key] = model.Descriptor{Entities: []model.Entity{{EntityID: entityID}}}
		srv.devices[key] = &api.DeviceView{ID: key, Online: true}
	}
	srv.mu.Unlock()

	got, err := srv.deviceKeyForBinding("edge-b/device-b", entityID)
	if err != nil || got != "edge-b/device-b" {
		t.Fatalf("explicit target = %q err=%v", got, err)
	}
	if got, err := srv.deviceKeyForBinding("edge-b/device-b", "missing"); err == nil || got != "" || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing target entity = %q err=%v", got, err)
	}
	if got, err := srv.deviceKeyForBinding("edge-missing/device", entityID); err == nil || got != "" || !strings.Contains(err.Error(), "no descriptor") {
		t.Fatalf("missing target device = %q err=%v", got, err)
	}

	srv.mu.Lock()
	srv.devices["edge-b/device-b"].Online = false
	srv.mu.Unlock()
	if got, err := srv.deviceKeyForBinding("edge-b/device-b", entityID); err == nil || got != "" || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("offline explicit target = %q err=%v", got, err)
	}
}
