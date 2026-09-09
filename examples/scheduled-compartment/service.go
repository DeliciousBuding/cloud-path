package scheduledcompartment

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/application"
	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/status"
)

// Manifest identity of the reference application. These must mirror
// examples/scheduled-compartment/plugin.yaml.
const (
	pluginIDValue       = "io.github.deliciousbuding.cloud-path-app-scheduled-compartment"
	pluginVersion       = "0.2.0"
	jobWindowCheck      = "window-check"
	jobWindowMissPrefix = "window-miss-"
	windowCheckCron     = "* * * * *"
	buzzerAction        = "buzzer"
	displayCap          = "cloudpath.dev/capability/display-text@1"
	buzzerCap           = "cloudpath.dev/capability/buzzer@1"
	keyCap              = "cloudpath.dev/capability/key@1"
)

// windowFreshOpenGrace bounds when the first observation of an already-started
// window is still treated as a fresh open. A later observation may be a plugin
// restart or a late start; it records the occurrence but does not replay a
// stale reminder or create a second durable miss task.
const windowFreshOpenGrace = time.Minute

// window state values stored in domain records.
const (
	windowOpened    = "opened"
	windowCompleted = "completed"
	windowMissed    = "missed"
)

// keyPressEvent is the event type delivered by the key@1 capability. A key
// press on a bound compartment entity means "the user confirmed this
// compartment": that interpretation lives here, in the application, and never
// in a Driver.
const keyPressEvent = keyCap + "/press"

// windowTrack is the in-memory runtime state for one scheduled window instance.
type windowTrack struct {
	ID                string
	OccurrenceID      string
	Compartment       string
	Start             time.Time
	End               time.Time
	State             string
	OpenedAt          time.Time
	ClosedAt          time.Time
	ReminderEntity    string
	MissTaskScheduled bool
}

// instanceState is the per-plugin-instance runtime state.
type instanceState struct {
	config    *Config
	configRev uint32
	bindings  map[string][]string
	windows   map[string]*windowTrack
	lastSeq   uint64
	jobs      map[string]string // idempotency key -> result JSON
}

// Service implements the ApplicationService protocol for the Scheduled
// Compartment reference application. It is device-agnostic: it only ever works
// with the entity ids supplied through capability bindings and never refers to
// a Driver id, port or vendor field.
type Service struct {
	pluginID  string
	version   string
	runtimeID string

	mu          sync.Mutex
	initialized bool
	closed      bool
	writer      application.ApplicationEffectWriter
	effectSeq   uint64
	now         func() time.Time
	instances   map[string]*instanceState
}

var _ application.ApplicationServer = (*Service)(nil)

// ApplicationID returns the manifest application id.
func ApplicationID() string { return pluginIDValue }

// Version returns the manifest application version.
func Version() string { return pluginVersion }

// New returns a fresh, uninitialized Scheduled Compartment service.
func New() *Service {
	return &Service{
		pluginID:  pluginIDValue,
		version:   pluginVersion,
		now:       time.Now,
		instances: map[string]*instanceState{},
	}
}

// ---------------------------------------------------------------------------
// Lifecycle / handshake
// ---------------------------------------------------------------------------

// Initialize negotiates the application protocol version and pins a runtime id.
func (s *Service) Initialize(_ context.Context, req *application.InitializeRequest) (*application.InitializeResponse, error) {
	if req == nil {
		return nil, status.Errorf(status.CodeInvalidArgument, "nil initialize request")
	}
	negotiated := uint32(0)
	if req.ProtocolVersion == application.ProtocolVersion {
		negotiated = application.ProtocolVersion
	} else {
		for _, v := range req.SupportedProtocolVersions {
			if v == application.ProtocolVersion {
				negotiated = v
				break
			}
		}
	}
	if negotiated == 0 {
		return nil, status.Errorf(status.CodeInvalidArgument, "unsupported application protocol version %d", req.ProtocolVersion)
	}

	s.mu.Lock()
	s.initialized = true
	if strings.TrimSpace(s.runtimeID) == "" {
		s.runtimeID = fmt.Sprintf("scheduled-compartment-%d", time.Now().UnixNano())
	}
	runtimeID := s.runtimeID
	s.mu.Unlock()

	return &application.InitializeResponse{
		NegotiatedProtocolVersion: negotiated,
		Status:                    status.New(),
		RuntimeID:                 runtimeID,
	}, nil
}

// Describe returns the stable application descriptor. It is the machine source
// of truth for requirements and jobs and must mirror plugin.yaml.
func (s *Service) Describe(context.Context) (*application.ApplicationDescriptor, error) {
	return &application.ApplicationDescriptor{
		ApplicationID:  s.pluginID,
		Version:        s.version,
		SchemaVersions: []string{application.SchemaVersion},
		Requirements: []application.RequirementDescriptor{
			{ID: "reminder-output", Capability: buzzerCap, Cardinality: "one"},
			{ID: "compartments", Capability: keyCap, Cardinality: "one-or-more", MinItems: 3},
			{ID: "local-display", Capability: displayCap, Cardinality: "zero-or-one"},
		},
		Jobs: []application.JobDescriptor{
			{ID: jobWindowCheck, Title: "Open due windows and schedule deadline checks", InputSchemaJSON: `{"type":"object","properties":{"window_id":{"type":"string"}}}`},
		},
		DeclarativeOnly: false,
	}, nil
}

// ConfigureInstance parses and validates the bounded config for one instance.
func (s *Service) ConfigureInstance(_ context.Context, req *application.ConfigureInstanceRequest) (*application.ConfigureInstanceResponse, error) {
	if req == nil {
		return nil, status.Errorf(status.CodeInvalidArgument, "nil configure request")
	}
	cfg, err := UnmarshalConfig(req.Config)
	if err != nil {
		return &application.ConfigureInstanceResponse{
			PluginInstanceID: req.PluginInstanceID,
			AppliedRevision:  0,
			Status:           status.Errorf(status.CodeInvalidArgument, "%v", err),
		}, nil
	}

	s.mu.Lock()
	st := s.instance(req.PluginInstanceID)
	st.config = &cfg
	st.configRev = req.ConfigRevision
	s.mu.Unlock()

	return &application.ConfigureInstanceResponse{
		PluginInstanceID: req.PluginInstanceID,
		AppliedRevision:  req.ConfigRevision,
		Status:           status.New(),
	}, nil
}

// ValidateBinding checks bindings against the declared requirements and, when
// valid, stores them for event processing.
func (s *Service) ValidateBinding(_ context.Context, req *application.ValidateBindingRequest) (*application.ValidateBindingResponse, error) {
	if req == nil {
		return nil, status.Errorf(status.CodeInvalidArgument, "nil validate request")
	}
	issues := validateBindings(req.Bindings)
	valid := len(issues) == 0
	if valid {
		s.mu.Lock()
		s.instance(req.PluginInstanceID).bindings = groupBindings(req.Bindings)
		s.mu.Unlock()
	}
	return &application.ValidateBindingResponse{Valid: valid, Issues: issues}, nil
}

// ---------------------------------------------------------------------------
// Event stream
// ---------------------------------------------------------------------------

// HandleEvents is the bidi event/effect stream. Core sends events here and the
// plugin emits Core-approved effects back over the same stream.
func (s *Service) HandleEvents(ctx context.Context, events application.ApplicationEventReader, effects application.ApplicationEffectWriter) error {
	if events == nil {
		return status.Errorf(status.CodeInvalidArgument, "nil event reader")
	}
	s.mu.Lock()
	s.writer = effects
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.writer = nil
		s.mu.Unlock()
	}()

	for {
		ev, err := events.Recv(ctx)
		if err != nil {
			if err == io.EOF || err == context.Canceled || err == context.DeadlineExceeded {
				return nil
			}
			return err
		}
		if err := s.handleEvent(ev); err != nil {
			return err
		}
	}
}

func (s *Service) handleEvent(ev *application.ApplicationEvent) error {
	if ev == nil || ev.Union == nil {
		return nil
	}
	instanceID := ev.PluginInstanceID

	s.mu.Lock()
	st := s.instance(instanceID)
	if ev.Sequence != 0 {
		if ev.Sequence <= st.lastSeq {
			s.mu.Unlock()
			return nil // duplicate event; idempotent
		}
		st.lastSeq = ev.Sequence
	}
	s.mu.Unlock()

	switch u := ev.Union.(type) {
	case *application.ScheduleTick:
		// Core no longer emits window ticks. Keep the event harmless for wire
		// compatibility; window-check owns the schedule state machine.
		return nil
	case *application.CapabilityEvent:
		return s.onCapabilityEvent(instanceID, u)
	case *application.RequestCompleted:
		return s.onRequestCompleted(instanceID, u)
	case *application.InstanceLifecycle:
		return nil
	default:
		return nil
	}
}

func (s *Service) onCapabilityEvent(instanceID string, ev *application.CapabilityEvent) error {
	if ev == nil {
		return nil
	}
	switch ev.EventType {
	case keyPressEvent:
		return s.onKeyEvent(instanceID, ev)
	default:
		return nil
	}
}

// onKeyEvent interprets a key press on a bound compartment entity as the user
// confirming that compartment. The occurrence is reconstructed from the event
// time and configuration, so a plugin restart does not discard a confirmation
// that still falls inside the configured window.
func (s *Service) onKeyEvent(instanceID string, ev *application.CapabilityEvent) error {
	s.mu.Lock()
	st := s.instance(instanceID)
	compID := keyToCompartment(st, ev.EntityID)
	if compID == "" {
		s.mu.Unlock()
		return nil // not a bound compartment entity
	}
	occurred := parseOccurred(ev.OccurredAt)
	if occurred.IsZero() {
		occurred = s.now()
	}
	occurrence, ok := windowForCompartmentAt(st, compID, occurred)
	if !ok {
		s.mu.Unlock()
		return nil // press is outside a configured window; idempotent
	}
	key := occurrenceKey(occurrence.OccurrenceID)
	if existing := st.windows[key]; existing != nil {
		if existing.State != windowOpened {
			s.mu.Unlock()
			return nil // already completed or missed
		}
		occurrence = existing
	}
	occurrence.State = windowCompleted
	occurrence.ClosedAt = occurred
	st.windows[key] = occurrence
	effects := []application.ApplicationEffectUnion{
		windowRecord(occurrence),
		cancelMissTaskEffect(occurrence.OccurrenceID),
	}
	s.mu.Unlock()

	return s.flush(instanceID, effects)
}

func (s *Service) onRequestCompleted(_ string, ev *application.RequestCompleted) error {
	// RequestCompleted acknowledges the reminder command lifecycle. It does not
	// change the schedule state machine: only a key press completes a window
	// and the durable miss task records an expired window. It is handled so the
	// stream stays healthy and is intentionally a no-op for state.
	_ = ev
	return nil
}
func (s *Service) flush(instanceID string, effects []application.ApplicationEffectUnion) error {
	for _, u := range effects {
		if err := s.sendEffect(instanceID, u); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) sendEffect(instanceID string, union application.ApplicationEffectUnion) error {
	s.mu.Lock()
	writer := s.writer
	if s.closed {
		s.mu.Unlock()
		return status.Errorf(status.CodeUnavailable, "plugin is shutting down")
	}
	s.effectSeq++
	seq := s.effectSeq
	s.mu.Unlock()
	if writer == nil {
		return nil // no active stream; nothing to emit
	}
	eff := &application.ApplicationEffect{
		PluginInstanceID: instanceID,
		Sequence:         seq,
		SchemaVersion:    application.SchemaVersion,
		Union:            union,
	}
	return writer.Send(context.Background(), eff)
}

// ---------------------------------------------------------------------------
// HTTP subroute / jobs / health / shutdown
// ---------------------------------------------------------------------------

// HandleRequest serves the plugin-scoped HTTP subroute. It is read-only and
// returns a bounded JSON summary of the instance config and window state.
func (s *Service) HandleRequest(_ context.Context, req *application.PluginHTTPRequest) (*application.PluginHTTPResponse, error) {
	if req == nil {
		return nil, status.Errorf(status.CodeInvalidArgument, "nil http request")
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, status.Errorf(status.CodeUnavailable, "plugin is shutting down")
	}
	st := s.instance(req.Context.InstanceID)
	code := uint32(200)
	body := []byte("{}")
	if req.Method == "GET" {
		statusBody := map[string]any{
			"instance_id": req.Context.InstanceID,
		}
		if st.config != nil {
			statusBody["timezone"] = st.config.Timezone
			statusBody["compartments"] = len(st.config.Compartments)
			statusBody["windows"] = len(st.windows)
			statusBody["active"] = activeCount(st)
		} else {
			statusBody["configured"] = false
		}
		body, _ = json.Marshal(statusBody)
	} else {
		code = 405
	}
	s.mu.Unlock()

	return &application.PluginHTTPResponse{
		StatusCode: code,
		Headers:    map[string]string{"content-type": "application/json"},
		Body:       body,
	}, nil
}

// RunJob executes either the automatic window opener or one durable miss
// check. Core dispatches durable schedule_job rows with JobID == ScheduleID, so
// miss checks arrive as window-miss-<occurrence>. Keeping opening and missing
// as separate jobs removes the old automatic-vs-durable duplicate dispatch.
func (s *Service) RunJob(_ context.Context, req *application.RunJobRequest) (*application.RunJobResponse, error) {
	if req == nil {
		return nil, status.Errorf(status.CodeInvalidArgument, "nil job request")
	}
	switch {
	case req.JobID == jobWindowCheck:
		return s.runWindowCheck(req)
	case strings.HasPrefix(req.JobID, jobWindowMissPrefix):
		return s.runWindowMiss(req)
	default:
		return nil, status.Errorf(status.CodeUnimplemented, "job %q is not implemented", req.JobID)
	}
}

// runWindowCheck is Core's automatic minute job. It opens each configured
// occurrence once and, for a fresh open, arms a durable miss task whose id is
// scoped to the occurrence date. Late observations are recorded without
// replaying a stale reminder or re-arming a cancelled miss task.
func (s *Service) runWindowCheck(req *application.RunJobRequest) (*application.RunJobResponse, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, status.Errorf(status.CodeUnavailable, "plugin is shutting down")
	}
	st := s.instance(req.PluginInstanceID)
	if st.config == nil {
		s.mu.Unlock()
		return &application.RunJobResponse{JobID: req.JobID, Status: status.New(), ResultJSON: resultJSON(nil)}, nil
	}
	if req.IdempotencyKey != "" {
		if prev, ok := st.jobs[req.IdempotencyKey]; ok {
			s.mu.Unlock()
			return &application.RunJobResponse{JobID: req.JobID, Status: status.New(), ResultJSON: prev}, nil
		}
	}

	cfg := st.config
	tz, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		s.mu.Unlock()
		return &application.RunJobResponse{JobID: req.JobID, Status: status.New(), ResultJSON: resultJSON(nil)}, nil
	}
	now := s.now()
	local := now.In(tz)
	today := local.Format("2006-01-02")
	for key, w := range st.windows {
		if w == nil || w.Start.In(tz).Format("2006-01-02") != today {
			delete(st.windows, key)
		}
	}

	var missed []string
	var effects []application.ApplicationEffectUnion
	for _, spec := range cfg.Schedule {
		startClock, startOK := parseHHMM(spec.Start)
		endClock, endOK := parseHHMM(spec.End)
		if !startOK || !endOK {
			continue
		}
		start := time.Date(local.Year(), local.Month(), local.Day(), startClock.Hour(), startClock.Minute(), 0, 0, tz)
		end := time.Date(local.Year(), local.Month(), local.Day(), endClock.Hour(), endClock.Minute(), 0, 0, tz)
		if !end.After(start) {
			continue
		}

		occurrenceID := makeOccurrenceID(today, spec.ID)
		key := occurrenceKey(occurrenceID)
		w := st.windows[key]
		if now.Before(start) {
			continue
		}
		if now.Before(end) {
			if w != nil {
				continue // already opened or completed for this occurrence
			}
			w = &windowTrack{
				ID: spec.ID, OccurrenceID: occurrenceID, Compartment: spec.Compartment,
				Start: start, End: end, State: windowOpened, OpenedAt: now,
				ReminderEntity: reminderEntity(st),
			}
			st.windows[key] = w
			effects = append(effects, windowRecord(w))

			// A fresh minute boundary is the only safe time to replay the
			// reminder. After that the observation may be a restart; record the
			// occurrence but do not reactivate a cancelled miss task.
			if !now.After(start.Add(windowFreshOpenGrace)) {
				if w.ReminderEntity != "" {
					effects = append(effects, reminderEffect(st, w))
				}
				w.MissTaskScheduled = true
				effects = append(effects, missTaskEffect(w))
			}
			continue
		}
		if w == nil || w.State != windowOpened || w.MissTaskScheduled {
			continue // no in-memory occurrence, already closed, or durable miss owns it
		}

		// Fallback for a late-started occurrence that never armed a durable
		// miss task. Restarts have no in-memory w and therefore do not produce
		// a false missed record.
		w.State = windowMissed
		w.ClosedAt = now
		missed = append(missed, spec.ID)
		effects = append(effects, windowRecord(w), cancelMissTaskEffect(w.OccurrenceID), missedNotificationEffect(w))
	}
	sort.Strings(missed)
	body := resultJSON(missed)
	if req.IdempotencyKey != "" {
		st.jobs[req.IdempotencyKey] = body
	}
	s.mu.Unlock()

	for _, u := range effects {
		if err := s.sendEffect(req.PluginInstanceID, u); err != nil {
			return nil, err
		}
	}
	return &application.RunJobResponse{JobID: req.JobID, Status: status.New(), ResultJSON: body}, nil
}

// runWindowMiss is the durable deadline check armed when a window opens. Its
// schedule id and effect ids are occurrence-qualified, so a restart cannot
// confuse today's miss with another day. Completion cancels this task, making
// that cancellation the durable "completed" fact available through the
// existing Application Protocol.
func (s *Service) runWindowMiss(req *application.RunJobRequest) (*application.RunJobResponse, error) {
	var args struct {
		WindowID       string `json:"window_id"`
		OccurrenceDate string `json:"occurrence_date"`
	}
	if err := json.Unmarshal([]byte(req.ArgsJSON), &args); err != nil {
		return nil, status.Errorf(status.CodeInvalidArgument, "invalid window-miss args: %v", err)
	}
	args.WindowID = strings.TrimSpace(args.WindowID)
	args.OccurrenceDate = strings.TrimSpace(args.OccurrenceDate)
	if args.WindowID == "" || args.OccurrenceDate == "" {
		return nil, status.Errorf(status.CodeInvalidArgument, "window_id and occurrence_date are required")
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, status.Errorf(status.CodeUnavailable, "plugin is shutting down")
	}
	st := s.instance(req.PluginInstanceID)
	if st.config == nil {
		s.mu.Unlock()
		return &application.RunJobResponse{JobID: req.JobID, Status: status.New(), ResultJSON: resultJSON(nil)}, nil
	}
	if req.IdempotencyKey != "" {
		if prev, ok := st.jobs[req.IdempotencyKey]; ok {
			s.mu.Unlock()
			return &application.RunJobResponse{JobID: req.JobID, Status: status.New(), ResultJSON: prev}, nil
		}
	}

	cfg := st.config
	tz, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		s.mu.Unlock()
		return &application.RunJobResponse{JobID: req.JobID, Status: status.New(), ResultJSON: resultJSON(nil)}, nil
	}
	var spec *WindowSpec
	for i := range cfg.Schedule {
		if cfg.Schedule[i].ID == args.WindowID {
			spec = &cfg.Schedule[i]
			break
		}
	}
	if spec == nil {
		s.mu.Unlock()
		return nil, status.Errorf(status.CodeInvalidArgument, "window %q is not configured", args.WindowID)
	}
	date, err := time.ParseInLocation("2006-01-02", args.OccurrenceDate, tz)
	if err != nil {
		s.mu.Unlock()
		return nil, status.Errorf(status.CodeInvalidArgument, "occurrence_date %q is invalid: %v", args.OccurrenceDate, err)
	}
	startClock, startOK := parseHHMM(spec.Start)
	endClock, endOK := parseHHMM(spec.End)
	if !startOK || !endOK {
		s.mu.Unlock()
		return nil, status.Errorf(status.CodeInvalidArgument, "window %q has an invalid schedule", spec.ID)
	}
	start := time.Date(date.Year(), date.Month(), date.Day(), startClock.Hour(), startClock.Minute(), 0, 0, tz)
	end := time.Date(date.Year(), date.Month(), date.Day(), endClock.Hour(), endClock.Minute(), 0, 0, tz)
	occurrenceID := makeOccurrenceID(args.OccurrenceDate, spec.ID)
	if req.JobID != missScheduleID(occurrenceID) {
		s.mu.Unlock()
		return nil, status.Errorf(status.CodeInvalidArgument, "job %q does not match occurrence %q", req.JobID, occurrenceID)
	}
	now := s.now()
	if now.Before(end) {
		s.mu.Unlock()
		return &application.RunJobResponse{JobID: req.JobID, Status: status.New(), ResultJSON: resultJSON(nil)}, nil
	}

	key := occurrenceKey(occurrenceID)
	w := st.windows[key]
	var effects []application.ApplicationEffectUnion
	var missed []string
	if w != nil && w.State == windowCompleted {
		effects = append(effects, cancelMissTaskEffect(occurrenceID))
	} else if w != nil && w.State == windowMissed {
		// already converged; cancellation was emitted by the first run
	} else {
		if w == nil {
			w = &windowTrack{
				ID: spec.ID, OccurrenceID: occurrenceID, Compartment: spec.Compartment,
				Start: start, End: end, State: windowMissed, ClosedAt: now,
			}
			st.windows[key] = w
		} else {
			w.State = windowMissed
			w.ClosedAt = now
		}
		missed = append(missed, spec.ID)
		effects = append(effects, windowRecord(w), cancelMissTaskEffect(occurrenceID), missedNotificationEffect(w))
	}
	sort.Strings(missed)
	body := resultJSON(missed)
	if req.IdempotencyKey != "" {
		st.jobs[req.IdempotencyKey] = body
	}
	s.mu.Unlock()

	for _, u := range effects {
		if err := s.sendEffect(req.PluginInstanceID, u); err != nil {
			return nil, err
		}
	}
	return &application.RunJobResponse{JobID: req.JobID, Status: status.New(), ResultJSON: body}, nil
}

// Health reports serving state per configured instance.
func (s *Service) Health(context.Context) (*application.HealthResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := application.HealthStateServing
	if s.closed {
		state = application.HealthStateNotServing
	}
	insts := make([]application.InstanceHealth, 0, len(s.instances))
	for id := range s.instances {
		insts = append(insts, application.InstanceHealth{PluginInstanceID: id, State: state})
	}
	return &application.HealthResponse{State: state, Instances: insts}, nil
}

// Shutdown marks the service as closed. Subsequent RPCs fail grace-fully.
func (s *Service) Shutdown(_ context.Context, _ *application.ShutdownRequest) (*application.ShutdownResponse, error) {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return &application.ShutdownResponse{Status: status.New()}, nil
}

// ---------------------------------------------------------------------------
// helper functions
// ---------------------------------------------------------------------------

func (s *Service) instance(id string) *instanceState {
	if s.instances == nil {
		s.instances = map[string]*instanceState{}
	}
	st, ok := s.instances[id]
	if !ok {
		st = &instanceState{
			bindings: map[string][]string{},
			windows:  map[string]*windowTrack{},
			jobs:     map[string]string{},
		}
		s.instances[id] = st
	}
	return st
}

func reminderEffect(st *instanceState, w *windowTrack) *application.RequestCommand {
	policy := defaultReminder
	if st != nil && st.config != nil {
		policy = st.config.ResolvedReminder()
	}
	return &application.RequestCommand{
		EntityID:       w.ReminderEntity,
		Action:         buzzerAction,
		ArgsJSON:       mustJSON(map[string]int{"freq": policy.Freq, "duration": policy.Duration}),
		IdempotencyKey: "reminder-" + w.OccurrenceID,
		Deadline:       w.End.UTC().Format(time.RFC3339),
	}
}

func missTaskEffect(w *windowTrack) *application.ScheduleTask {
	return &application.ScheduleTask{
		ScheduleID: missScheduleID(w.OccurrenceID),
		Cron:       windowCheckCron,
		PayloadJSON: mustJSON(map[string]string{
			"window_id":       w.ID,
			"occurrence_date": w.Start.Format("2006-01-02"),
			"occurrence_id":   w.OccurrenceID,
		}),
	}
}

func cancelMissTaskEffect(occurrenceID string) *application.CancelScheduledTask {
	return &application.CancelScheduledTask{ScheduleID: missScheduleID(occurrenceID)}
}

// validateBindings enforces the declared requirement cardinalities and rejects
// any requirement id that is not part of this application (which structurally
// rules out Driver coupling).
func validateBindings(bindings []application.Binding) []application.BindingIssue {
	allowed := map[string]struct{}{"reminder-output": {}, "compartments": {}, "local-display": {}}
	counts := map[string]int{}
	entityOK := map[string]bool{}

	var issues []application.BindingIssue

	for _, b := range bindings {
		if _, ok := allowed[b.RequirementID]; !ok {
			issues = append(issues, application.BindingIssue{
				RequirementID: b.RequirementID,
				Severity:      "error",
				Message:       fmt.Sprintf("requirement %q is not declared by this application", b.RequirementID),
			})
			continue
		}
		counts[b.RequirementID]++
		if strings.TrimSpace(b.EntityID) == "" {
			issues = append(issues, application.BindingIssue{
				RequirementID: b.RequirementID,
				Severity:      "error",
				Message:       "entity_id must not be empty",
			})
		}
		if b.RequirementID == "compartments" {
			key := b.RequirementID + "\x00" + b.EntityID
			if entityOK[key] {
				issues = append(issues, application.BindingIssue{
					RequirementID: b.RequirementID,
					Severity:      "error",
					Message:       fmt.Sprintf("duplicate compartment entity %q", b.EntityID),
				})
			}
			entityOK[key] = true

		}
	}

	if counts["reminder-output"] != 1 {
		issues = append(issues, application.BindingIssue{
			RequirementID: "reminder-output",
			Severity:      "error",
			Message:       fmt.Sprintf("reminder-output requires exactly one binding, got %d", counts["reminder-output"]),
		})
	}
	if counts["compartments"] < 3 {
		issues = append(issues, application.BindingIssue{
			RequirementID: "compartments",
			Severity:      "error",
			Message:       fmt.Sprintf("compartments requires at least 3 bindings, got %d", counts["compartments"]),
		})
	}
	if counts["local-display"] > 1 {
		issues = append(issues, application.BindingIssue{
			RequirementID: "local-display",
			Severity:      "error",
			Message:       "local-display allows at most one binding",
		})
	}
	return issues
}

func groupBindings(bindings []application.Binding) map[string][]string {
	out := map[string][]string{}
	for _, b := range bindings {
		out[b.RequirementID] = append(out[b.RequirementID], b.EntityID)
	}
	return out
}

func reminderEntity(st *instanceState) string {
	if st == nil {
		return ""
	}
	if ids := st.bindings["reminder-output"]; len(ids) > 0 {
		return ids[0]
	}
	return ""
}

func keyToCompartment(st *instanceState, entity string) string {
	if st == nil || st.config == nil {
		return ""
	}
	keys := st.bindings["compartments"]
	comps := st.config.Compartments
	for i, c := range keys {
		if i >= len(comps) {
			break
		}
		if c == entity {
			return comps[i].ID
		}
	}
	return ""
}

// keyToCompartment maps a bound key entity to its configured compartment by
// binding order. The application gives business meaning to the key: the
// Driver only reports a generic key press.

// windowForCompartmentAt reconstructs the configured occurrence containing at.
// It is the restart-safe fallback for key confirmation: no read API is needed
// because the event timestamp and bounded config are sufficient to identify
// the occurrence.
func windowForCompartmentAt(st *instanceState, compID string, at time.Time) (*windowTrack, bool) {
	if st == nil || st.config == nil {
		return nil, false
	}
	tz, err := time.LoadLocation(st.config.Timezone)
	if err != nil {
		return nil, false
	}
	local := at.In(tz)
	date := local.Format("2006-01-02")
	for _, spec := range st.config.Schedule {
		if spec.Compartment != compID {
			continue
		}
		startClock, startOK := parseHHMM(spec.Start)
		endClock, endOK := parseHHMM(spec.End)
		if !startOK || !endOK {
			continue
		}
		start := time.Date(local.Year(), local.Month(), local.Day(), startClock.Hour(), startClock.Minute(), 0, 0, tz)
		end := time.Date(local.Year(), local.Month(), local.Day(), endClock.Hour(), endClock.Minute(), 0, 0, tz)
		if at.Before(start) || !at.Before(end) {
			continue
		}
		return &windowTrack{
			ID: spec.ID, OccurrenceID: makeOccurrenceID(date, spec.ID), Compartment: spec.Compartment,
			Start: start, End: end, State: windowOpened, ReminderEntity: reminderEntity(st),
		}, true
	}
	return nil, false
}

func makeOccurrenceID(date, windowID string) string { return windowID + "@" + date }

func occurrenceKey(occurrenceID string) string { return occurrenceID }

func missScheduleID(occurrenceID string) string { return jobWindowMissPrefix + occurrenceID }

func activeCount(st *instanceState) int {
	n := 0
	for _, w := range st.windows {
		if w.State == windowOpened {
			n++
		}
	}
	return n
}

func parseOccurred(s string) time.Time {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(s))
	if err != nil {
		return time.Time{}
	}
	return t
}

func windowRecord(w *windowTrack) *application.UpsertDomainRecord {
	data := map[string]any{
		"id":              w.ID,
		"occurrence_id":   w.OccurrenceID,
		"compartment":     w.Compartment,
		"start":           w.Start.UTC().Format(time.RFC3339),
		"end":             w.End.UTC().Format(time.RFC3339),
		"state":           w.State,
		"opened_at":       optTime(w.OpenedAt),
		"closed_at":       optTime(w.ClosedAt),
		"reminder_entity": w.ReminderEntity,
	}
	return &application.UpsertDomainRecord{
		RecordType: "window",
		RecordID:   w.OccurrenceID,
		DataJSON:   mustJSON(data),
		Version:    "1",
	}
}

func missedNotificationEffect(w *windowTrack) *application.SendNotification {
	return &application.SendNotification{
		Title:    "Scheduled window missed",
		Body:     fmt.Sprintf("Window %s for compartment %s was not completed in time", w.ID, w.Compartment),
		Severity: "warning",
	}
}

func resultJSON(ids []string) string {
	if ids == nil {
		ids = []string{}
	}
	return mustJSON(map[string]any{"missed": ids})
}

func optTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
