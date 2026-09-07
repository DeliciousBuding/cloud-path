package edge

import (
	"encoding/json"
	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/sdk/go/model"
	"testing"
	"time"
)

type sampledFakeDevice struct {
	fakeDevice
	descriptor model.Descriptor
}

func (d *sampledFakeDevice) Descriptor() model.Descriptor { return d.descriptor }

func TestStateCarriesFreshSamplesWithoutChangingValues(t *testing.T) {
	at := time.Now().Add(-time.Second).UTC()
	desc := semanticTestDescriptor("same value", at)
	dev := &sampledFakeDevice{fakeDevice: fakeDevice{id: "d1", done: make(chan struct{})}, descriptor: desc}
	sup := &supervisor{dcfg: DeviceCfg{ID: "d1"}, dev: dev}
	client := &wsClient{online: true, send: make(chan []byte, 4)}
	edge := &Edge{cfg: &Config{ReportIntervalS: 30}, client: client}
	read := func() api.StateData {
		t.Helper()
		var env api.Envelope
		if err := json.Unmarshal(<-client.send, &env); err != nil {
			t.Fatal(err)
		}
		var state api.StateData
		if err := json.Unmarshal(env.Data, &state); err != nil {
			t.Fatal(err)
		}
		return state
	}
	edge.reportState("e1/d1", sup, true)
	first := read()
	if len(first.Observations) != 1 || first.Observations[0].EntityID != "clock" || first.Raw["ok"] != true {
		t.Fatalf("typed state=%+v", first)
	}
	old := dev.descriptor.Entities[0].Observations["time"]
	newer := old
	newer.ObservedAt = at.Add(time.Second)
	dev.descriptor.Entities[0].Observations["time"] = newer
	edge.reportState("e1/d1", sup, true)
	second := read().Observations[0].Observations["time"]
	if second.Value != "same value" || !second.ObservedAt.Equal(newer.ObservedAt) || second.ReceivedAt.IsZero() {
		t.Fatalf("new sample missing: %+v", second)
	}
	if !dev.descriptor.Entities[0].Observations["time"].ReceivedAt.Equal(old.ReceivedAt) {
		t.Fatal("snapshot mutated provider timestamps")
	}
	// Legacy devices keep the existing raw state and make no synthetic samples.
	sup.dev = &fakeDevice{id: "legacy", done: make(chan struct{})}
	sup.adapter = &lifecycleFake{name: "legacy"}
	edge.reportState("e1/d1", sup, true)
	if got := read(); len(got.Observations) != 0 || got.Raw["ok"] != true {
		t.Fatalf("legacy state changed: %+v", got)
	}
}
