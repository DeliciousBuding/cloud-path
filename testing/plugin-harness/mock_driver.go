// Package pluginharness contains the conformance runner plus its mock Driver
// and mock Core. Everything runs over the in-process transport
// (sdk/go/transport) so the harness exercises the real handwritten wire
// encoding without needing gRPC or a serial port.
package pluginharness

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/driver"
	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/status"
)

// ExecOutcome controls what a delayed command does when its timer fires.
type ExecOutcome int

const (
	// ExecSucceed completes with SUCCEEDED unless the deadline already passed.
	ExecSucceed ExecOutcome = iota
	// ExecTimeout completes with TIMED_OUT even inside the deadline.
	ExecTimeout
)

// MockDriver is a deterministic in-memory DriverServer for conformance
// testing. It publishes all six DriverMessage variants to matching Watch
// sessions and implements idempotent Execute plus cancellation.
type MockDriver struct {
	mu          sync.Mutex
	desc        driver.DriverDescriptor
	negotiated  uint32
	initialized bool
	replay      bool
	shutdown    bool
	watchErr    error

	watchBuf  int
	nextWatch uint64
	watchers  map[uint64]*mockWatcher
	history   []*driver.DriverMessage
	seq       map[string]uint64

	commands     map[string]*mockCommand // by idempotency key
	commandsByID map[string]*mockCommand
	cancels      map[string]*driver.ExecuteResponse
	entityDev    map[string]string // entity_id -> device_id

	execDelay   time.Duration
	execOutcome ExecOutcome
	nextCmd     uint64

	// pending is drained by the single pump goroutine, which fans frames out
	// to watchers without holding mu. Blocking on a full watcher queue
	// therefore back-pressures publishers but never blocks Core-side reads.
	pending chan *driver.DriverMessage
}

type mockWatcher struct {
	id       uint64
	instance string
	devices  map[string]bool // nil = all devices
	ch       chan *driver.DriverMessage
}

type mockCommand struct {
	commandID string
	key       string
	entity    string
	action    string
	deviceID  string
	state     driver.CommandState
	deadline  time.Time
	cancel    chan struct{}
}

// MockDriverOption tunes a MockDriver.
type MockDriverOption func(*MockDriver)

// WithReplay enables Watch replay from resume_from_sequence.
func WithReplay(enabled bool) MockDriverOption {
	return func(d *MockDriver) { d.replay = enabled }
}

// WithWatchBuffer sets the per-session publish buffer used in backpressure
// tests.
func WithWatchBuffer(n int) MockDriverOption {
	return func(d *MockDriver) { d.watchBuf = n }
}

// WithExecDelay makes Execute return ACCEPTED and complete after delay.
func WithExecDelay(d time.Duration, outcome ExecOutcome) MockDriverOption {
	return func(m *MockDriver) { m.execDelay = d; m.execOutcome = outcome }
}

// WithWatchError makes Watch fail immediately (crash/fault injection).
func WithWatchError(err error) MockDriverOption {
	return func(m *MockDriver) { m.watchErr = err }
}

// NewMockDriver returns a configured mock Driver with a stable descriptor.
func NewMockDriver(opts ...MockDriverOption) *MockDriver {
	d := &MockDriver{
		desc: driver.DriverDescriptor{
			DriverID:       "mock-driver",
			Version:        "1",
			SchemaVersions: []string{driver.SchemaVersion},
			Capabilities: []driver.CapabilityDescriptor{
				{
					ID:    "cloudpath.dev/capability/temperature@1",
					Title: "Temperature",
					Properties: []driver.PropertyDescriptor{
						{Name: "value", Type: "number", Unit: "Cel", Access: "read", Quality: []string{"good", "uncertain", "bad", "unavailable"}},
					},
				},
			},
		},
		watchBuf:     4,
		watchers:     make(map[uint64]*mockWatcher),
		seq:          make(map[string]uint64),
		commands:     make(map[string]*mockCommand),
		commandsByID: make(map[string]*mockCommand),
		cancels:      make(map[string]*driver.ExecuteResponse),
		entityDev:    make(map[string]string),
		execOutcome:  ExecSucceed,
		pending:      make(chan *driver.DriverMessage, 64),
	}
	for _, o := range opts {
		o(d)
	}
	go d.pump()
	return d
}

// pump is the single ordered fan-out loop.
func (d *MockDriver) pump() {
	for msg := range d.pending {
		d.mu.Lock()
		targets := make([]*mockWatcher, 0, len(d.watchers))
		for _, w := range d.watchers {
			if w.instance == msg.PluginInstanceID && w.match(msg.DeviceID) {
				targets = append(targets, w)
			}
		}
		d.mu.Unlock()
		for _, w := range targets {
			w.ch <- msg
		}
	}
}

// Descriptor returns the stable descriptor (for expectations).
func (d *MockDriver) Descriptor() driver.DriverDescriptor {
	d.mu.Lock()
	defer d.mu.Unlock()
	return cloneDescriptor(d.desc)
}

func cloneDescriptor(in driver.DriverDescriptor) driver.DriverDescriptor {
	out := in
	out.SchemaVersions = append([]string(nil), in.SchemaVersions...)
	out.Capabilities = append([]driver.CapabilityDescriptor(nil), in.Capabilities...)
	for i := range out.Capabilities {
		c := &out.Capabilities[i]
		c.Properties = append([]driver.PropertyDescriptor(nil), c.Properties...)
		c.Events = append([]driver.EventDescriptor(nil), c.Events...)
		c.Actions = append([]driver.ActionDescriptor(nil), c.Actions...)
		for j := range c.Properties {
			c.Properties[j].Quality = append([]string(nil), c.Properties[j].Quality...)
		}
	}
	return out
}

// WatcherCount returns the number of currently registered Watch sessions.
func (d *MockDriver) WatcherCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.watchers)
}

// NegotiatedVersion reports the version chosen during Initialize (0 before).
func (d *MockDriver) NegotiatedVersion() uint32 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.negotiated
}

// ShutdownNow marks the mock as stopped: Initialize fails and Watch streams
// are refused at the application layer (separate from transport crash).
func (d *MockDriver) ShutdownNow() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.shutdown = true
}

func (d *MockDriver) unavailable() *status.Status {
	return status.Errorf(status.CodeUnavailable, "mock driver shut down")
}

func (d *MockDriver) Initialize(ctx context.Context, req *driver.InitializeRequest) (*driver.InitializeResponse, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.shutdown {
		return nil, d.unavailable()
	}
	v, ok := driver.NegotiateProtocolVersion(req.SupportedProtocolVersions, req.ProtocolVersion, 1, driver.ProtocolVersion)
	if !ok {
		return &driver.InitializeResponse{
			NegotiatedProtocolVersion: 0,
			Status:                    status.Errorf(status.CodeFailedPrecondition, "no common protocol version"),
		}, nil
	}
	d.initialized = true
	d.negotiated = v
	return &driver.InitializeResponse{
		NegotiatedProtocolVersion: v,
		Status:                    status.New(),
		ReplaySupported:           d.replay,
		RuntimeID:                 "mock-driver-runtime",
	}, nil
}

func (d *MockDriver) Describe(ctx context.Context) (*driver.DriverDescriptor, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.shutdown {
		return nil, d.unavailable()
	}
	if !d.initialized {
		return nil, status.Errorf(status.CodeFailedPrecondition, "Initialize required before Describe")
	}
	desc := cloneDescriptor(d.desc)
	return &desc, nil
}

func (d *MockDriver) ConfigureInstance(ctx context.Context, req *driver.ConfigureInstanceRequest) (*driver.ConfigureInstanceResponse, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.shutdown {
		return nil, d.unavailable()
	}
	return &driver.ConfigureInstanceResponse{
		PluginInstanceID: req.PluginInstanceID,
		AppliedRevision:  req.ConfigRevision,
		Status:           status.New(),
	}, nil
}

func (d *MockDriver) Discover(ctx context.Context, req *driver.DiscoverRequest, stream driver.DiscoveryWriter) error {
	d.mu.Lock()
	if d.shutdown {
		d.mu.Unlock()
		return d.unavailable()
	}
	seq := d.nextLocked(req.PluginInstanceID, "")
	d.mu.Unlock()

	send := func(e *driver.DiscoveryEvent) error {
		e.PluginInstanceID = req.PluginInstanceID
		e.DiscoveryID = req.DiscoveryID
		e.SchemaVersion = driver.SchemaVersion
		return stream.Send(ctx, e)
	}
	if err := send(&driver.DiscoveryEvent{Sequence: seq, Union: &driver.DiscoveryStarted{DiscoveryID: req.DiscoveryID}}); err != nil {
		return err
	}
	if err := send(&driver.DiscoveryEvent{Sequence: seq + 1, Union: &driver.DiscoveryFinished{FoundCount: 0}}); err != nil {
		return err
	}
	return nil
}

func (d *MockDriver) OpenDevice(ctx context.Context, req *driver.OpenDeviceRequest) (*driver.OpenDeviceResponse, error) {
	return &driver.OpenDeviceResponse{
		PluginInstanceID: req.PluginInstanceID,
		DeviceID:         req.DeviceID,
		Status:           status.New(),
	}, nil
}

func (d *MockDriver) CloseDevice(ctx context.Context, req *driver.CloseDeviceRequest) (*driver.CloseDeviceResponse, error) {
	return &driver.CloseDeviceResponse{
		PluginInstanceID: req.PluginInstanceID,
		DeviceID:         req.DeviceID,
		Status:           status.New(),
	}, nil
}

func (d *MockDriver) Health(ctx context.Context) (*driver.HealthResponse, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.shutdown {
		return &driver.HealthResponse{State: driver.HealthStateNotServing}, nil
	}
	return &driver.HealthResponse{State: driver.HealthStateServing}, nil
}

func (d *MockDriver) Shutdown(ctx context.Context, req *driver.ShutdownRequest) (*driver.ShutdownResponse, error) {
	d.mu.Lock()
	d.shutdown = true
	d.mu.Unlock()
	return &driver.ShutdownResponse{Status: status.New()}, nil
}

func (d *MockDriver) Execute(ctx context.Context, req *driver.ExecuteRequest) (*driver.ExecuteResponse, error) {
	d.mu.Lock()
	if d.shutdown {
		d.mu.Unlock()
		return nil, d.unavailable()
	}

	// Cancellation first: it has its own idempotency domain.
	if req.CancelCommandID != "" {
		resp, ok := d.cancels[req.IdempotencyKey]
		if ok {
			d.mu.Unlock()
			return cloneExecuteResponse(resp, true), nil
		}
		cmd := d.commandsByID[req.CancelCommandID]
		if cmd == nil {
			d.mu.Unlock()
			return &driver.ExecuteResponse{
				IdempotencyKey: req.IdempotencyKey,
				Status:         status.Errorf(status.CodeNotFound, "command %s not found", req.CancelCommandID),
			}, nil
		}
		out := &driver.ExecuteResponse{
			CommandID:      cmd.commandID,
			IdempotencyKey: req.IdempotencyKey,
			Status:         status.New(),
			State:          driver.CommandStateCancelled,
		}
		if cmd.state >= driver.CommandStateSucceeded {
			out.State = cmd.state
			out.Replay = true
		} else {
			cmd.state = driver.CommandStateCancelled
			select {
			case <-cmd.cancel:
			default:
				close(cmd.cancel)
			}
			d.cancels[req.IdempotencyKey] = out
			msg := d.commandProgressLocked(cmd)
			d.stageLocked(msg)
			d.mu.Unlock()
			d.pending <- msg
			return out, nil
		}
		d.mu.Unlock()
		return out, nil
	}

	if req.IdempotencyKey == "" {
		d.mu.Unlock()
		return &driver.ExecuteResponse{
			Status: status.Errorf(status.CodeInvalidArgument, "idempotency_key is required"),
		}, nil
	}
	if req.EntityID == "" || req.Action == "" {
		d.mu.Unlock()
		return &driver.ExecuteResponse{
			IdempotencyKey: req.IdempotencyKey,
			Status:         status.Errorf(status.CodeInvalidArgument, "entity_id and action are required"),
		}, nil
	}
	if req.Deadline == "" {
		d.mu.Unlock()
		return &driver.ExecuteResponse{
			IdempotencyKey: req.IdempotencyKey,
			Status:         status.Errorf(status.CodeInvalidArgument, "deadline is required"),
		}, nil
	}
	deadline, err := time.Parse(time.RFC3339, req.Deadline)
	if err != nil {
		d.mu.Unlock()
		return &driver.ExecuteResponse{
			IdempotencyKey: req.IdempotencyKey,
			Status:         status.Errorf(status.CodeInvalidArgument, "deadline is not RFC3339: %v", err),
		}, nil
	}
	if !deadline.After(time.Now()) {
		d.mu.Unlock()
		return &driver.ExecuteResponse{
			IdempotencyKey: req.IdempotencyKey,
			Status:         status.Errorf(status.CodeDeadlineExceeded, "deadline already passed"),
		}, nil
	}

	// Idempotency cache: return the original outcome without re-running.
	if cached, ok := d.commands[req.IdempotencyKey]; ok {
		out := executeResponseFor(cached, true)
		d.mu.Unlock()
		return out, nil
	}

	d.nextCmd++
	cmd := &mockCommand{
		commandID: fmt.Sprintf("cmd-%d", d.nextCmd),
		key:       req.IdempotencyKey,
		entity:    req.EntityID,
		action:    req.Action,
		deviceID:  d.entityDev[req.EntityID],
		state:     driver.CommandStateAccepted,
		deadline:  deadline,
		cancel:    make(chan struct{}),
	}
	d.commands[req.IdempotencyKey] = cmd
	d.commandsByID[cmd.commandID] = cmd
	accepted := d.commandProgressLocked(cmd)
	d.stageLocked(accepted)
	delay := d.execDelay
	outcome := d.execOutcome
	d.mu.Unlock()

	d.pending <- accepted
	out := executeResponseFor(cmd, false)
	// Long tasks run outside the call and report through CommandProgress.
	go d.runCommand(ctx, cmd, delay, outcome)
	return out, nil
}

// runCommand waits for the timer/deadline/cancel and reports a terminal state.
func (d *MockDriver) runCommand(ctx context.Context, cmd *mockCommand, delay time.Duration, outcome ExecOutcome) {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-cmd.cancel:
		return // cancellation already published CANCELLED
	case <-ctx.Done():
		return
	}

	d.mu.Lock()
	if cmd.state >= driver.CommandStateSucceeded {
		d.mu.Unlock()
		return
	}
	switch outcome {
	case ExecTimeout:
		cmd.state = driver.CommandStateTimedOut
	default:
		if cmd.state == driver.CommandStateCancelled {
			d.mu.Unlock()
			return
		}
		if !cmd.deadline.After(time.Now()) {
			cmd.state = driver.CommandStateTimedOut
		} else {
			cmd.state = driver.CommandStateSucceeded
		}
	}
	terminal := d.commandProgressLocked(cmd)
	d.stageLocked(terminal)
	d.mu.Unlock()
	d.pending <- terminal
}

// commandProgressLocked builds a CommandProgress for cmd. Caller holds d.mu.
func (d *MockDriver) commandProgressLocked(cmd *mockCommand) *driver.DriverMessage {
	cp := &driver.CommandProgress{
		CommandID:      cmd.commandID,
		IdempotencyKey: cmd.key,
		EntityID:       cmd.entity,
		Action:         cmd.action,
		State:          cmd.state,
		Progress:       1,
	}
	if cmd.state == driver.CommandStateRunning {
		cp.Progress = 0.5
	}
	switch cmd.state {
	case driver.CommandStateSucceeded:
		cp.ResultJSON = `{"ok":true}`
	case driver.CommandStateTimedOut:
		cp.ErrorCode = "TIMED_OUT"
	case driver.CommandStateCancelled:
		cp.ErrorCode = "CANCELLED"
	}
	return &driver.DriverMessage{
		PluginInstanceID: "mock-instance",
		DeviceID:         cmd.deviceID,
		SchemaVersion:    driver.SchemaVersion,
		Union:            cp,
	}
}

func executeResponseFor(cmd *mockCommand, replay bool) *driver.ExecuteResponse {
	return &driver.ExecuteResponse{
		CommandID:        cmd.commandID,
		IdempotencyKey:   cmd.key,
		Status:           status.New(),
		State:            cmd.state,
		Replay:           replay,
		AcceptedDeadline: cmd.deadline.UTC().Format(time.RFC3339),
	}
}

func cloneExecuteResponse(in *driver.ExecuteResponse, replay bool) *driver.ExecuteResponse {
	out := *in
	out.Replay = replay
	if in.Status != nil {
		s := *in.Status
		out.Status = &s
	}
	return &out
}

// ---------------------------------------------------------------------------
// Watch plumbing
// ---------------------------------------------------------------------------

func (d *MockDriver) Watch(ctx context.Context, req *driver.WatchRequest, stream driver.DriverMessageWriter) error {
	d.mu.Lock()
	if d.shutdown {
		d.mu.Unlock()
		return d.unavailable()
	}
	if d.watchErr != nil {
		err := d.watchErr
		d.mu.Unlock()
		return err
	}
	if req.ResumeFromSequence > 0 && !d.replay {
		d.mu.Unlock()
		return status.Errorf(status.CodeFailedPrecondition, "replay is not supported by this plugin")
	}
	d.nextWatch++
	w := &mockWatcher{
		id:       d.nextWatch,
		instance: req.PluginInstanceID,
		devices:  stringSet(req.DeviceIDs),
		ch:       make(chan *driver.DriverMessage, d.watchBuf),
	}
	d.watchers[w.id] = w
	var replay []*driver.DriverMessage
	if req.ResumeFromSequence > 0 {
		for _, m := range d.history {
			if m.PluginInstanceID == req.PluginInstanceID && m.Sequence > req.ResumeFromSequence && w.match(m.DeviceID) {
				replay = append(replay, m)
			}
		}
		sort.SliceStable(replay, func(i, j int) bool { return replay[i].Sequence < replay[j].Sequence })
	}
	d.mu.Unlock()

	defer d.removeWatcher(w)
	for _, m := range replay {
		if err := stream.Send(ctx, m); err != nil {
			return err
		}
	}
	for {
		select {
		case m := <-w.ch:
			if err := stream.Send(ctx, m); err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (d *MockDriver) removeWatcher(w *mockWatcher) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.watchers, w.id)
}

func (m *mockWatcher) match(deviceID string) bool {
	return m.devices == nil || m.devices[deviceID]
}

func stringSet(xs []string) map[string]bool {
	if len(xs) == 0 {
		return nil
	}
	out := make(map[string]bool, len(xs))
	for _, x := range xs {
		out[x] = true
	}
	return out
}

func (d *MockDriver) nextLocked(instanceID, deviceID string) uint64 {
	key := instanceID + "\x00" + deviceID
	next := d.seq[key] + 1
	d.seq[key] = next
	return next
}

// stageLocked assigns the next (instance, device) sequence, records the
// message for replay and appends it to history. Caller holds d.mu; the frame
// must then be handed to emit so fan-out happens outside the lock.
func (d *MockDriver) stageLocked(msg *driver.DriverMessage) {
	if msg.Sequence == 0 {
		msg.Sequence = d.nextLocked(msg.PluginInstanceID, msg.DeviceID)
	}
	if msg.SchemaVersion == "" {
		msg.SchemaVersion = driver.SchemaVersion
	}
	d.recordEntityLocked(msg.Union)
	d.history = append(d.history, msg)
}

// emit enqueues a staged frame for fan-out. It blocks when the pump is
// stalled behind a full watcher queue, propagating backpressure to the
// publisher without blocking Core-side reads.
func (d *MockDriver) emit(msg *driver.DriverMessage) {
	d.pending <- msg
}

// Publish stages and emits one DriverMessage with a fresh sequence and
// returns it.
func (d *MockDriver) Publish(instanceID, deviceID string, body driver.DriverMessageUnion) *driver.DriverMessage {
	d.mu.Lock()
	msg := &driver.DriverMessage{
		PluginInstanceID: instanceID,
		Sequence:         d.nextLocked(instanceID, deviceID),
		SchemaVersion:    driver.SchemaVersion,
		DeviceID:         deviceID,
		Union:            body,
	}
	d.recordEntityLocked(body)
	d.history = append(d.history, msg)
	d.mu.Unlock()
	d.emit(msg)
	return msg
}

func (d *MockDriver) recordEntityLocked(body driver.DriverMessageUnion) {
	eu, ok := body.(*driver.EntityUpsert)
	if ok && eu.Entity.DeviceID != "" {
		d.entityDev[eu.Entity.EntityID] = eu.Entity.DeviceID
	}
}

// History returns a copy of all published messages (for replay assertions).
func (d *MockDriver) History() []*driver.DriverMessage {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]*driver.DriverMessage(nil), d.history...)
}
