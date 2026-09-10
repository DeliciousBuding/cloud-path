package server

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/model"
	"github.com/DeliciousBuding/cloud-path/internal/store"
	sdkapplication "github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/application"
)

func TestApplicationObservationRoutesPreserveFactsAndScope(t *testing.T) {
	srv, _ := setup(t)
	const cap = "example.test/capability/measurement@1"
	const other = "example.test/capability/other@1"
	srv.descriptors["e1/d1"] = model.Descriptor{Entities: []model.Entity{{EntityID: "own", Capabilities: []string{cap, other}}}}
	srv.descriptors["e1/d2"] = model.Descriptor{Entities: []model.Entity{{EntityID: "foreign", Capabilities: []string{cap}}}}
	h := &AppHost{srv: srv, running: map[appInstKey]*appInstanceRun{}}
	for tid, id := range map[int64]string{1: "mine", 2: "other"} {
		h.running[appInstKey{tid, id}] = &appInstanceRun{
			row: store.PluginInstanceRow{TenantID: tid, InstanceID: id},
			bindings: []api.AppBindingView{
				{RequirementID: "input", EntityID: "own", DeviceID: "e1/d1", Capability: cap},
				{RequirementID: "input", EntityID: "foreign", DeviceID: "e1/d2", Capability: cap},
			},
		}
	}
	at := time.Unix(1700000000, 0).UTC()
	obs := model.Observation{Capability: cap, Property: "value", Value: 12.5, Quality: model.QualityUncertain, ObservedAt: at, ReceivedAt: at.Add(time.Second), Sequence: 42}
	sets := []api.EntityObservationSet{{EntityID: "own", Observations: map[string]model.Observation{"value": obs}}, {EntityID: "foreign", Observations: map[string]model.Observation{"value": obs}}}
	got := h.observationDeliveries(1, "e1/d1", true, sets)
	if len(got) != 1 || got[0].route.run.row.InstanceID != "mine" || got[0].event.EventType != sdkapplication.PropertyObservedEvent {
		t.Fatalf("wrong routes: %+v", got)
	}
	var payload model.Observation
	if err := json.Unmarshal([]byte(got[0].event.PayloadJSON), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Value != 12.5 || payload.Quality != model.QualityUncertain || payload.Sequence != 42 || !payload.ObservedAt.Equal(at) {
		t.Fatalf("sample changed: %+v", payload)
	}
	offline := h.observationDeliveries(1, "e1/d1", false, sets)
	_ = json.Unmarshal([]byte(offline[0].event.PayloadJSON), &payload)
	if payload.Quality != model.QualityUnavailable {
		t.Fatalf("offline promoted: %+v", payload)
	}
	if obs.Quality != model.QualityUncertain {
		t.Fatal("original sample mutated")
	}
	for _, bad := range []model.Observation{{Capability: other, Property: "value", Value: 1}, {Capability: cap, Property: "different", Value: 1}, {Capability: cap, Property: "value", Value: nil}, {Capability: cap, Property: "value", Value: 1, Quality: "invented"}} {
		if got := h.observationDeliveries(1, "e1/d1", true, []api.EntityObservationSet{{EntityID: "own", Observations: map[string]model.Observation{"value": bad}}}); len(got) != 0 {
			t.Fatalf("invalid or unbound property delivered: %+v", bad)
		}
	}
	if got := h.observationDeliveries(1, "missing", true, sets); len(got) != 0 {
		t.Fatal("missing descriptor inferred")
	}
	if got := h.observationDeliveries(99, "e1/d1", true, sets); len(got) != 0 {
		t.Fatal("unknown tenant routed")
	}
	var disabled *AppHost
	disabled.DispatchDeviceObservations(1, "e1/d1", true, sets)
}
