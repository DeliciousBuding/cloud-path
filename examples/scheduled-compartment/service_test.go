package scheduledcompartment

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/application"
	"github.com/DeliciousBuding/cloud-path/sdk/go/transport"
)

const (
	testInstance   = "app-1"
	buzzerEntityID = "dev/buzzer"
	c1             = "dev/key-1"
	c2             = "dev/key-2"
	c3             = "dev/key-3"
)

var validConfigJSON = `{
  "timezone": "Asia/Shanghai",
  "compartments": [
    {"id":"c1","name":"Compartment 1"},
    {"id":"c2","name":"Compartment 2"},
    {"id":"c3","name":"Compartment 3"}
  ],
  "schedule": [
    {"id":"w-morning","compartment":"c1","start":"08:00","end":"08:30"}
  ]
}`

var validBindings = []application.Binding{
	{RequirementID: "reminder-output", EntityID: buzzerEntityID},
	{RequirementID: "compartments", EntityID: c1},
	{RequirementID: "compartments", EntityID: c2},
	{RequirementID: "compartments", EntityID: c3},
}

// --- in-package harness over the real Application Protocol wire ---

type testApp struct {
	t         *testing.T
	svc       *Service
	cli       application.ApplicationClient
	serverEnd transport.Transport
	clientEnd transport.Transport
	serveDone chan struct{}
	ctx       context.Context
	cancel    context.CancelFunc
	stream    application.ApplicationEventStream
	now       time.Time
}

func newTestApp(t *testing.T, now time.Time) *testApp {
	t.Helper()
	svc := New()
	a := &testApp{
		t:   t,
		svc: svc,
		now: now,
	}
	svc.now = func() time.Time { return a.now }

	serverEnd, clientEnd := transport.Pipe(256)
	rpcServer := application.NewRPCServer(serverEnd, svc)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = rpcServer.Serve(context.Background())
	}()
	cli := application.NewClient(clientEnd)
	ctx, cancel := context.WithCancel(context.Background())
	a.serverEnd = serverEnd
	a.clientEnd = clientEnd
	a.serveDone = done
	a.cli = cli
	a.ctx = ctx
	a.cancel = cancel

	if _, err := cli.Initialize(ctx, &application.InitializeRequest{
		PluginID:                  "mock-app",
		PluginVersion:             "0.1.0",
		LaunchID:                  "launch-1",
		HandshakeCookie:           "cookie-1",
		ProtocolVersion:           application.ProtocolVersion,
		SupportedProtocolVersions: []uint32{1},
		NodeID:                    "node-1",
		RuntimeType:               "process",
		HostInfo:                  map[string]string{"os": "windows"},
	}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	return a
}

func (a *testApp) configure(cfgJSON string) *application.ConfigureInstanceResponse {
	a.t.Helper()
	resp, err := a.cli.ConfigureInstance(a.ctx, &application.ConfigureInstanceRequest{
		PluginInstanceID: testInstance,
		Config:           []byte(cfgJSON),
		ConfigRevision:   1,
	})
	if err != nil {
		a.t.Fatalf("ConfigureInstance: %v", err)
	}
	return resp
}

func (a *testApp) validate(bindings []application.Binding) *application.ValidateBindingResponse {
	a.t.Helper()
	resp, err := a.cli.ValidateBinding(a.ctx, &application.ValidateBindingRequest{
		PluginInstanceID: testInstance,
		Bindings:         bindings,
	})
	if err != nil {
		a.t.Fatalf("ValidateBinding: %v", err)
	}
	return resp
}

func (a *testApp) openStream() {
	a.t.Helper()
	st, err := a.cli.HandleEvents(a.ctx)
	if err != nil {
		a.t.Fatalf("HandleEvents: %v", err)
	}
	a.stream = st

	// The real runtime dispatches an initial lifecycle event before jobs can
	// arrive. Wait for the service-side HandleEvents loop to register its
	// writer so RunJob-driven tests do not race that startup handshake.
	deadline := time.Now().Add(5 * time.Second)
	for {
		a.svc.mu.Lock()
		ready := a.svc.writer != nil
		a.svc.mu.Unlock()
		if ready {
			return
		}
		if time.Now().After(deadline) {
			a.t.Fatal("service event writer did not become ready")
		}
		time.Sleep(time.Millisecond)
	}
}

func (a *testApp) send(seq uint64, union application.ApplicationEventUnion) {
	a.t.Helper()
	if err := a.stream.Send(a.ctx, &application.ApplicationEvent{
		PluginInstanceID: testInstance,
		Sequence:         seq,
		SchemaVersion:    "1",
		Union:            union,
	}); err != nil {
		a.t.Fatalf("send event: %v", err)
	}
}

// recvEffects drains effects until the stream goes idle (a short timeout) or
// EOF. It models the fact that an event may produce a variable number of
// effects on a bidi stream.
func (a *testApp) recvEffects(idle time.Duration) []*application.ApplicationEffect {
	a.t.Helper()
	var out []*application.ApplicationEffect
	for {
		rctx, cancel := context.WithTimeout(a.ctx, idle)
		eff, err := a.stream.Recv(rctx)
		cancel()
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, context.DeadlineExceeded) ||
				errors.Is(err, context.Canceled) || errors.Is(err, transport.ErrClosed) {
				break
			}
			a.t.Fatalf("recv effect: %v", err)
		}
		out = append(out, eff)
	}
	return out
}

// waitEffects 先条件等待至 least 个效果到达（30s 上限），再按 idle 语义把同批
// 余量收干。慢机/满载下首个效果的到达可远晚于固定 idle 窗（Windows CI 实测
// 60ms 被击穿收到空集）；正断言与阶段间 drain 必须用它。同一事件的效果由服务
// 端连续产出，首个到达后其余紧随其后，idle 收干即可保证不泄漏进下一阶段。
func (a *testApp) waitEffects(least int, idle time.Duration) []*application.ApplicationEffect {
	a.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var out []*application.ApplicationEffect
	for len(out) < least {
		remain := time.Until(deadline)
		if remain <= 0 {
			a.t.Fatalf("waitEffects: 超时只收到 %d/%d 个效果", len(out), least)
		}
		rctx, cancel := context.WithTimeout(a.ctx, remain)
		eff, err := a.stream.Recv(rctx)
		cancel()
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				a.t.Fatalf("waitEffects: 超时只收到 %d/%d 个效果", len(out), least)
			}
			if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) || errors.Is(err, transport.ErrClosed) {
				a.t.Fatalf("waitEffects: 流已关闭，收到 %d/%d 个效果: %v", len(out), least, err)
			}
			a.t.Fatalf("recv effect: %v", err)
		}
		out = append(out, eff)
	}
	for {
		rctx, cancel := context.WithTimeout(a.ctx, idle)
		eff, err := a.stream.Recv(rctx)
		cancel()
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, context.DeadlineExceeded) ||
				errors.Is(err, context.Canceled) || errors.Is(err, transport.ErrClosed) {
				break
			}
			a.t.Fatalf("recv effect: %v", err)
		}
		out = append(out, eff)
	}
	return out
}

func (a *testApp) runJob(jobID, idem string) *application.RunJobResponse {
	a.t.Helper()
	return a.runJobWithArgs(jobID, `{"window_id":"w-morning"}`, idem)
}

func (a *testApp) runJobWithArgs(jobID, argsJSON, idem string) *application.RunJobResponse {
	a.t.Helper()
	resp, err := a.cli.RunJob(a.ctx, &application.RunJobRequest{
		PluginInstanceID: testInstance,
		JobID:            jobID,
		ArgsJSON:         argsJSON,
		IdempotencyKey:   idem,
	})
	if err != nil {
		a.t.Fatalf("RunJob: %v", err)
	}
	return resp
}

func (a *testApp) close() {
	a.t.Helper()
	a.cancel()
	_ = a.serverEnd.Close()
	_ = a.clientEnd.Close()
	select {
	case <-a.serveDone:
	case <-time.After(5 * time.Second):
		a.t.Error("server did not stop after close")
	}
}

func mustConfigureAndBind(t *testing.T, now time.Time) *testApp {
	t.Helper()
	a := newTestApp(t, now)
	if resp := a.configure(validConfigJSON); !resp.Status.IsOK() {
		t.Fatalf("configure rejected: %s", resp.Status)
	}
	if resp := a.validate(validBindings); !resp.Valid {
		t.Fatalf("bindings rejected: %+v", resp.Issues)
	}
	return a
}

// --- tests ---

func TestDescriptorRequirements(t *testing.T) {
	a := newTestApp(t, time.Now())
	defer a.close()

	desc, err := a.cli.Describe(a.ctx)
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if desc.ApplicationID != "io.github.deliciousbuding.cloud-path-app-scheduled-compartment" {
		t.Fatalf("application id = %q", desc.ApplicationID)
	}
	if desc.Version != "0.2.0" {
		t.Fatalf("version = %q", desc.Version)
	}
	if desc.DeclarativeOnly {
		t.Fatal("expected process-based application (declarative_only=false)")
	}
	if len(desc.Requirements) != 3 {
		t.Fatalf("requirements = %d, want 3", len(desc.Requirements))
	}
	want := map[string]struct {
		cap  string
		card string
		min  uint32
	}{
		"reminder-output": {"cloudpath.dev/capability/buzzer@1", "one", 0},
		"compartments":    {"cloudpath.dev/capability/key@1", "one-or-more", 3},
		"local-display":   {"cloudpath.dev/capability/display-text@1", "zero-or-one", 0},
	}
	for _, r := range desc.Requirements {
		w, ok := want[r.ID]
		if !ok {
			t.Fatalf("unexpected requirement %q", r.ID)
		}
		if r.Capability != w.cap || r.Cardinality != w.card || r.MinItems != w.min {
			t.Fatalf("requirement %q = (%s,%s,%d), want (%s,%s,%d)",
				r.ID, r.Capability, r.Cardinality, r.MinItems, w.cap, w.card, w.min)
		}
		delete(want, r.ID)
	}
	if len(want) != 0 {
		t.Fatalf("missing requirements: %v", want)
	}
	if len(desc.Jobs) != 1 || desc.Jobs[0].ID != "window-check" {
		t.Fatalf("jobs = %+v, want single window-check job", desc.Jobs)
	}
}

func TestConfigureAndValidateBinding(t *testing.T) {
	a := newTestApp(t, time.Now())
	defer a.close()

	// valid configure
	cfgResp := a.configure(validConfigJSON)
	if !cfgResp.Status.IsOK() {
		t.Fatalf("valid config rejected: %s", cfgResp.Status)
	}
	if cfgResp.AppliedRevision != 1 {
		t.Fatalf("applied revision = %d, want 1", cfgResp.AppliedRevision)
	}

	// valid bindings
	vResp := a.validate(validBindings)
	if !vResp.Valid {
		t.Fatalf("valid bindings rejected: %+v", vResp.Issues)
	}
	if len(vResp.Issues) != 0 {
		t.Fatalf("expected no issues, got %+v", vResp.Issues)
	}

	// missing reminder-output -> invalid
	bad := []application.Binding{
		{RequirementID: "compartments", EntityID: c1},
		{RequirementID: "compartments", EntityID: c2},
		{RequirementID: "compartments", EntityID: c3},
	}
	if r := a.validate(bad); r.Valid {
		t.Fatal("expected missing reminder-output to be invalid")
	}

	// fewer than 3 compartments -> invalid
	bad2 := []application.Binding{
		{RequirementID: "reminder-output", EntityID: buzzerEntityID},
		{RequirementID: "compartments", EntityID: c1},
		{RequirementID: "compartments", EntityID: c2},
	}
	if r := a.validate(bad2); r.Valid {
		t.Fatal("expected fewer than 3 compartments to be invalid")
	}

	// unknown requirement -> invalid (structural driver-coupling rejection)
	bad3 := []application.Binding{
		{RequirementID: "reminder-output", EntityID: buzzerEntityID},
		{RequirementID: "compartments", EntityID: c1},
		{RequirementID: "compartments", EntityID: c2},
		{RequirementID: "compartments", EntityID: c3},
		{RequirementID: "driver:vendor-device", EntityID: "dev/thing"},
	}
	if r := a.validate(bad3); r.Valid {
		t.Fatal("expected unknown requirement to be invalid")
	} else if len(r.Issues) == 0 {
		t.Fatal("expected at least one issue")
	}

	// empty entity -> invalid
	bad4 := []application.Binding{
		{RequirementID: "reminder-output", EntityID: ""},
		{RequirementID: "compartments", EntityID: c1},
		{RequirementID: "compartments", EntityID: c2},
		{RequirementID: "compartments", EntityID: c3},
	}
	if r := a.validate(bad4); r.Valid {
		t.Fatal("expected empty entity_id to be invalid")
	}
}

func TestWindowReminderEffect(t *testing.T) {
	// 00:00 UTC is 08:00 in the configured Asia/Shanghai timezone.
	a := mustConfigureAndBind(t, time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC))
	defer a.close()
	a.openStream()
	if resp := a.runJob("window-check", "job-open-1"); !resp.Status.IsOK() {
		t.Fatalf("RunJob status: %s", resp.Status)
	}
	effects := a.waitEffects(3, 60*time.Millisecond)

	gotRequest := requestCommandOf(effects)
	if gotRequest == nil {
		t.Fatal("expected a RequestCommand(buzzer) effect")
	}
	if gotRequest.EntityID != buzzerEntityID {
		t.Fatalf("request entity = %q, want %q", gotRequest.EntityID, buzzerEntityID)
	}
	if gotRequest.Action != "buzzer" {
		t.Fatalf("action = %q, want buzzer", gotRequest.Action)
	}
	var args struct {
		Freq     int `json:"freq"`
		Duration int `json:"duration"`
	}
	if err := json.Unmarshal([]byte(gotRequest.ArgsJSON), &args); err != nil {
		t.Fatalf("buzzer args %q: %v", gotRequest.ArgsJSON, err)
	}
	if args.Freq != defaultReminder.Freq || args.Duration != defaultReminder.Duration {
		t.Fatalf("buzzer args = %+v, want default reminder policy %+v", args, defaultReminder)
	}
	if gotRequest.IdempotencyKey != "reminder-w-morning@2026-09-03" {
		t.Fatalf("idempotency = %q, want occurrence-qualified key", gotRequest.IdempotencyKey)
	}
	if got := recordIDOf(effects, "w-morning"); got != "w-morning@2026-09-03" {
		t.Fatalf("record id = %q, want occurrence-qualified id", got)
	}
	task := scheduleTaskOf(effects)
	if task == nil {
		t.Fatal("expected a durable miss ScheduleTask")
	}
	if task.ScheduleID != "window-miss-w-morning@2026-09-03" || task.Cron != windowCheckCron {
		t.Fatalf("miss task = %+v", task)
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(task.PayloadJSON), &payload); err != nil {
		t.Fatalf("miss payload %q: %v", task.PayloadJSON, err)
	}
	if payload["window_id"] != "w-morning" || payload["occurrence_date"] != "2026-09-03" {
		t.Fatalf("miss payload = %+v", payload)
	}
	if got := windowStateOf(effects, "w-morning"); got != windowOpened {
		t.Fatalf("window state = %q, want %q", got, windowOpened)
	}
}

func TestKeyPressCompletesWindow(t *testing.T) {
	a := mustConfigureAndBind(t, time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC))
	defer a.close()
	a.openStream()
	if resp := a.runJob("window-check", "job-open-1"); !resp.Status.IsOK() {
		t.Fatalf("RunJob status: %s", resp.Status)
	}
	_ = a.waitEffects(3, 60*time.Millisecond)

	a.send(2, &application.CapabilityEvent{
		RequirementID: "compartments",
		EntityID:      c1,
		EventType:     keyPressEvent,
		OccurredAt:    "2026-09-03T08:05:00+08:00",
	})
	effects := a.waitEffects(2, 60*time.Millisecond)
	if got := windowStateOf(effects, "w-morning"); got != windowCompleted {
		t.Fatalf("window state = %q, want %q", got, windowCompleted)
	}
	if got := recordIDOf(effects, "w-morning"); got != "w-morning@2026-09-03" {
		t.Fatalf("record id = %q, want occurrence-qualified id", got)
	}
	if !hasCancelTask(effects, "window-miss-w-morning@2026-09-03") {
		t.Fatalf("expected occurrence-qualified CancelScheduledTask, effects=%+v", effects)
	}
}

func TestMissedWindowRecord(t *testing.T) {
	start := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC) // 08:00 Asia/Shanghai
	a := mustConfigureAndBind(t, start)
	defer a.close()
	a.openStream()
	if resp := a.runJob("window-check", "job-open-1"); !resp.Status.IsOK() {
		t.Fatalf("RunJob status: %s", resp.Status)
	}
	_ = a.waitEffects(3, 60*time.Millisecond)

	// advance the clock past the window end and run the durable miss job
	a.now = time.Date(2026, 9, 3, 0, 31, 0, 0, time.UTC) // 08:31 Asia/Shanghai
	const missID = "window-miss-w-morning@2026-09-03"
	resp := a.runJobWithArgs(missID, `{"window_id":"w-morning","occurrence_date":"2026-09-03"}`, "job-missed-1")
	if !resp.Status.IsOK() {
		t.Fatalf("RunJob status: %s", resp.Status)
	}
	if !strings.Contains(resp.ResultJSON, "w-morning") {
		t.Fatalf("result %s does not mention w-morning", resp.ResultJSON)
	}
	effects := a.waitEffects(3, 60*time.Millisecond)
	if got := windowStateOf(effects, "w-morning"); got != windowMissed {
		t.Fatalf("window state = %q, want %q", got, windowMissed)
	}
	if !hasCancelTask(effects, missID) {
		t.Fatal("expected CancelScheduledTask for the occurrence-qualified miss task")
	}
	if !hasNotification(effects) {
		t.Fatal("expected a SendNotification for the missed window")
	}

	// idempotency: the same key returns the stored result without re-emitting.
	resp2 := a.runJobWithArgs(missID, `{"window_id":"w-morning","occurrence_date":"2026-09-03"}`, "job-missed-1")
	if resp2.ResultJSON != resp.ResultJSON {
		t.Fatalf("idempotent result mismatch: %s vs %s", resp2.ResultJSON, resp.ResultJSON)
	}
	if dup := a.recvEffects(40 * time.Millisecond); len(dup) != 0 {
		t.Fatalf("duplicate miss job emitted %d effects", len(dup))
	}

	// A different dispatch key for the already-missed occurrence is also a no-op.
	_ = a.runJobWithArgs(missID, `{"window_id":"w-morning","occurrence_date":"2026-09-03"}`, "job-missed-2")
	if dup := a.recvEffects(40 * time.Millisecond); len(dup) != 0 {
		t.Fatalf("duplicate occurrence miss emitted %d effects", len(dup))
	}
}

func TestDuplicateJobIdempotent(t *testing.T) {
	a := mustConfigureAndBind(t, time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC))
	defer a.close()
	a.openStream()

	first := a.runJob("window-check", "job-open-1")
	start := a.waitEffects(3, 60*time.Millisecond)
	if n := countRequestCommand(start); n != 1 {
		t.Fatalf("window start emitted %d RequestCommand, want 1", n)
	}
	if n := countScheduleTask(start); n != 1 {
		t.Fatalf("window start emitted %d ScheduleTask, want 1", n)
	}

	// Same idempotency key returns the stored result without re-emitting.
	second := a.runJob("window-check", "job-open-1")
	if second.ResultJSON != first.ResultJSON {
		t.Fatalf("idempotent result mismatch: %s vs %s", second.ResultJSON, first.ResultJSON)
	}
	if dup := a.recvEffects(40 * time.Millisecond); len(dup) != 0 {
		t.Fatalf("duplicate idempotency key emitted %d effects", len(dup))
	}

	// A different automatic-minute key for the same occurrence is also a no-op.
	_ = a.runJob("window-check", "job-open-2")
	if dup := a.recvEffects(40 * time.Millisecond); len(dup) != 0 {
		t.Fatalf("duplicate occurrence emitted %d effects", len(dup))
	}

	a.send(3, &application.CapabilityEvent{
		RequirementID: "compartments", EntityID: c1, EventType: keyPressEvent,
		OccurredAt: "2026-09-03T08:05:00+08:00",
	})
	after := a.waitEffects(2, 60*time.Millisecond)
	if n := countRequestCommand(after); n != 0 {
		t.Fatalf("completion emitted %d additional RequestCommand, want 0", n)
	}
	if got := windowStateOf(after, "w-morning"); got != windowCompleted {
		t.Fatalf("window state after complete = %q, want %q", got, windowCompleted)
	}

	// A duplicate key press after completion must not re-complete.
	a.send(4, &application.CapabilityEvent{
		RequirementID: "compartments", EntityID: c1, EventType: keyPressEvent,
		OccurredAt: "2026-09-03T08:06:00+08:00",
	})
	if dup := a.recvEffects(40 * time.Millisecond); len(dup) != 0 {
		t.Fatalf("duplicate key press after completion emitted %d effects", len(dup))
	}
}

func TestLateObservationDoesNotFabricateMissed(t *testing.T) {
	// A first observation after the window has ended may be a restart after a
	// completed window. The app has no read API for its durable records, so it
	// must not fabricate a missed event from memory alone.
	a := mustConfigureAndBind(t, time.Date(2026, 9, 3, 0, 31, 0, 0, time.UTC))
	defer a.close()
	a.openStream()

	resp := a.runJob("window-check", "job-late-observation")
	if !resp.Status.IsOK() {
		t.Fatalf("RunJob status: %s", resp.Status)
	}
	if resp.ResultJSON != `{"missed":[]}` {
		t.Fatalf("late observation result = %s, want empty missed list", resp.ResultJSON)
	}
	if effects := a.recvEffects(60 * time.Millisecond); len(effects) != 0 {
		t.Fatalf("late observation emitted %d effects, want none", len(effects))
	}
}

func TestLateObservationRecordsOpenedWithoutStaleReminder(t *testing.T) {
	// Restarting mid-window records the occurrence but does not replay a stale
	// reminder or arm a second miss task. The key-press path still completes it.
	a := mustConfigureAndBind(t, time.Date(2026, 9, 3, 0, 5, 0, 0, time.UTC)) // 08:05
	defer a.close()
	a.openStream()

	_ = a.runJob("window-check", "job-late-mid-window")
	effects := a.waitEffects(1, 60*time.Millisecond)
	if got := windowStateOf(effects, "w-morning"); got != windowOpened {
		t.Fatalf("late mid-window state = %q, want %q", got, windowOpened)
	}
	if n := countRequestCommand(effects); n != 0 {
		t.Fatalf("late mid-window emitted %d stale reminders", n)
	}
	if n := countScheduleTask(effects); n != 0 {
		t.Fatalf("late mid-window emitted %d miss tasks, want 0", n)
	}
}

func TestRestartKeyPressCompletesOccurrence(t *testing.T) {
	// No warm window-check state: the event timestamp plus bounded config must
	// still identify and complete the current occurrence.
	a := mustConfigureAndBind(t, time.Date(2026, 9, 3, 0, 5, 0, 0, time.UTC))
	defer a.close()
	a.openStream()

	a.send(1, &application.CapabilityEvent{
		RequirementID: "compartments", EntityID: c1, EventType: keyPressEvent,
		OccurredAt: "2026-09-03T08:06:00+08:00",
	})
	effects := a.waitEffects(2, 60*time.Millisecond)
	if got := windowStateOf(effects, "w-morning"); got != windowCompleted {
		t.Fatalf("restart key press state = %q, want %q", got, windowCompleted)
	}
	if !hasCancelTask(effects, "window-miss-w-morning@2026-09-03") {
		t.Fatalf("restart key press did not cancel the occurrence task: %+v", effects)
	}
}

func TestRestartAfterCompletionDoesNotFabricateMissed(t *testing.T) {
	a := mustConfigureAndBind(t, time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC))
	a.openStream()
	_ = a.runJob("window-check", "first-run")
	_ = a.waitEffects(3, 60*time.Millisecond)
	a.send(1, &application.CapabilityEvent{
		RequirementID: "compartments", EntityID: c1, EventType: keyPressEvent,
		OccurredAt: "2026-09-03T08:05:00+08:00",
	})
	_ = a.waitEffects(2, 60*time.Millisecond)
	a.close()

	// The durable miss task was cancelled by the completion effect. After a
	// restart the automatic opener sees no warm state and must stay quiet.
	b := mustConfigureAndBind(t, time.Date(2026, 9, 3, 0, 31, 0, 0, time.UTC))
	defer b.close()
	b.openStream()
	_ = b.runJob("window-check", "restart-check")
	if effects := b.recvEffects(60 * time.Millisecond); len(effects) != 0 {
		t.Fatalf("restart emitted %d false effects, want none", len(effects))
	}
}

func TestMissJobAfterRestartRecordsMissed(t *testing.T) {
	// The miss task payload is self-contained. Even with no warm occurrence
	// state, the durable deadline job can reconstruct and record the miss.
	a := mustConfigureAndBind(t, time.Date(2026, 9, 3, 0, 31, 0, 0, time.UTC))
	defer a.close()
	a.openStream()

	const missID = "window-miss-w-morning@2026-09-03"
	_ = a.runJobWithArgs(missID, `{"window_id":"w-morning","occurrence_date":"2026-09-03"}`, "restart-miss")
	effects := a.waitEffects(3, 60*time.Millisecond)
	if got := windowStateOf(effects, "w-morning"); got != windowMissed {
		t.Fatalf("restart miss state = %q, want %q", got, windowMissed)
	}
	if !hasCancelTask(effects, missID) || !hasNotification(effects) {
		t.Fatalf("restart miss effects = %+v", effects)
	}
}

func TestAutomaticJobDefersMissToDurableTask(t *testing.T) {
	a := mustConfigureAndBind(t, time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC))
	defer a.close()
	a.openStream()
	_ = a.runJob("window-check", "open")
	_ = a.waitEffects(3, 60*time.Millisecond)

	a.now = time.Date(2026, 9, 3, 0, 31, 0, 0, time.UTC)
	_ = a.runJob("window-check", "automatic-after-end")
	if effects := a.recvEffects(60 * time.Millisecond); len(effects) != 0 {
		t.Fatalf("automatic job duplicated durable miss work: %+v", effects)
	}

	const missID = "window-miss-w-morning@2026-09-03"
	_ = a.runJobWithArgs(missID, `{"window_id":"w-morning","occurrence_date":"2026-09-03"}`, "durable-miss")
	effects := a.waitEffects(3, 60*time.Millisecond)
	if got := windowStateOf(effects, "w-morning"); got != windowMissed {
		t.Fatalf("durable miss state = %q, want %q", got, windowMissed)
	}
}

func TestMissJobBeforeEndDoesNotMiss(t *testing.T) {
	a := mustConfigureAndBind(t, time.Date(2026, 9, 3, 0, 29, 0, 0, time.UTC))
	defer a.close()
	a.openStream()

	const missID = "window-miss-w-morning@2026-09-03"
	args := `{"window_id":"w-morning","occurrence_date":"2026-09-03"}`
	_ = a.runJobWithArgs(missID, args, "early-miss")
	if effects := a.recvEffects(60 * time.Millisecond); len(effects) != 0 {
		t.Fatalf("early miss job emitted %d effects, want none", len(effects))
	}

	a.now = time.Date(2026, 9, 3, 0, 31, 0, 0, time.UTC)
	_ = a.runJobWithArgs(missID, args, "deadline-miss")
	effects := a.waitEffects(3, 60*time.Millisecond)
	if got := windowStateOf(effects, "w-morning"); got != windowMissed {
		t.Fatalf("deadline miss state = %q, want %q", got, windowMissed)
	}
}

func TestWindowCheckOpensAgainOnNextDay(t *testing.T) {
	a := mustConfigureAndBind(t, time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC))
	defer a.close()
	a.openStream()

	_ = a.runJob("window-check", "day-1-open")
	dayOne := a.waitEffects(3, 60*time.Millisecond)
	if n := countRequestCommand(dayOne); n != 1 {
		t.Fatalf("day 1 emitted %d RequestCommand, want 1", n)
	}
	dayOneRecord := recordIDOf(dayOne, "w-morning")
	dayOneCommand := requestCommandOf(dayOne)
	dayOneTask := scheduleTaskOf(dayOne)
	a.send(1, &application.CapabilityEvent{
		RequirementID: "compartments", EntityID: c1, EventType: keyPressEvent,
		OccurredAt: "2026-09-03T08:05:00+08:00",
	})
	_ = a.waitEffects(2, 60*time.Millisecond)

	a.now = time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC)
	_ = a.runJob("window-check", "day-2-open")
	dayTwo := a.waitEffects(3, 60*time.Millisecond)
	if n := countRequestCommand(dayTwo); n != 1 {
		t.Fatalf("day 2 emitted %d RequestCommand, want 1", n)
	}
	if got := windowStateOf(dayTwo, "w-morning"); got != windowOpened {
		t.Fatalf("day 2 window state = %q, want %q", got, windowOpened)
	}
	dayTwoRecord := recordIDOf(dayTwo, "w-morning")
	dayTwoCommand := requestCommandOf(dayTwo)
	dayTwoTask := scheduleTaskOf(dayTwo)
	if dayOneRecord == dayTwoRecord || dayOneCommand == nil || dayTwoCommand == nil ||
		dayOneCommand.IdempotencyKey == dayTwoCommand.IdempotencyKey ||
		dayOneTask == nil || dayTwoTask == nil || dayOneTask.ScheduleID == dayTwoTask.ScheduleID {
		t.Fatalf("cross-day identities collide: records=%q/%q commands=%+v/%+v tasks=%+v/%+v",
			dayOneRecord, dayTwoRecord, dayOneCommand, dayTwoCommand, dayOneTask, dayTwoTask)
	}
}

func TestLateObservationFallsBackToMissedAtEnd(t *testing.T) {
	a := mustConfigureAndBind(t, time.Date(2026, 9, 3, 0, 5, 0, 0, time.UTC)) // 08:05
	defer a.close()
	a.openStream()

	_ = a.runJob("window-check", "late-mid-window")
	_ = a.waitEffects(1, 60*time.Millisecond)

	a.now = time.Date(2026, 9, 3, 0, 31, 0, 0, time.UTC) // 08:31
	_ = a.runJob("window-check", "late-fallback")
	effects := a.waitEffects(3, 60*time.Millisecond)
	if got := windowStateOf(effects, "w-morning"); got != windowMissed {
		t.Fatalf("late fallback state = %q, want %q", got, windowMissed)
	}
	if !hasCancelTask(effects, "window-miss-w-morning@2026-09-03") || !hasNotification(effects) {
		t.Fatalf("late fallback effects = %+v", effects)
	}
}

func TestRestartInsideFreshGraceMayRearmMissTask(t *testing.T) {
	// A fresh service at 08:00:30 models a restart with no warm state. Application
	// Protocol v1 has no read API to distinguish that from a first observation,
	// so this test locks the documented ambiguity: the occurrence is opened and
	// its durable miss task is armed again.
	a := mustConfigureAndBind(t, time.Date(2026, 9, 3, 0, 0, 30, 0, time.UTC)) // 08:00:30
	defer a.close()
	a.openStream()

	_ = a.runJob("window-check", "restart-inside-grace")
	effects := a.waitEffects(3, 60*time.Millisecond)
	if got := windowStateOf(effects, "w-morning"); got != windowOpened {
		t.Fatalf("restart-inside-grace state = %q, want %q", got, windowOpened)
	}
	if n := countScheduleTask(effects); n != 1 {
		t.Fatalf("restart-inside-grace emitted %d miss tasks, want 1", n)
	}
}

func TestStaleMissAfterNextDayCheckDoesNotOverrideCompletion(t *testing.T) {
	a := mustConfigureAndBind(t, time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC))
	defer a.close()
	a.openStream()

	_ = a.runJob("window-check", "day-1-open")
	_ = a.waitEffects(3, 60*time.Millisecond)
	a.send(1, &application.CapabilityEvent{
		RequirementID: "compartments", EntityID: c1, EventType: keyPressEvent,
		OccurredAt: "2026-09-03T08:05:00+08:00",
	})
	_ = a.waitEffects(2, 60*time.Millisecond)

	// Advancing through the next day's automatic check used to evict the
	// completed occurrence before a delayed durable dispatch for day one.
	a.now = time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC)
	_ = a.runJob("window-check", "day-2-open")
	_ = a.waitEffects(3, 60*time.Millisecond)

	const staleMissID = "window-miss-w-morning@2026-09-03"
	resp := a.runJobWithArgs(staleMissID, `{"window_id":"w-morning","occurrence_date":"2026-09-03"}`, "stale-day-1-miss")
	if resp.ResultJSON != `{"missed":[]}` {
		t.Fatalf("stale miss result = %s, want empty missed list", resp.ResultJSON)
	}
	effects := a.recvEffects(60 * time.Millisecond)
	if got := windowStateOf(effects, "w-morning"); got != "" {
		t.Fatalf("stale miss changed completed occurrence to %q: %+v", got, effects)
	}
	if hasNotification(effects) {
		t.Fatalf("stale miss emitted a missed notification: %+v", effects)
	}
}

func TestRejectDriverCoupling(t *testing.T) {
	a := newTestApp(t, time.Now())
	defer a.close()

	desc, err := a.cli.Describe(a.ctx)
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	// A device-agnostic application only consumes public capability
	// requirements. Asserting the closed allowed set makes it structurally
	// impossible to couple to a Driver requirement.
	for _, r := range desc.Requirements {
		switch r.Capability {
		case buzzerCap, keyCap, displayCap:
		default:
			t.Fatalf("requirement %s uses undeclared capability %s", r.ID, r.Capability)
		}
	}

	// Bindings must be for declared capability requirements only; a driver
	// requirement id is rejected, so the application can never couple to one.
	resp := a.validate([]application.Binding{
		{RequirementID: "reminder-output", EntityID: buzzerEntityID},
		{RequirementID: "compartments", EntityID: c1},
		{RequirementID: "compartments", EntityID: c2},
		{RequirementID: "compartments", EntityID: c3},
		{RequirementID: "driver:vendor-device", EntityID: "dev/thing"},
	})
	if resp.Valid {
		t.Fatal("expected driver requirement to be rejected")
	}
	found := false
	for _, issue := range resp.Issues {
		if issue.RequirementID == "driver:vendor-device" && strings.Contains(issue.Message, "not declared") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a 'not declared' issue for the driver requirement, got %+v", resp.Issues)
	}
}

func TestInvalidScheduleConfig(t *testing.T) {
	a := newTestApp(t, time.Now())
	defer a.close()

	invalid := []string{
		// missing timezone
		`{"compartments":[{"id":"c1"},{"id":"c2"},{"id":"c3"}],"schedule":[{"id":"w1","compartment":"c1","start":"08:00","end":"08:30"}]}`,
		// invalid timezone name
		`{"timezone":"Not/AZone","compartments":[{"id":"c1"},{"id":"c2"},{"id":"c3"}],"schedule":[{"id":"w1","compartment":"c1","start":"08:00","end":"08:30"}]}`,
		// missing compartments
		`{"timezone":"Asia/Shanghai","schedule":[{"id":"w1","compartment":"c1","start":"08:00","end":"08:30"}]}`,
		// duplicate compartment id
		`{"timezone":"Asia/Shanghai","compartments":[{"id":"c1"},{"id":"c1"},{"id":"c2"}],"schedule":[{"id":"w1","compartment":"c1","start":"08:00","end":"08:30"}]}`,
		// missing schedule
		`{"timezone":"Asia/Shanghai","compartments":[{"id":"c1"},{"id":"c2"},{"id":"c3"}]}`,
		// window references unknown compartment
		`{"timezone":"Asia/Shanghai","compartments":[{"id":"c1"},{"id":"c2"},{"id":"c3"}],"schedule":[{"id":"w1","compartment":"nope","start":"08:00","end":"08:30"}]}`,
		// invalid HH:MM
		`{"timezone":"Asia/Shanghai","compartments":[{"id":"c1"},{"id":"c2"},{"id":"c3"}],"schedule":[{"id":"w1","compartment":"c1","start":"25:00","end":"08:30"}]}`,
		// end before start
		`{"timezone":"Asia/Shanghai","compartments":[{"id":"c1"},{"id":"c2"},{"id":"c3"}],"schedule":[{"id":"w1","compartment":"c1","start":"08:30","end":"08:00"}]}`,
		// reminder freq out of buzzer step range
		`{"timezone":"Asia/Shanghai","compartments":[{"id":"c1"},{"id":"c2"},{"id":"c3"}],"schedule":[{"id":"w1","compartment":"c1","start":"08:00","end":"08:30"}],"reminder":{"freq":10,"duration":1}}`,
		// reminder duration negative
		`{"timezone":"Asia/Shanghai","compartments":[{"id":"c1"},{"id":"c2"},{"id":"c3"}],"schedule":[{"id":"w1","compartment":"c1","start":"08:00","end":"08:30"}],"reminder":{"freq":1,"duration":-1}}`,
		// malformed JSON
		`{`,
	}
	for i, cfg := range invalid {
		resp := a.configure(cfg)
		if resp.Status.IsOK() {
			t.Fatalf("case %d: invalid config accepted", i)
		}
		if resp.AppliedRevision != 0 {
			t.Fatalf("case %d: applied revision = %d, want 0", i, resp.AppliedRevision)
		}
	}
}

func TestGracefulShutdown(t *testing.T) {
	a := mustConfigureAndBind(t, time.Now())
	defer a.close()

	sh, err := a.cli.Shutdown(a.ctx, &application.ShutdownRequest{Reason: "host close", GraceSeconds: 1})
	if err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if !sh.Status.IsOK() {
		t.Fatalf("shutdown status: %s", sh.Status)
	}

	health, err := a.cli.Health(a.ctx)
	if err != nil {
		t.Fatalf("Health after shutdown: %v", err)
	}
	if health.State != application.HealthStateNotServing {
		t.Fatalf("health state after shutdown = %v, want NotServing", health.State)
	}
}

// --- helpers ---

func windowStateOf(effects []*application.ApplicationEffect, id string) string {
	m, ok := windowDataOf(effects, id)
	if !ok {
		return ""
	}
	state, _ := m["state"].(string)
	return state
}

func windowDataOf(effects []*application.ApplicationEffect, id string) (map[string]any, bool) {
	for _, e := range effects {
		u, ok := e.Union.(*application.UpsertDomainRecord)
		if !ok || u.RecordType != "window" {
			continue
		}
		var m map[string]any
		if json.Unmarshal([]byte(u.DataJSON), &m) != nil {
			continue
		}
		if u.RecordID == id || m["id"] == id || m["occurrence_id"] == id {
			return m, true
		}
	}
	return nil, false
}

func recordIDOf(effects []*application.ApplicationEffect, windowID string) string {
	for _, e := range effects {
		u, ok := e.Union.(*application.UpsertDomainRecord)
		if !ok || u.RecordType != "window" {
			continue
		}
		var m map[string]any
		if json.Unmarshal([]byte(u.DataJSON), &m) != nil || m["id"] != windowID {
			continue
		}
		return u.RecordID
	}
	return ""
}

func requestCommandOf(effects []*application.ApplicationEffect) *application.RequestCommand {
	for _, e := range effects {
		if u, ok := e.Union.(*application.RequestCommand); ok {
			return u
		}
	}
	return nil
}

func scheduleTaskOf(effects []*application.ApplicationEffect) *application.ScheduleTask {
	for _, e := range effects {
		if u, ok := e.Union.(*application.ScheduleTask); ok {
			return u
		}
	}
	return nil
}

func countScheduleTask(effects []*application.ApplicationEffect) int {
	n := 0
	for _, e := range effects {
		if _, ok := e.Union.(*application.ScheduleTask); ok {
			n++
		}
	}
	return n
}

func countRequestCommand(effects []*application.ApplicationEffect) int {
	n := 0
	for _, e := range effects {
		if _, ok := e.Union.(*application.RequestCommand); ok {
			n++
		}
	}
	return n
}

func hasCancelTask(effects []*application.ApplicationEffect, scheduleID string) bool {
	for _, e := range effects {
		if u, ok := e.Union.(*application.CancelScheduledTask); ok && u.ScheduleID == scheduleID {
			return true
		}
	}
	return false
}

func hasNotification(effects []*application.ApplicationEffect) bool {
	for _, e := range effects {
		if _, ok := e.Union.(*application.SendNotification); ok {
			return true
		}
	}
	return false
}
