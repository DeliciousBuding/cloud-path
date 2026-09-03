package appruntime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	coreapplication "github.com/DeliciousBuding/cloud-path/internal/application"
	sdkapplication "github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/application"
)

const (
	defaultEventQueueSize  = 64
	defaultShutdownTimeout = 5 * time.Second
)

// Runtime manages Application Instances and their SDK service sessions. It is
// safe for concurrent use.
type Runtime struct {
	opts RuntimeOptions

	mu        sync.Mutex
	ctx       context.Context
	cancel    context.CancelFunc
	instances map[string]*instanceRecord
	closed    bool
}

type instanceRecord struct {
	spec  InstanceSpec
	appID string

	cli          sdkapplication.ApplicationClient
	stream       sdkapplication.ApplicationEventStream
	descriptor   *sdkapplication.ApplicationDescriptor
	requirements []coreapplication.Requirement

	mu       sync.Mutex
	state    InstanceState
	err      error
	seq      uint64
	executed map[string]bool
	rejected int

	cancel context.CancelFunc
	runCtx context.Context
	done   chan struct{}
	events chan *sdkapplication.ApplicationEvent
	wg     sync.WaitGroup
}

// NewRuntime builds a Runtime and validates the required options.
func NewRuntime(opts RuntimeOptions) (*Runtime, error) {
	if opts.Dialer == nil {
		return nil, errors.New("appruntime: dialer is required")
	}
	if opts.Executor == nil {
		return nil, errors.New("appruntime: effect executor is required")
	}
	if opts.EventQueueSize <= 0 {
		opts.EventQueueSize = defaultEventQueueSize
	}
	if opts.ShutdownTimeout <= 0 {
		opts.ShutdownTimeout = defaultShutdownTimeout
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	parent := opts.Context
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	return &Runtime{
		opts:      opts,
		ctx:       ctx,
		cancel:    cancel,
		instances: map[string]*instanceRecord{},
	}, nil
}

// StartInstance performs Initialize, Describe, ConfigureInstance, SDK
// ValidateBinding and authoritative Binder validation, then starts the event
// stream. It never starts an instance whose binding is invalid or whose
// required capabilities are missing.
func (r *Runtime) StartInstance(ctx context.Context, spec InstanceSpec) (*Instance, error) {
	if err := validateSpec(spec); err != nil {
		return nil, err
	}

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, ErrRuntimeClosed
	}
	if _, ok := r.instances[spec.PluginInstanceID]; ok {
		r.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrInstanceExists, spec.PluginInstanceID)
	}
	rec := newInstanceRecord(spec, r.opts.EventQueueSize)
	r.instances[spec.PluginInstanceID] = rec
	r.mu.Unlock()

	if err := r.startRecord(ctx, rec); err != nil {
		rec.mu.Lock()
		rec.state = StateFailed
		rec.err = err
		rec.mu.Unlock()
		close(rec.done)
		return rec.snapshot(), err
	}
	return rec.snapshot(), nil
}

func newInstanceRecord(spec InstanceSpec, queueSize int) *instanceRecord {
	if queueSize <= 0 {
		queueSize = defaultEventQueueSize
	}
	return &instanceRecord{
		spec:     spec,
		executed: map[string]bool{},
		state:    StateCreated,
		done:     make(chan struct{}),
		events:   make(chan *sdkapplication.ApplicationEvent, queueSize),
	}
}

func (r *Runtime) startRecord(ctx context.Context, rec *instanceRecord) error {
	cli, err := r.opts.Dialer(rec.spec.PluginID)
	if err != nil {
		return fmt.Errorf("dial plugin %s: %w", rec.spec.PluginID, err)
	}
	rec.cli = cli

	initResp, err := cli.Initialize(ctx, initializeRequest(rec.spec))
	if err != nil {
		return fmt.Errorf("initialize plugin %s: %w", rec.spec.PluginID, err)
	}
	if initResp != nil && initResp.Status != nil && !initResp.Status.IsOK() {
		return fmt.Errorf("initialize plugin %s: %w", rec.spec.PluginID, initResp.Status)
	}

	desc, err := cli.Describe(ctx)
	if err != nil {
		return fmt.Errorf("describe plugin %s: %w", rec.spec.PluginID, err)
	}
	if desc == nil {
		return fmt.Errorf("describe plugin %s: nil descriptor", rec.spec.PluginID)
	}
	rec.descriptor = desc
	rec.requirements = requirementsFromDescriptor(desc)

	rec.appID = rec.spec.ApplicationID
	if rec.appID == "" {
		rec.appID = desc.ApplicationID
	}
	if rec.appID == "" {
		return errors.New("appruntime: application id is missing from spec and descriptor")
	}

	vbResp, err := cli.ValidateBinding(ctx, &sdkapplication.ValidateBindingRequest{
		PluginInstanceID: rec.spec.PluginInstanceID,
		Bindings:         toSDKBindings(rec.spec.Bindings),
	})
	if err != nil {
		return fmt.Errorf("plugin validate binding: %w", err)
	}
	if vbResp == nil || !vbResp.Valid {
		return invalidBindingError(vbResp)
	}

	binder := coreapplication.Binder{
		ApplicationID:    rec.appID,
		PluginInstanceID: rec.spec.PluginInstanceID,
		TenantID:         rec.spec.TenantID,
	}
	if res := binder.Validate(rec.requirements, rec.spec.Candidates, rec.spec.Bindings); !res.Valid {
		return &coreapplication.BindingError{Result: res}
	}

	cfgResp, err := cli.ConfigureInstance(ctx, &sdkapplication.ConfigureInstanceRequest{
		PluginInstanceID: rec.spec.PluginInstanceID,
		Config:           rec.spec.Config,
		ConfigRevision:   rec.spec.ConfigRevision,
	})
	if err != nil {
		return fmt.Errorf("configure instance %s: %w", rec.spec.PluginInstanceID, err)
	}
	if cfgResp != nil && cfgResp.Status != nil && !cfgResp.Status.IsOK() {
		return fmt.Errorf("configure instance %s: %w", rec.spec.PluginInstanceID, cfgResp.Status)
	}

	stream, err := cli.HandleEvents(ctx)
	if err != nil {
		return fmt.Errorf("open event stream for %s: %w", rec.spec.PluginInstanceID, err)
	}
	if stream == nil {
		return fmt.Errorf("open event stream for %s: nil stream", rec.spec.PluginInstanceID)
	}
	rec.stream = stream

	recCtx, cancel := context.WithCancel(r.ctx)
	rec.cancel = cancel
	rec.runCtx = recCtx
	rec.wg.Add(2)
	go r.senderLoop(recCtx, rec)
	go r.recvLoop(recCtx, rec)
	go func() {
		rec.wg.Wait()
		close(rec.done)
	}()

	rec.mu.Lock()
	rec.state = StateRunning
	rec.mu.Unlock()
	return nil
}

func (r *Runtime) senderLoop(ctx context.Context, rec *instanceRecord) {
	defer rec.wg.Done()
	for {
		select {
		case ev := <-rec.events:
			if ev == nil {
				return
			}
			if err := rec.stream.Send(ctx, ev); err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}
				rec.fail(fmt.Errorf("send event to %s: %w", rec.spec.PluginInstanceID, err))
				return
			}
		case <-ctx.Done():
			// Graceful close: half-close our send side so the plugin's event
			// handler observes EOF instead of hanging forever.
			closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = rec.stream.CloseSend(closeCtx)
			cancel()
			return
		}
	}
}

func (r *Runtime) recvLoop(ctx context.Context, rec *instanceRecord) {
	defer rec.wg.Done()
	src := EffectSource{
		PluginInstanceID: rec.spec.PluginInstanceID,
		TenantID:         rec.spec.TenantID,
		Bindings:         rec.spec.Bindings,
		Candidates:       rec.spec.Candidates,
	}
	for {
		raw, err := rec.stream.Recv(ctx)
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
				rec.fail(fmt.Errorf("receive effect from %s: %w", rec.spec.PluginInstanceID, err))
			}
			return
		}
		effect, err := EffectFromSDK(raw, src)
		if err != nil {
			rec.noteRejected()
			r.opts.Logger.Warn("application effect rejected", "instance", rec.spec.PluginInstanceID, "error", err)
			continue
		}
		if rr := r.executeOnce(rec, effect); rr.Err != nil {
			r.opts.Logger.Error("application effect execution failed", "instance", rec.spec.PluginInstanceID, "effect_id", effect.ID, "error", rr.Err)
		}
	}
}

// recCtx returns a context that is canceled when the instance stops. It is a
// small helper so Core executors observe instance shutdown.
func recCtx(rec *instanceRecord) context.Context {
	if rec.runCtx != nil {
		return rec.runCtx
	}
	return context.Background()
}

func (rec *instanceRecord) noteRejected() {
	rec.mu.Lock()
	rec.rejected++
	rec.mu.Unlock()
}

func (rec *instanceRecord) fail(err error) {
	rec.mu.Lock()
	if rec.state == StateRunning || rec.state == StateStarting || rec.state == StateCreated {
		rec.state = StateFailed
	}
	rec.err = err
	cancel := rec.cancel
	rec.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (rec *instanceRecord) snapshot() *Instance {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	inst := &Instance{
		ApplicationID:    rec.appID,
		PluginInstanceID: rec.spec.PluginInstanceID,
		PluginID:         rec.spec.PluginID,
		TenantID:         rec.spec.TenantID,
		State:            rec.state,
		Err:              rec.err,
		Descriptor:       rec.descriptor,
	}
	inst.Requirements = append([]coreapplication.Requirement(nil), rec.requirements...)
	inst.Bindings = append([]coreapplication.Binding(nil), rec.spec.Bindings...)
	return inst
}

// GetInstance returns a point-in-time snapshot of one instance.
func (r *Runtime) GetInstance(instanceID string) (*Instance, error) {
	rec, err := r.instance(instanceID)
	if err != nil {
		return nil, err
	}
	return rec.snapshot(), nil
}

// Describe returns the plugin descriptor recorded during startup.
func (r *Runtime) Describe(instanceID string) (*sdkapplication.ApplicationDescriptor, error) {
	rec, err := r.instance(instanceID)
	if err != nil {
		return nil, err
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return rec.descriptor, nil
}

// DispatchEvent enqueues one event for the instance's event stream. It blocks
// while the queue is full (backpressure) and returns on ctx cancellation or
// instance shutdown.
func (r *Runtime) DispatchEvent(ctx context.Context, instanceID string, event *sdkapplication.ApplicationEvent) error {
	if event == nil {
		return fmt.Errorf("%w: nil event", ErrInvalidEffect)
	}
	rec, err := r.instance(instanceID)
	if err != nil {
		return err
	}
	rec.mu.Lock()
	if rec.state != StateRunning && rec.state != StateStarting {
		rec.mu.Unlock()
		return ErrInstanceNotRunning
	}
	rec.seq++
	out := *event
	out.Sequence = rec.seq
	out.PluginInstanceID = rec.spec.PluginInstanceID
	if out.SchemaVersion == "" {
		out.SchemaVersion = sdkapplication.SchemaVersion
	}
	rec.mu.Unlock()

	select {
	case rec.events <- &out:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-rec.done:
		return ErrInstanceNotRunning
	}
}

// HandleRequest forwards an HTTP subroute request to the instance's plugin and
// injects the authoritative tenant/instance context.
func (r *Runtime) HandleRequest(ctx context.Context, instanceID string, req *sdkapplication.PluginHTTPRequest) (*sdkapplication.PluginHTTPResponse, error) {
	rec, err := r.instance(instanceID)
	if err != nil {
		return nil, err
	}
	if err := rec.ensureRunning(); err != nil {
		return nil, err
	}
	if req == nil {
		return nil, fmt.Errorf("%w: nil request", ErrInvalidEffect)
	}
	out := *req
	out.PluginInstanceID = rec.spec.PluginInstanceID
	switch out.Context.TenantID {
	case "":
		out.Context.TenantID = rec.spec.TenantID
	case rec.spec.TenantID:
	default:
		return nil, fmt.Errorf("%w: request tenant %q, instance tenant %q", ErrTenantMismatch, out.Context.TenantID, rec.spec.TenantID)
	}
	switch out.Context.InstanceID {
	case "":
		out.Context.InstanceID = rec.spec.PluginInstanceID
	case rec.spec.PluginInstanceID:
	default:
		return nil, fmt.Errorf("%w: request instance %q, instance %q", ErrTenantMismatch, out.Context.InstanceID, rec.spec.PluginInstanceID)
	}
	return rec.cli.HandleRequest(ctx, &out)
}

// RunJob forwards a job invocation to the instance's plugin.
func (r *Runtime) RunJob(ctx context.Context, instanceID string, req *sdkapplication.RunJobRequest) (*sdkapplication.RunJobResponse, error) {
	rec, err := r.instance(instanceID)
	if err != nil {
		return nil, err
	}
	if err := rec.ensureRunning(); err != nil {
		return nil, err
	}
	if req == nil {
		return nil, fmt.Errorf("%w: nil job request", ErrInvalidEffect)
	}
	out := *req
	out.PluginInstanceID = rec.spec.PluginInstanceID
	return rec.cli.RunJob(ctx, &out)
}

// Health forwards a health probe to the instance's plugin.
func (r *Runtime) Health(ctx context.Context, instanceID string) (*sdkapplication.HealthResponse, error) {
	rec, err := r.instance(instanceID)
	if err != nil {
		return nil, err
	}
	if err := rec.ensureRunning(); err != nil {
		return nil, err
	}
	return rec.cli.Health(ctx)
}

// StopInstance gracefully shuts one instance down.
func (r *Runtime) StopInstance(ctx context.Context, instanceID, reason string, grace time.Duration) error {
	rec, err := r.instance(instanceID)
	if err != nil {
		return err
	}
	return r.stopRecord(ctx, rec, reason, grace)
}

// Close shuts down the runtime and every instance. It is idempotent.
func (r *Runtime) Close(ctx context.Context) error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	recs := make([]*instanceRecord, 0, len(r.instances))
	for _, rec := range r.instances {
		recs = append(recs, rec)
	}
	r.mu.Unlock()

	r.cancel()
	var errs []error
	for _, rec := range recs {
		if err := r.stopRecord(ctx, rec, "runtime shutdown", 0); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (r *Runtime) stopRecord(ctx context.Context, rec *instanceRecord, reason string, grace time.Duration) error {
	rec.mu.Lock()
	if rec.state == StateStopped {
		rec.mu.Unlock()
		return nil
	}
	if rec.state != StateStopping {
		rec.state = StateStopping
	}
	cancel := rec.cancel
	rec.mu.Unlock()

	if grace <= 0 {
		grace = r.opts.ShutdownTimeout
	}
	var shutdownErr error
	if rec.cli != nil {
		shutCtx, shutCancel := context.WithTimeout(ctx, grace)
		_, shutdownErr = rec.cli.Shutdown(shutCtx, &sdkapplication.ShutdownRequest{
			Reason:       reason,
			GraceSeconds: uint32(grace.Seconds()),
		})
		shutCancel()
	}

	if cancel != nil {
		cancel()
	}
	if err := waitDone(ctx, rec.done, grace); err != nil {
		return err
	}
	rec.mu.Lock()
	rec.state = StateStopped
	rec.mu.Unlock()
	return shutdownErr
}

func (r *Runtime) instance(instanceID string) (*instanceRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.instances[instanceID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrInstanceNotFound, instanceID)
	}
	return rec, nil
}

func (rec *instanceRecord) ensureRunning() error {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.state != StateRunning {
		return fmt.Errorf("%w: state=%s", ErrInstanceNotRunning, rec.state)
	}
	return nil
}

func waitDone(ctx context.Context, done <-chan struct{}, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return fmt.Errorf("appruntime: shutdown timed out after %s", timeout)
	}
}

func validateSpec(spec InstanceSpec) error {
	var errs []error
	if spec.PluginInstanceID == "" {
		errs = append(errs, errors.New("plugin_instance_id must not be empty"))
	}
	if spec.PluginID == "" {
		errs = append(errs, errors.New("plugin_id must not be empty"))
	}
	if spec.TenantID == "" {
		errs = append(errs, errors.New("tenant_id must not be empty"))
	}
	if spec.EventQueueSize < 0 {
		errs = append(errs, errors.New("event queue size must be non-negative"))
	}
	return errors.Join(errs...)
}

func requirementsFromDescriptor(desc *sdkapplication.ApplicationDescriptor) []coreapplication.Requirement {
	if desc == nil {
		return nil
	}
	out := make([]coreapplication.Requirement, 0, len(desc.Requirements))
	for _, r := range desc.Requirements {
		out = append(out, coreapplication.Requirement{
			ID:          r.ID,
			Capability:  r.Capability,
			Cardinality: coreapplication.Cardinality(r.Cardinality),
			MinItems:    int(r.MinItems),
		})
	}
	return out
}

func toSDKBindings(bindings []coreapplication.Binding) []sdkapplication.Binding {
	if bindings == nil {
		return nil
	}
	out := make([]sdkapplication.Binding, 0, len(bindings))
	for _, b := range bindings {
		out = append(out, sdkapplication.Binding{RequirementID: b.RequirementID, EntityID: b.EntityID})
	}
	return out
}

func invalidBindingError(resp *sdkapplication.ValidateBindingResponse) error {
	if resp == nil {
		return errors.New("appruntime: plugin returned nil binding validation response")
	}
	msgs := make([]string, 0, len(resp.Issues))
	for _, issue := range resp.Issues {
		msgs = append(msgs, fmt.Sprintf("%s: %s", issue.RequirementID, issue.Message))
	}
	return fmt.Errorf("appruntime: plugin rejected binding: %s", strings.Join(msgs, "; "))
}

func initializeRequest(spec InstanceSpec) *sdkapplication.InitializeRequest {
	return &sdkapplication.InitializeRequest{
		PluginID:                  spec.PluginID,
		PluginVersion:             spec.PluginVersion,
		LaunchID:                  spec.LaunchID,
		ProtocolVersion:           sdkapplication.ProtocolVersion,
		SupportedProtocolVersions: []uint32{sdkapplication.ProtocolVersion},
		NodeID:                    spec.NodeID,
		RuntimeType:               "application-runtime",
		HostInfo:                  spec.HostInfo,
	}
}
