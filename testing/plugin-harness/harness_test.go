package pluginharness

import (
	"context"
	"testing"
	"time"

	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/driver"
)

// TestConformanceSuite runs every required conformance case:
// handshake/version negotiation, Describe stability, duplicate/out-of-order
// dedup, backpressure, command idempotency/timeout/cancellation and mock
// crash exit.
func TestConformanceSuite(t *testing.T) {
	(Suite{}).Run(t)
}

// TestHandshakeVersion focuses on the handshake/version-negotiation case.
func TestHandshakeVersion(t *testing.T) {
	CaseHandshakeVersion(t)
}

// TestExecuteIdempotency focuses on the Execute idempotency case.
func TestExecuteIdempotency(t *testing.T) {
	CaseCommandIdempotency(t)
}

// TestWatchReplay exercises resume-from-sequence for plugins that declare
// replay support.
func TestWatchReplay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h := NewHarness(t, []MockDriverOption{WithReplay(true)}, nil)
	defer h.Close()
	h.Initialize(ctx)

	// Publish before any Watch exists so replay is the only delivery path.
	h.driver.Publish(instanceID, "dev-r", &driver.DeviceUpsert{
		Device: driver.Device{DeviceID: "dev-r", ExternalID: "ext-r", Status: driver.DeviceStatusOnline},
	})
	h.driver.Publish(instanceID, "dev-r", &driver.Observation{
		EntityID: "sensor-r", Value: driver.Value{Kind: driver.ValueNumber, NumberValue: 1},
	})

	if err := h.StartWatch(ctx, &driver.WatchRequest{PluginInstanceID: instanceID, ResumeFromSequence: 1}); err != nil {
		t.Fatalf("StartWatch: %v", err)
	}
	defer h.core.StopWatch()

	// Replay delivers the observation (sequence 2), not the device upsert.
	if m, ok := h.core.WaitForMessage(3*time.Second, func(m *driver.DriverMessage) bool {
		return m.DeviceID == "dev-r" && m.Sequence == 2
	}); !ok {
		t.Fatal("replayed observation missing")
	} else if _, isObs := m.Union.(*driver.Observation); !isObs {
		t.Fatalf("replayed body type = %T, want Observation", m.Union)
	}
}
