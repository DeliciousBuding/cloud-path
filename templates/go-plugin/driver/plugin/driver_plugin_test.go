package plugin

import (
	"context"
	"testing"
	"time"

	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/driver"
	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/status"
	"github.com/DeliciousBuding/cloud-path/sdk/go/transport"
)

// wired is a DriverServer connected to a DriverClient over the in-process
// transport. The cleanup closes both ends and waits for the Serve loop.
type wired struct {
	d    *Driver
	cli  driver.DriverClient
	done chan struct{}
	ends []transport.Transport
}

func newWired(t *testing.T) *wired {
	t.Helper()
	d := New()
	serverEnd, clientEnd := transport.Pipe(16)
	srv := driver.NewRPCServer(serverEnd, d)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(context.Background())
	}()
	t.Cleanup(func() {
		_ = serverEnd.Close()
		_ = clientEnd.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Serve loop did not exit after transport close")
		}
	})
	return &wired{d: d, cli: driver.NewClient(clientEnd), done: done, ends: []transport.Transport{serverEnd, clientEnd}}
}

func handshake() *driver.InitializeRequest {
	return &driver.InitializeRequest{
		PluginID:                  PluginID,
		PluginVersion:             pluginVersion,
		LaunchID:                  "launch-test",
		HandshakeCookie:           "cookie-test",
		ProtocolVersion:           driver.ProtocolVersion,
		SupportedProtocolVersions: []uint32{1},
		NodeID:                    "node-test",
		RuntimeType:               "process",
		HostInfo:                  map[string]string{"os": "windows"},
	}
}

func TestInitializeDescribeExecuteHealthShutdown(t *testing.T) {
	w := newWired(t)
	ctx := context.Background()

	init, err := w.cli.Initialize(ctx, handshake())
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if init.Status == nil || !init.Status.IsOK() {
		t.Fatalf("Initialize status: %v", init.Status)
	}
	if init.NegotiatedProtocolVersion != 1 {
		t.Fatalf("negotiated = %d, want 1", init.NegotiatedProtocolVersion)
	}

	desc, err := w.cli.Describe(ctx)
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if len(desc.Capabilities) != 1 {
		t.Fatalf("capabilities = %d, want 1", len(desc.Capabilities))
	}
	if desc.Capabilities[0].ID != capabilityID {
		t.Fatalf("capability id = %q", desc.Capabilities[0].ID)
	}

	execResp, err := w.cli.Execute(ctx, &driver.ExecuteRequest{
		PluginInstanceID: "inst-1",
		IdempotencyKey:   "key-1",
		Action:           "read",
		EntityID:         sensorEntityID,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if execResp.State != driver.CommandStateSucceeded {
		t.Fatalf("Execute state = %v", execResp.State)
	}

	// Unknown action must be rejected.
	bad, err := w.cli.Execute(ctx, &driver.ExecuteRequest{PluginInstanceID: "inst-1", IdempotencyKey: "key-2", Action: "rm-rf"})
	if err != nil {
		t.Fatalf("Execute(bad): %v", err)
	}
	if bad.Status == nil || bad.Status.Code != status.CodeInvalidArgument {
		t.Fatalf("bad action status = %v", bad.Status)
	}

	health, err := w.cli.Health(ctx)
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if health.State != driver.HealthStateServing {
		t.Fatalf("health state = %v", health.State)
	}

	shutdown, err := w.cli.Shutdown(ctx, &driver.ShutdownRequest{Reason: "test"})
	if err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if shutdown.Status == nil || !shutdown.Status.IsOK() {
		t.Fatalf("Shutdown status: %v", shutdown.Status)
	}

	// After shutdown, Health must become NOT_SERVING.
	h2, err := w.cli.Health(ctx)
	if err != nil {
		t.Fatalf("Health after shutdown: %v", err)
	}
	if h2.State != driver.HealthStateNotServing {
		t.Fatalf("post-shutdown health = %v, want NOT_SERVING", h2.State)
	}
}

func TestWatchPublishesObservation(t *testing.T) {
	w := newWired(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := w.cli.Initialize(ctx, handshake()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	st, err := w.cli.Watch(ctx, &driver.WatchRequest{PluginInstanceID: "inst-1"})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	defer st.Cancel(ctx)

	gotObservation := false
	for i := 0; i < 6 && !gotObservation; i++ {
		msg, err := st.Recv(ctx)
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		if obs, ok := msg.Union.(*driver.Observation); ok {
			gotObservation = true
			if obs.Capability != capabilityID || obs.Property != "temperature" {
				t.Fatalf("unexpected observation %+v", obs)
			}
			if obs.Value.Kind != driver.ValueNumber {
				t.Fatalf("observation value kind = %v", obs.Value.Kind)
			}
		}
	}
	if !gotObservation {
		t.Fatal("no Observation received on Watch stream")
	}
}
