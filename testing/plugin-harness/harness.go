package pluginharness

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/driver"
	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/status"
	"github.com/DeliciousBuding/cloud-path/sdk/go/transport"
)

const instanceID = "mock-instance"

// Harness wires one MockDriver and one MockCore together over an in-process
// transport and runs the protocol dispatcher. It is the standard fixture for
// conformance cases.
type Harness struct {
	t *testing.T

	driver       *MockDriver
	core         *MockCore
	coreEnd      transport.Transport
	peerEnd      transport.Transport
	serveDone    chan struct{}
	closed       bool
	pipeCapacity int
}

// HarnessOption tunes a Harness.
type HarnessOption func(*Harness)

// WithPipeCapacity sets the transport queue size (small values exercise
// backpressure).
func WithPipeCapacity(n int) HarnessOption {
	return func(h *Harness) { h.pipeCapacity = n }
}

// NewHarness starts a fresh mock stack. Call Close to tear it down.
func NewHarness(t *testing.T, driverOpts []MockDriverOption, coreOpts []MockCoreOption, opts ...HarnessOption) *Harness {
	t.Helper()
	h := &Harness{t: t, pipeCapacity: 64}
	for _, o := range opts {
		o(h)
	}
	coreEnd, peerEnd := transport.Pipe(h.pipeCapacity)
	d := NewMockDriver(driverOpts...)
	srv := driver.NewRPCServer(peerEnd, d)
	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		_ = srv.Serve(context.Background())
	}()
	h.driver = d
	h.core = NewMockCore(driver.NewClient(coreEnd), coreEnd, peerEnd, coreOpts...)
	h.coreEnd = coreEnd
	h.peerEnd = peerEnd
	h.serveDone = serveDone
	return h
}

// Driver exposes the mock Driver for direct staging of publishes.
func (h *Harness) Driver() *MockDriver { return h.driver }

// Core exposes the mock Core.
func (h *Harness) Core() *MockCore { return h.core }

// Initialize performs the handshake and requires success.
func (h *Harness) Initialize(ctx context.Context) *driver.InitializeResponse {
	h.t.Helper()
	resp, err := h.core.Initialize(ctx, coreInitializeRequest())
	if err != nil {
		h.t.Fatalf("Initialize: %v", err)
	}
	if resp.Status != nil && !resp.Status.IsOK() {
		h.t.Fatalf("Initialize status: %v", resp.Status)
	}
	return resp
}

// StartWatch opens a Watch stream and waits until the mock Driver has
// actually registered the session, removing the publish/registration race
// between the client call and the server handler.
func (h *Harness) StartWatch(ctx context.Context, req *driver.WatchRequest) error {
	h.t.Helper()
	before := h.driver.WatcherCount()
	if err := h.core.StartWatch(ctx, req); err != nil {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if h.driver.WatcherCount() > before {
			return nil
		}
		time.Sleep(2 * time.Millisecond)
	}
	return fmt.Errorf("watch session was never registered by the mock driver")
}

// Close tears the harness down and waits for the server loop to exit.
func (h *Harness) Close() {
	h.t.Helper()
	if h.closed {
		return
	}
	h.closed = true
	_ = h.coreEnd.Close()
	_ = h.peerEnd.Close()
	select {
	case <-h.serveDone:
	case <-time.After(5 * time.Second):
		h.t.Error("server Serve loop did not exit after transport close")
	}
}

// coreInitializeRequest is the canonical handshake request.
func coreInitializeRequest() *driver.InitializeRequest {
	return &driver.InitializeRequest{
		PluginID:                  "mock-driver",
		PluginVersion:             "0.1.0",
		LaunchID:                  "launch-1",
		HandshakeCookie:           "cookie-1",
		ProtocolVersion:           driver.ProtocolVersion,
		SupportedProtocolVersions: []uint32{1},
		NodeID:                    "edge-1",
		RuntimeType:               "process",
		HostInfo:                  map[string]string{"os": "windows"},
	}
}

// Case is one conformance case.
type Case struct {
	Name string
	Run  func(t *testing.T)
}

// Suite is the ordered conformance suite defined by
// docs/architecture/plugin-system.md §12: handshake/version negotiation,
// descriptor stability, duplicate/out-of-order/backpressure, command
// idempotency/timeout/cancellation, and crash exit at the mock level.
type Suite struct{}

// Cases returns the suite cases. Each case builds its own Harness so tests
// stay independent.
func (Suite) Cases() []Case {
	return []Case{
		{Name: "handshake_and_version_negotiation", Run: CaseHandshakeVersion},
		{Name: "describe_stability", Run: CaseDescribeStability},
		{Name: "duplicate_and_out_of_order", Run: CaseDuplicateOutOfOrder},
		{Name: "backpressure", Run: CaseBackpressure},
		{Name: "command_idempotency", Run: CaseCommandIdempotency},
		{Name: "command_timeout", Run: CaseCommandTimeout},
		{Name: "command_cancellation", Run: CaseCommandCancellation},
		{Name: "crash_exit", Run: CaseCrashExit},
	}
}

// Run executes every case as a subtest.
func (s Suite) Run(t *testing.T) {
	for _, c := range s.Cases() {
		t.Run(c.Name, c.Run)
	}
}

func CaseHandshakeVersion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h := NewHarness(t, nil, nil)
	defer h.Close()

	resp := h.Initialize(ctx)
	if resp.NegotiatedProtocolVersion != driver.ProtocolVersion {
		t.Fatalf("negotiated version = %d, want %d", resp.NegotiatedProtocolVersion, driver.ProtocolVersion)
	}
	if h.driver.NegotiatedVersion() != driver.ProtocolVersion {
		t.Fatalf("driver negotiated version = %d, want %d", h.driver.NegotiatedVersion(), driver.ProtocolVersion)
	}
	// Incompatible version sets must fail the negotiation without a panic.
	req := coreInitializeRequest()
	req.SupportedProtocolVersions = []uint32{99}
	req.ProtocolVersion = 99
	bad, err := h.core.Initialize(ctx, req)
	if err != nil {
		t.Fatalf("Initialize(unsupported): %v", err)
	}
	if bad.NegotiatedProtocolVersion != 0 || bad.Status == nil || bad.Status.Code != status.CodeFailedPrecondition {
		t.Fatalf("expected failed precondition, got version=%d status=%v", bad.NegotiatedProtocolVersion, bad.Status)
	}

	// Health must reflect serving state after handshake.
	health, err := h.core.Health(ctx)
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if health.State != driver.HealthStateServing {
		t.Fatalf("health state = %d, want serving", health.State)
	}
}

func CaseDescribeStability(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h := NewHarness(t, nil, nil)
	defer h.Close()

	h.Initialize(ctx)
	first, err := h.core.Describe(ctx)
	if err != nil {
		t.Fatalf("Describe #1: %v", err)
	}
	second, err := h.core.Describe(ctx)
	if err != nil {
		t.Fatalf("Describe #2: %v", err)
	}
	if !driverDescriptorEqual(first, second) {
		t.Fatalf("Describe is not stable:\nfirst:  %+v\nsecond: %+v", first, second)
	}
	if first.DriverID != "mock-driver" || len(first.SchemaVersions) == 0 {
		t.Fatalf("unexpected descriptor: %+v", first)
	}
}

func CaseDuplicateOutOfOrder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h := NewHarness(t, nil, nil)
	defer h.Close()
	h.Initialize(ctx)

	if err := h.StartWatch(ctx, &driver.WatchRequest{PluginInstanceID: instanceID}); err != nil {
		t.Fatalf("StartWatch: %v", err)
	}
	defer h.core.StopWatch()

	const dev = "dev-1"
	pub := func(body driver.DriverMessageUnion) {
		h.driver.Publish(instanceID, dev, body)
	}
	obs := func(n float64) driver.DriverMessageUnion {
		return &driver.Observation{
			EntityID:   "sensor-1",
			Capability: "cloudpath.dev/capability/temperature@1",
			Property:   "value",
			Value:      driver.Value{Kind: driver.ValueNumber, NumberValue: n},
			ObservedAt: "2026-09-03T08:00:00Z",
			Quality:    "good",
		}
	}

	pub(&driver.DeviceUpsert{Device: driver.Device{DeviceID: dev, ExternalID: "ext-1", Status: driver.DeviceStatusOnline}})
	pub(obs(1))
	pub(obs(2))
	// Duplicate: same sequence must not be re-emitted, so re-publishing with
	// an explicit stale sequence emulates a retried frame.
	stale := &driver.DriverMessage{
		PluginInstanceID: instanceID,
		Sequence:         3, // already used by obs(2) at scope (instance, dev)
		SchemaVersion:    driver.SchemaVersion,
		DeviceID:         dev,
		Union:            obs(3),
	}
	h.driver.PublishRaw(stale)
	// Out-of-order older sequence must be dropped too.
	h.driver.PublishRaw(&driver.DriverMessage{
		PluginInstanceID: instanceID,
		Sequence:         2,
		SchemaVersion:    driver.SchemaVersion,
		DeviceID:         dev,
		Union:            obs(9),
	})
	// A different device is an independent sequence scope.
	h.driver.Publish(instanceID, "dev-2", obs(1))

	wait := func(seq uint64) {
		if _, ok := h.core.WaitForMessage(3*time.Second, func(m *driver.DriverMessage) bool {
			return m.DeviceID == dev && m.Sequence == seq
		}); !ok {
			t.Fatalf("missing message seq=%d", seq)
		}
	}
	wait(3)

	// Accepted stream must contain exactly the three unique sequences for
	// dev-1 plus the first sequence of dev-2, in order.
	want := []*driver.DriverMessage{
		{PluginInstanceID: instanceID, DeviceID: dev, Sequence: 1},
		{PluginInstanceID: instanceID, DeviceID: dev, Sequence: 2},
		{PluginInstanceID: instanceID, DeviceID: dev, Sequence: 3},
		{PluginInstanceID: instanceID, DeviceID: "dev-2", Sequence: 1},
	}
	if err := h.core.ExpectAccepted(want); err != nil {
		t.Fatalf("%v", err)
	}
	if h.core.RawCount() < 5 {
		t.Fatalf("raw count = %d, want >= 5 (dup frames must still be observed and dropped)", h.core.RawCount())
	}
}

func CaseBackpressure(t *testing.T) {
	// Transport-level contract: Send must block on a full queue, not drop.
	a, b := transport.Pipe(2)
	defer a.Close()
	defer b.Close()

	const n = 20
	sent := make(chan error, 1)
	go func() {
		ctx := context.Background()
		for i := 0; i < n; i++ {
			if err := a.Send(ctx, transport.Message{CallID: uint64(i + 1), Kind: transport.KindStreamMsg}); err != nil {
				sent <- err
				return
			}
		}
		sent <- nil
	}()

	select {
	case err := <-sent:
		t.Fatalf("producer finished while consumer was idle (backpressure not applied): %v", err)
	case <-time.After(80 * time.Millisecond):
	}

	// Consume everything; frames must be complete and in order.
	for i := 0; i < n; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		m, err := b.Recv(ctx)
		cancel()
		if err != nil {
			t.Fatalf("Recv %d: %v", i+1, err)
		}
		if m.CallID != uint64(i+1) {
			t.Fatalf("frame %d out of order: got call id %d", i+1, m.CallID)
		}
	}
	if err := <-sent; err != nil {
		t.Fatalf("producer error: %v", err)
	}

	// End-to-end: a slow Core consumer must still receive every accepted
	// message exactly once.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	h := NewHarness(t, []MockDriverOption{WithWatchBuffer(2)}, []MockCoreOption{WithRecvDelay(2 * time.Millisecond)}, WithPipeCapacity(2))
	defer h.Close()
	h.Initialize(ctx)
	if err := h.StartWatch(ctx, &driver.WatchRequest{PluginInstanceID: instanceID}); err != nil {
		t.Fatalf("StartWatch: %v", err)
	}
	defer h.core.StopWatch()

	pubDone := make(chan struct{})
	go func() {
		defer close(pubDone)
		for i := 0; i < n; i++ {
			h.driver.Publish(instanceID, "dev-bp", &driver.Observation{
				EntityID: "sensor-bp",
				Value:    driver.Value{Kind: driver.ValueNumber, NumberValue: float64(i)},
			})
		}
	}()

	waitOK := false
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		if len(h.core.Accepted()) == n {
			waitOK = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	<-pubDone
	if !waitOK {
		t.Fatalf("accepted %d messages, want %d (frames were dropped)", len(h.core.Accepted()), n)
	}
}

func CaseCommandIdempotency(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h := NewHarness(t, []MockDriverOption{WithExecDelay(30*time.Millisecond, ExecSucceed)}, nil)
	defer h.Close()
	h.Initialize(ctx)

	if err := h.StartWatch(ctx, &driver.WatchRequest{PluginInstanceID: instanceID}); err != nil {
		t.Fatalf("StartWatch: %v", err)
	}
	defer h.core.StopWatch()

	// Register entity -> device mapping used by CommandProgress scoping.
	h.driver.Publish(instanceID, "dev-cmd", &driver.EntityUpsert{
		Entity: driver.Entity{EntityID: "sensor-cmd", DeviceID: "dev-cmd", UniqueKey: "u1", Name: "sensor", Capabilities: []string{"cloudpath.dev/capability/temperature@1"}},
	})

	req := &driver.ExecuteRequest{
		PluginInstanceID: instanceID,
		IdempotencyKey:   "k-1",
		EntityID:         "sensor-cmd",
		Action:           "calibrate",
		ArgsJSON:         `{"offset":1}`,
		Deadline:         time.Now().Add(5 * time.Second).UTC().Format(time.RFC3339),
		Actor:            "tenant-a/user-1",
	}
	first, err := h.core.Execute(ctx, req)
	if err != nil {
		t.Fatalf("Execute #1: %v", err)
	}
	if first.Status != nil && !first.Status.IsOK() {
		t.Fatalf("Execute #1 rejected: %v", first.Status)
	}
	if first.Replay {
		t.Fatal("first Execute must not be a replay")
	}

	second, err := h.core.Execute(ctx, req)
	if err != nil {
		t.Fatalf("Execute #2: %v", err)
	}
	if second.Status != nil && !second.Status.IsOK() {
		t.Fatalf("Execute #2 rejected: %v", second.Status)
	}
	if !second.Replay {
		t.Fatal("second Execute with the same idempotency key must be a replay")
	}
	if second.CommandID != first.CommandID {
		t.Fatalf("replay command id %q != original %q", second.CommandID, first.CommandID)
	}

	if state, ok := h.core.WaitForCommandTerminal(5*time.Second, first.CommandID); !ok {
		t.Fatal("command never reached a terminal state")
	} else if state != driver.CommandStateSucceeded {
		t.Fatalf("terminal state = %s, want SUCCEEDED", state)
	}

	// Exactly one accept + one terminal progress frame, plus entity upsert.
	progress := progressStates(h.core.Accepted())
	if progress[first.CommandID] != 2 {
		t.Fatalf("command %s produced %d progress frames, want 2 (accepted + succeeded)", first.CommandID, progress[first.CommandID])
	}
}

func CaseCommandTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h := NewHarness(t, []MockDriverOption{WithExecDelay(150*time.Millisecond, ExecSucceed)}, nil)
	defer h.Close()
	h.Initialize(ctx)
	if err := h.StartWatch(ctx, &driver.WatchRequest{PluginInstanceID: instanceID}); err != nil {
		t.Fatalf("StartWatch: %v", err)
	}
	defer h.core.StopWatch()

	h.driver.Publish(instanceID, "dev-timeout", &driver.EntityUpsert{
		Entity: driver.Entity{EntityID: "sensor-timeout", DeviceID: "dev-timeout", UniqueKey: "u2", Name: "sensor", Capabilities: []string{"cloudpath.dev/capability/temperature@1"}},
	})

	req := &driver.ExecuteRequest{
		PluginInstanceID: instanceID,
		IdempotencyKey:   "k-timeout",
		EntityID:         "sensor-timeout",
		Action:           "calibrate",
		ArgsJSON:         `{"offset":1}`,
		Deadline:         time.Now().Add(40 * time.Millisecond).UTC().Format(time.RFC3339Nano),
		Actor:            "tenant-a/user-1",
	}
	resp, err := h.core.Execute(ctx, req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if resp.Status != nil && !resp.Status.IsOK() {
		t.Fatalf("Execute rejected: %v", resp.Status)
	}
	if state, ok := h.core.WaitForCommandTerminal(5*time.Second, resp.CommandID); !ok {
		t.Fatal("command never reached a terminal state")
	} else if state != driver.CommandStateTimedOut {
		t.Fatalf("terminal state = %s, want TIMED_OUT", state)
	}

	// A deadline already in the past is rejected before dispatch.
	past := *req
	past.IdempotencyKey = "k-past"
	past.Deadline = time.Now().Add(-time.Second).UTC().Format(time.RFC3339)
	rej, err := h.core.Execute(ctx, &past)
	if err != nil {
		t.Fatalf("Execute(past deadline): %v", err)
	}
	if rej.Status == nil || rej.Status.Code != status.CodeDeadlineExceeded {
		t.Fatalf("past-deadline status = %v, want DEADLINE_EXCEEDED", rej.Status)
	}
}

func CaseCommandCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h := NewHarness(t, []MockDriverOption{WithExecDelay(500*time.Millisecond, ExecSucceed)}, nil)
	defer h.Close()
	h.Initialize(ctx)
	if err := h.StartWatch(ctx, &driver.WatchRequest{PluginInstanceID: instanceID}); err != nil {
		t.Fatalf("StartWatch: %v", err)
	}
	defer h.core.StopWatch()

	h.driver.Publish(instanceID, "dev-cancel", &driver.EntityUpsert{
		Entity: driver.Entity{EntityID: "sensor-cancel", DeviceID: "dev-cancel", UniqueKey: "u3", Name: "sensor", Capabilities: []string{"cloudpath.dev/capability/temperature@1"}},
	})

	req := &driver.ExecuteRequest{
		PluginInstanceID: instanceID,
		IdempotencyKey:   "k-cancel",
		EntityID:         "sensor-cancel",
		Action:           "calibrate",
		ArgsJSON:         `{"offset":1}`,
		Deadline:         time.Now().Add(5 * time.Second).UTC().Format(time.RFC3339),
		Actor:            "tenant-a/user-1",
	}
	resp, err := h.core.Execute(ctx, req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if resp.Status != nil && !resp.Status.IsOK() {
		t.Fatalf("Execute rejected: %v", resp.Status)
	}

	cancelReq := &driver.ExecuteRequest{
		PluginInstanceID: instanceID,
		IdempotencyKey:   "k-cancel-directive",
		CancelCommandID:  resp.CommandID,
		Deadline:         time.Now().Add(5 * time.Second).UTC().Format(time.RFC3339),
	}
	done, err := h.core.Execute(ctx, cancelReq)
	if err != nil {
		t.Fatalf("Execute(cancel): %v", err)
	}
	if done.State != driver.CommandStateCancelled {
		t.Fatalf("cancel response state = %s, want CANCELLED", done.State)
	}

	if state, ok := h.core.WaitForCommandTerminal(5*time.Second, resp.CommandID); !ok {
		t.Fatal("command never reached a terminal state")
	} else if state != driver.CommandStateCancelled {
		t.Fatalf("terminal state = %s, want CANCELLED", state)
	}

	// The cancellation directive itself is idempotent.
	again, err := h.core.Execute(ctx, cancelReq)
	if err != nil {
		t.Fatalf("Execute(cancel replay): %v", err)
	}
	if !again.Replay {
		t.Fatal("repeated cancel directive must replay")
	}
}

func CaseCrashExit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h := NewHarness(t, nil, nil)
	defer h.Close()
	h.Initialize(ctx)

	if err := h.StartWatch(ctx, &driver.WatchRequest{PluginInstanceID: instanceID}); err != nil {
		t.Fatalf("StartWatch: %v", err)
	}
	defer h.core.StopWatch()

	h.driver.Publish(instanceID, "dev-crash", &driver.DeviceUpsert{
		Device: driver.Device{DeviceID: "dev-crash", ExternalID: "ext-crash", Status: driver.DeviceStatusOnline},
	})
	if _, ok := h.core.WaitForMessage(3*time.Second, func(m *driver.DriverMessage) bool { return m.DeviceID == "dev-crash" }); !ok {
		t.Fatal("expected pre-crash message")
	}

	h.core.SimulateCrash()

	// The Watch consumer must terminate with an error (not io.EOF, not hang).
	if _, ok := waitChanTimeout(h.core.watchDone, 5*time.Second); !ok {
		t.Fatal("Watch consumer did not exit after crash")
	}
	if h.core.WatchErr() == nil {
		t.Fatal("WatchErr is nil after crash; streams must fail with an error")
	}

	// Post-crash unary calls must fail fast with UNAVAILABLE.
	_, err := h.core.Health(ctx)
	if StatusCode(err) != status.CodeUnavailable {
		t.Fatalf("post-crash Health error = %v, want UNAVAILABLE", err)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func driverDescriptorEqual(a, b *driver.DriverDescriptor) bool {
	ab, errA := json.Marshal(a)
	bb, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	return string(ab) == string(bb)
}

func progressStates(msgs []*driver.DriverMessage) map[string]int {
	out := make(map[string]int)
	for _, m := range msgs {
		if cp, ok := m.Union.(*driver.CommandProgress); ok {
			out[cp.CommandID]++
		}
	}
	return out
}

func waitChanTimeout(ch <-chan struct{}, d time.Duration) (struct{}, bool) {
	select {
	case <-ch:
		return struct{}{}, true
	case <-time.After(d):
		return struct{}{}, false
	}
}

// PublishRaw stages a pre-built frame without assigning a fresh sequence,
// used to inject duplicates and out-of-order frames.
func (d *MockDriver) PublishRaw(msg *driver.DriverMessage) {
	d.mu.Lock()
	d.recordEntityLocked(msg.Union)
	d.history = append(d.history, msg)
	d.mu.Unlock()
	d.emit(msg)
}
