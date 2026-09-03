package plugin

import (
	"context"
	"testing"
	"time"

	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/application"
	"github.com/DeliciousBuding/cloud-path/sdk/go/transport"
)

type wired struct {
	cli application.ApplicationClient
}

func newWired(t *testing.T) *wired {
	t.Helper()
	a := New()
	serverEnd, clientEnd := transport.Pipe(16)
	srv := application.NewRPCServer(serverEnd, a)
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
	return &wired{cli: application.NewClient(clientEnd)}
}

func handshake() *application.InitializeRequest {
	return &application.InitializeRequest{
		PluginID:                  PluginID,
		PluginVersion:             pluginVersion,
		LaunchID:                  "launch-test",
		HandshakeCookie:           "cookie-test",
		ProtocolVersion:           application.ProtocolVersion,
		SupportedProtocolVersions: []uint32{1},
		NodeID:                    "node-test",
		RuntimeType:               "process",
		HostInfo:                  map[string]string{"os": "windows"},
	}
}

func TestInitializeDescribeBindHealthShutdown(t *testing.T) {
	w := newWired(t)
	ctx := context.Background()

	init, err := w.cli.Initialize(ctx, handshake())
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if init.Status == nil || !init.Status.IsOK() {
		t.Fatalf("Initialize status: %v", init.Status)
	}

	desc, err := w.cli.Describe(ctx)
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if len(desc.Requirements) != 1 || desc.Requirements[0].Capability != capabilityID {
		t.Fatalf("requirements = %+v", desc.Requirements)
	}

	bind, err := w.cli.ValidateBinding(ctx, &application.ValidateBindingRequest{
		PluginInstanceID: "inst-1",
		Bindings: []application.Binding{
			{RequirementID: requirementID, EntityID: "temp-1"},
		},
	})
	if err != nil {
		t.Fatalf("ValidateBinding: %v", err)
	}
	if !bind.Valid {
		t.Fatalf("ValidateBinding should be valid: %+v", bind.Issues)
	}

	// Empty binding set must be rejected.
	bad, err := w.cli.ValidateBinding(ctx, &application.ValidateBindingRequest{PluginInstanceID: "inst-1"})
	if err != nil {
		t.Fatalf("ValidateBinding(bad): %v", err)
	}
	if bad.Valid {
		t.Fatal("empty binding set should be invalid")
	}

	health, err := w.cli.Health(ctx)
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if health.State != application.HealthStateServing {
		t.Fatalf("health state = %v", health.State)
	}

	shutdown, err := w.cli.Shutdown(ctx, &application.ShutdownRequest{Reason: "test"})
	if err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if shutdown.Status == nil || !shutdown.Status.IsOK() {
		t.Fatalf("Shutdown status: %v", shutdown.Status)
	}

	h2, err := w.cli.Health(ctx)
	if err != nil {
		t.Fatalf("Health after shutdown: %v", err)
	}
	if h2.State != application.HealthStateNotServing {
		t.Fatalf("post-shutdown health = %v, want NOT_SERVING", h2.State)
	}
}

func TestHandleEventsEmitsSafeEffect(t *testing.T) {
	w := newWired(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := w.cli.Initialize(ctx, handshake()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	st, err := w.cli.HandleEvents(ctx)
	if err != nil {
		t.Fatalf("HandleEvents: %v", err)
	}
	defer st.Cancel(ctx)

	if err := st.Send(ctx, &application.ApplicationEvent{
		PluginInstanceID: "inst-1",
		Sequence:         1,
		SchemaVersion:    application.SchemaVersion,
		Union:            &application.CapabilityEvent{RequirementID: requirementID, EntityID: "temp-1", EventType: "observation", PayloadJSON: `{"t":21}`},
	}); err != nil {
		t.Fatalf("Send event: %v", err)
	}
	if err := st.CloseSend(ctx); err != nil {
		t.Fatalf("CloseSend: %v", err)
	}

	eff, err := st.Recv(ctx)
	if err != nil {
		t.Fatalf("Recv effect: %v", err)
	}
	upsert, ok := eff.Union.(*application.UpsertDomainRecord)
	if !ok {
		t.Fatalf("effect body = %T, want UpsertDomainRecord", eff.Union)
	}
	if upsert.RecordID != "temp-1" || upsert.RecordType != "temperature_reading" {
		t.Fatalf("unexpected upsert: %+v", upsert)
	}
	if eff.PluginInstanceID != "inst-1" {
		t.Fatalf("effect instance = %q", eff.PluginInstanceID)
	}
}

func TestHandleEventsUnknownIsIgnored(t *testing.T) {
	w := newWired(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := w.cli.Initialize(ctx, handshake()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	st, err := w.cli.HandleEvents(ctx)
	if err != nil {
		t.Fatalf("HandleEvents: %v", err)
	}
	defer st.Cancel(ctx)

	// RequestCompleted is not handled; the server must not emit an effect.
	if err := st.Send(ctx, &application.ApplicationEvent{Union: &application.RequestCompleted{RequestID: "r1"}}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := st.CloseSend(ctx); err != nil {
		t.Fatalf("CloseSend: %v", err)
	}

	// After the server consumes the event and reaches EOF it returns, so the
	// stream terminates. There must be no effect delivered for an unhandled
	// event; Recv returns EOF.
	if _, err := st.Recv(ctx); err == nil {
		t.Fatal("expected stream end with no effect for unhandled event")
	}
}
