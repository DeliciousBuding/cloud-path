package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// Defaults applied when a ManagerOptions field is left zero.
const (
	defaultHealthCheckInterval    = 5 * time.Second
	defaultHealthFailureThreshold = 3
)

// Environment variables injected into a per-instance plugin process so it can
// tell which tenant/instance it is serving. Shared processes serve several
// instances and are therefore not given a single identity.
const (
	EnvTenant     = "CLOUDPATH_TENANT"
	EnvInstanceID = "CLOUDPATH_INSTANCE_ID"
)

// Sentinel errors returned by the Manager. Callers can compare with
// errors.Is to distinguish "does not exist" from other failures.
var (
	ErrInstallationNotFound = errors.New("pluginhost: installation not found")
	ErrInstallationExists   = errors.New("pluginhost: installation already registered")
	ErrInstanceNotFound     = errors.New("pluginhost: instance not found")
	ErrInstanceExists       = errors.New("pluginhost: instance already exists")
	ErrInvalidArgument      = errors.New("pluginhost: invalid argument")
)

// Installation is one installed version of a plugin on this node. It is the
// node-level artifact that PluginInstance values are bound to; the same plugin
// may have multiple versions installed side by side for rolling migrations.
type Installation struct {
	PluginID string
	Version  string
	Path     string
	// Kind selects the protocol client the host builds for this installation.
	// A zero value means KindDriver.
	Kind Kind
}

// Isolation selects whether an instance shares a plugin process with other
// instances of the same installation, or gets its own dedicated process.
type Isolation uint8

const (
	// IsolationShared is the default: one process per installation serves all
	// of its instances.
	IsolationShared Isolation = iota
	// IsolationPerInstance gives the instance its own plugin process.
	IsolationPerInstance
)

// String returns the stable, canonical isolation name.
func (i Isolation) String() string {
	switch i {
	case IsolationShared:
		return "shared"
	case IsolationPerInstance:
		return "per-instance"
	default:
		return fmt.Sprintf("isolation(%d)", uint8(i))
	}
}

// Instance is one bound, running unit of a plugin. It is always owned by
// exactly one tenant and bound to exactly one installation version at a time.
type Instance struct {
	ID        string
	Tenant    string
	PluginID  string
	Version   string
	Config    map[string]string
	Isolation Isolation
}

// InstanceSpec is the input for CreateInstance.
type InstanceSpec struct {
	ID        string
	Tenant    string
	PluginID  string
	Version   string
	Config    map[string]string
	Isolation Isolation
}

// InstanceSnapshot is a point-in-time view of one instance for observability
// and tests. State is the serving process lifecycle state from the
// Supervisor; Health is the Manager health-loop grade.
type InstanceSnapshot struct {
	Tenant              string
	InstanceID          string
	PluginID            string
	Version             string
	Isolation           Isolation
	Enabled             bool
	State               State
	Health              Health
	ConsecutiveFailures int
	Restarts            int
	Crashes             int
	Launches            int
	Config              map[string]string
}

// RemoveResult reports what happened to plugin data during Remove.
type RemoveResult struct {
	// Purged is true only when an explicit purge option was requested.
	Purged bool
	// DataPreserved is true when plugin data was left in place. It is the
	// default Remove semantics.
	DataPreserved bool
}

// RemoveOption changes Remove behavior.
type RemoveOption func(*removeOptions)

type removeOptions struct{ purge bool }

// WithPurge requests that plugin data be deleted during Remove. Purge is a
// separate, explicit, high-risk option and is off by default.
func WithPurge() RemoveOption {
	return func(o *removeOptions) { o.purge = true }
}

// ManagerOptions configures a Manager. All fields are optional; sensible
// defaults are applied when a field is left zero.
type ManagerOptions struct {
	Runner           Runner
	Logger           *slog.Logger
	Protocol         string
	ProtocolVersion  uint32
	HealthChecker    HealthChecker
	MetricsCollector MetricsCollector

	// HealthCheckInterval is how often running instances are probed.
	HealthCheckInterval time.Duration
	// HealthFailureThreshold is the number of consecutive failed probes before
	// HealthFailurePolicy is applied.
	HealthFailureThreshold int
	// HealthFailurePolicy selects restart or disable after the threshold.
	HealthFailurePolicy HealthFailurePolicy

	// Supervisor tuning, forwarded to every supervised process.
	HandshakeTimeout time.Duration
	ShutdownTimeout  time.Duration
	MaxRestarts      int
	BaseBackoff      time.Duration
	MaxBackoff       time.Duration
	LogBufferSize    int
	Jitter           func(time.Duration) time.Duration

	// CommandArgs/CommandEnv are the default argv/env used for every launched
	// process (Env is supplemented with the launch identity).
	CommandArgs []string
	CommandEnv  []string
}

// Manager owns plugin installations, instances and their supervised
// processes, plus the periodic health and metrics framework. It is safe for
// concurrent use.
type Manager struct {
	mu   sync.Mutex
	opts ManagerOptions

	ctx    context.Context
	cancel context.CancelFunc

	installations map[string]Installation
	instances     map[string]*instanceRecord
	procs         map[string]*procGroup

	healthDone chan struct{}
	closeOnce  sync.Once
}

// instanceRecord is the Manager-side bookkeeping for one instance. It is
// created at CreateInstance and removed at Remove.
type instanceRecord struct {
	inst        Instance
	enabled     bool
	procKey     string
	health      Health
	failures    int
	lastHealthy time.Time
}

// procGroup is one supervised plugin process. In shared isolation several
// instances reference the same procGroup; in per-instance isolation exactly
// one instance does.
type procGroup struct {
	key            string
	supervisor     *Supervisor
	ctx            context.Context
	cancel         context.CancelFunc
	done           chan error
	started        bool
	healthRestarts int
}

// NewManager builds a Manager and starts its background health loop. Call
// Close to stop the loop and every supervised process.
func NewManager(opts ManagerOptions) *Manager {
	if opts.Protocol == "" {
		opts.Protocol = "driver"
	}
	if opts.ProtocolVersion == 0 {
		opts.ProtocolVersion = 1
	}
	if opts.HealthCheckInterval <= 0 {
		opts.HealthCheckInterval = defaultHealthCheckInterval
	}
	if opts.HealthFailureThreshold <= 0 {
		opts.HealthFailureThreshold = defaultHealthFailureThreshold
	}
	if opts.HealthFailurePolicy != HealthPolicyRestart {
		opts.HealthFailurePolicy = HealthPolicyDisable
	}
	if opts.Runner == nil {
		opts.Runner = ExecRunner{}
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if opts.MetricsCollector == nil {
		opts.MetricsCollector = noopMetricsCollector{}
	}

	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{
		opts:          opts,
		ctx:           ctx,
		cancel:        cancel,
		installations: map[string]Installation{},
		instances:     map[string]*instanceRecord{},
		procs:         map[string]*procGroup{},
		healthDone:    make(chan struct{}),
	}
	go m.healthLoop()
	return m
}

// Close stops the health loop and gracefully shuts down every supervised
// process. It is idempotent.
func (m *Manager) Close() error {
	m.closeOnce.Do(func() {
		m.mu.Lock()
		groups := make([]*procGroup, 0, len(m.procs))
		for _, g := range m.procs {
			groups = append(groups, g)
		}
		m.procs = map[string]*procGroup{}
		m.mu.Unlock()

		m.cancel()
		for _, g := range groups {
			g.cancel()
		}

		<-m.healthDone
		wait := m.opts.ShutdownTimeout
		if wait <= 0 {
			wait = defaultShutdownTimeout
		}
		deadline := time.After(wait + 500*time.Millisecond)
		for _, g := range groups {
			select {
			case <-g.done:
			case <-deadline:
			}
		}
	})
	return nil
}

// RegisterInstallation records one installed plugin version. It fails if the
// same (plugin id, version) is already registered.
func (m *Manager) RegisterInstallation(inst Installation) error {
	if inst.PluginID == "" || inst.Version == "" || inst.Path == "" {
		return fmt.Errorf("%w: installation requires plugin id, version and path", ErrInvalidArgument)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := installationKey(inst.PluginID, inst.Version)
	if _, ok := m.installations[key]; ok {
		return fmt.Errorf("%w: %s@%s", ErrInstallationExists, inst.PluginID, inst.Version)
	}
	m.installations[key] = inst
	return nil
}

// CreateInstance binds a new instance to an installed version. The instance is
// created stopped; call Start to launch it.
func (m *Manager) CreateInstance(spec InstanceSpec) (Instance, error) {
	if spec.Tenant == "" || spec.ID == "" {
		return Instance{}, fmt.Errorf("%w: instance requires tenant and id", ErrInvalidArgument)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.installations[installationKey(spec.PluginID, spec.Version)]; !ok {
		return Instance{}, fmt.Errorf("%w: %s@%s", ErrInstallationNotFound, spec.PluginID, spec.Version)
	}
	key := instKey(spec.Tenant, spec.ID)
	if _, ok := m.instances[key]; ok {
		return Instance{}, fmt.Errorf("%w: %s/%s", ErrInstanceExists, spec.Tenant, spec.ID)
	}
	inst := Instance{
		ID:        spec.ID,
		Tenant:    spec.Tenant,
		PluginID:  spec.PluginID,
		Version:   spec.Version,
		Config:    cloneMap(spec.Config),
		Isolation: spec.Isolation,
	}
	if inst.Isolation != IsolationPerInstance {
		inst.Isolation = IsolationShared
	}
	rec := &instanceRecord{
		inst:    inst,
		procKey: procKeyFor(inst),
		health:  HealthUnknown,
	}
	m.instances[key] = rec
	return inst, nil
}

// Start launches the instance's process (or binds it to the running shared
// process). It is idempotent for an already-enabled instance.
func (m *Manager) Start(tenant, id string) error { return m.enableInstance(tenant, id) }

// Enable re-enables a previously disabled instance and relaunches its process.
func (m *Manager) Enable(tenant, id string) error { return m.enableInstance(tenant, id) }

func (m *Manager) enableInstance(tenant, id string) error {
	if tenant == "" || id == "" {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	rec := m.instances[instKey(tenant, id)]
	if rec == nil {
		m.mu.Unlock()
		return ErrInstanceNotFound
	}
	if rec.enabled {
		m.mu.Unlock()
		return nil
	}
	rec.enabled = true
	rec.failures = 0
	rec.health = HealthUnknown

	g := m.procs[rec.procKey]
	created := g == nil
	if created {
		g = m.newProcGroupLocked(rec)
		m.procs[rec.procKey] = g
	}
	m.mu.Unlock()

	if created {
		m.launchProcGroup(g)
	} else {
		g.supervisor.Enable()
	}
	return nil
}

// Disable stops the instance. For a shared process it only stops the process
// when no other enabled instance still uses it.
func (m *Manager) Disable(tenant, id string) error {
	if tenant == "" || id == "" {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	rec := m.instances[instKey(tenant, id)]
	if rec == nil {
		m.mu.Unlock()
		return ErrInstanceNotFound
	}
	if !rec.enabled {
		m.mu.Unlock()
		return nil
	}
	stopSupervisor := m.disableRecordLocked(rec)
	g := m.procs[rec.procKey]
	var sup *Supervisor
	if stopSupervisor && g != nil {
		sup = g.supervisor
	}
	m.mu.Unlock()

	if sup != nil {
		sup.Disable()
	}
	return nil
}

// disableRecordLocked marks rec disabled and reports whether the process that
// serves it should be disabled too (per-instance, or last enabled shared).
func (m *Manager) disableRecordLocked(rec *instanceRecord) bool {
	rec.enabled = false
	if rec.inst.Isolation == IsolationPerInstance {
		return true
	}
	for _, other := range m.instances {
		if other != rec && other.procKey == rec.procKey && other.enabled {
			return false
		}
	}
	return true
}

// Remove deletes an instance and stops its process when it is no longer
// needed. Plugin data is preserved by default; use WithPurge to delete it.
func (m *Manager) Remove(tenant, id string, opts ...RemoveOption) (RemoveResult, error) {
	if tenant == "" || id == "" {
		return RemoveResult{}, ErrInvalidArgument
	}
	o := removeOptions{}
	for _, opt := range opts {
		opt(&o)
	}

	m.mu.Lock()
	key := instKey(tenant, id)
	rec := m.instances[key]
	if rec == nil {
		m.mu.Unlock()
		return RemoveResult{}, ErrInstanceNotFound
	}
	delete(m.instances, key)

	var stop *procGroup
	g := m.procs[rec.procKey]
	if rec.inst.Isolation == IsolationPerInstance {
		if g != nil {
			delete(m.procs, rec.procKey)
			stop = g
		}
	} else {
		stillUsed := false
		for _, other := range m.instances {
			if other.procKey == rec.procKey && other.enabled {
				stillUsed = true
				break
			}
		}
		if !stillUsed && g != nil {
			delete(m.procs, rec.procKey)
			stop = g
		}
	}
	m.mu.Unlock()

	if stop != nil {
		stop.cancel()
		wait := m.opts.ShutdownTimeout
		if wait <= 0 {
			wait = defaultShutdownTimeout
		}
		select {
		case <-stop.done:
		case <-time.After(wait + 100*time.Millisecond):
		}
	}

	return RemoveResult{Purged: o.purge, DataPreserved: !o.purge}, nil
}

// Snapshot returns the current state of one instance. Tenant scoping is
// strict: a wrong tenant is indistinguishable from a missing instance.
func (m *Manager) Snapshot(tenant, id string) (InstanceSnapshot, error) {
	if tenant == "" || id == "" {
		return InstanceSnapshot{}, ErrInvalidArgument
	}
	m.mu.Lock()
	rec := m.instances[instKey(tenant, id)]
	if rec == nil {
		m.mu.Unlock()
		return InstanceSnapshot{}, ErrInstanceNotFound
	}
	g := m.procs[rec.procKey]
	var sup *Supervisor
	if g != nil {
		sup = g.supervisor
	}
	inst := rec.inst
	health := rec.health
	failures := rec.failures
	enabled := rec.enabled
	m.mu.Unlock()

	snap := InstanceSnapshot{
		Tenant:              inst.Tenant,
		InstanceID:          inst.ID,
		PluginID:            inst.PluginID,
		Version:             inst.Version,
		Isolation:           inst.Isolation,
		Enabled:             enabled,
		Health:              health,
		ConsecutiveFailures: failures,
		Config:              cloneMap(inst.Config),
	}
	if sup != nil {
		ps := sup.Snapshot()
		snap.State = ps.State
		snap.Restarts = ps.Restarts
		snap.Crashes = ps.Crashes
		snap.Launches = ps.Launches
	} else {
		snap.State = StateStopped
	}
	return snap, nil
}

// ListInstances returns a snapshot for every instance owned by tenant, sorted
// by instance id.
func (m *Manager) ListInstances(tenant string) []InstanceSnapshot {
	m.mu.Lock()
	var ids []string
	for _, rec := range m.instances {
		if rec.inst.Tenant == tenant {
			ids = append(ids, rec.inst.ID)
		}
	}
	m.mu.Unlock()

	sort.Strings(ids)
	out := make([]InstanceSnapshot, 0, len(ids))
	for _, id := range ids {
		if snap, err := m.Snapshot(tenant, id); err == nil {
			out = append(out, snap)
		}
	}
	return out
}

// ListInstallations returns all registered installations sorted by plugin id
// then version.
func (m *Manager) ListInstallations() []Installation {
	m.mu.Lock()
	out := make([]Installation, 0, len(m.installations))
	for _, inst := range m.installations {
		out = append(out, inst)
	}
	m.mu.Unlock()

	sort.Slice(out, func(i, j int) bool {
		if out[i].PluginID != out[j].PluginID {
			return out[i].PluginID < out[j].PluginID
		}
		return out[i].Version < out[j].Version
	})
	return out
}

// Metrics returns a factual resource/health snapshot for one instance. It
// combines process-level observability from MetricsCollector with the restart
// count and last-healthy time maintained by the Manager.
func (m *Manager) Metrics(tenant, id string) (Metrics, error) {
	if tenant == "" || id == "" {
		return Metrics{}, ErrInvalidArgument
	}
	m.mu.Lock()
	rec := m.instances[instKey(tenant, id)]
	if rec == nil {
		m.mu.Unlock()
		return Metrics{}, ErrInstanceNotFound
	}
	g := m.procs[rec.procKey]
	var sup *Supervisor
	started := false
	healthRestarts := 0
	if g != nil {
		sup = g.supervisor
		started = g.started
		healthRestarts = g.healthRestarts
	}
	target := MetricsTarget{
		PluginID:   rec.inst.PluginID,
		Version:    rec.inst.Version,
		Tenant:     rec.inst.Tenant,
		InstanceID: rec.inst.ID,
	}
	lastHealthy := rec.lastHealthy
	m.mu.Unlock()

	base := Metrics{Handles: -1, Goroutines: -1, LastHealthy: lastHealthy}
	if sup != nil {
		base.RestartCount = sup.Snapshot().Restarts + healthRestarts
	}
	if sup == nil || !started {
		return base, nil
	}

	collected, err := m.opts.MetricsCollector.Collect(m.ctx, target)
	if err != nil {
		return Metrics{}, fmt.Errorf("pluginhost: collect metrics: %w", err)
	}
	collected.RestartCount = base.RestartCount
	collected.LastHealthy = lastHealthy
	return collected, nil
}

// newProcGroupLocked builds (but does not launch) the supervised process for
// rec. The caller must hold m.mu.
func (m *Manager) newProcGroupLocked(rec *instanceRecord) *procGroup {
	inst := m.installations[installationKey(rec.inst.PluginID, rec.inst.Version)]
	kind := inst.Kind
	if kind == 0 {
		kind = kindFromProtocol(m.opts.Protocol)
	}
	cfg := Config{
		PluginID:            inst.PluginID,
		Kind:                kind,
		Protocol:            kind.Protocol(),
		ProtocolVersion:     m.opts.ProtocolVersion,
		Command:             CommandSpec{Path: inst.Path, Args: m.opts.CommandArgs, Env: m.opts.CommandEnv},
		HandshakeTimeout:    m.opts.HandshakeTimeout,
		ShutdownTimeout:     m.opts.ShutdownTimeout,
		HealthCheckInterval: m.opts.HealthCheckInterval,
		MaxRestarts:         m.opts.MaxRestarts,
		BaseBackoff:         m.opts.BaseBackoff,
		MaxBackoff:          m.opts.MaxBackoff,
		LogBufferSize:       m.opts.LogBufferSize,
		Jitter:              m.opts.Jitter,
	}
	if rec.inst.Isolation == IsolationPerInstance {
		cfg.Command.Env = append(append([]string{}, cfg.Command.Env...),
			EnvTenant+"="+rec.inst.Tenant,
			EnvInstanceID+"="+rec.inst.ID,
		)
	}
	gctx, cancel := context.WithCancel(m.ctx)
	return &procGroup{
		key:        rec.procKey,
		supervisor: NewSupervisor(cfg, m.opts.Runner, m.opts.Logger),
		ctx:        gctx,
		cancel:     cancel,
		done:       make(chan error, 1),
	}
}

// launchProcGroup starts the supervisor Run loop exactly once. It is called
// outside the Manager lock so the blocking Run never holds m.mu.
func (m *Manager) launchProcGroup(g *procGroup) {
	m.mu.Lock()
	if g.started {
		m.mu.Unlock()
		return
	}
	g.started = true
	m.mu.Unlock()
	go func() { g.done <- g.supervisor.Run(g.ctx) }()
}

// healthLoop periodically probes running instances until the Manager is
// closed.
func (m *Manager) healthLoop() {
	defer close(m.healthDone)
	interval := m.opts.HealthCheckInterval
	if interval <= 0 {
		interval = defaultHealthCheckInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.probeOnce()
		}
	}
}

// probeOnce performs one health pass over running instances. Probes run
// outside the Manager lock; Supervisor mutations are also applied outside it.
func (m *Manager) probeOnce() {
	checker := m.opts.HealthChecker
	if checker == nil {
		return
	}

	type target struct {
		key   string
		probe HealthTarget
	}
	var targets []target

	m.mu.Lock()
	for key, rec := range m.instances {
		g := m.procs[rec.procKey]
		if g == nil || !g.started || !rec.enabled {
			continue
		}
		st := g.supervisor.State()
		if st != StateHealthy && st != StateDegraded {
			continue
		}
		targets = append(targets, target{
			key: key,
			probe: HealthTarget{
				PluginID:   rec.inst.PluginID,
				Version:    rec.inst.Version,
				Tenant:     rec.inst.Tenant,
				InstanceID: rec.inst.ID,
			},
		})
	}
	m.mu.Unlock()

	type action struct {
		sup    *Supervisor
		policy HealthFailurePolicy
	}

	for _, t := range targets {
		h, err := checker.Check(m.ctx, t.probe)
		degraded := err != nil || h == HealthDegraded

		m.mu.Lock()
		rec := m.instances[t.key]
		if rec == nil || !rec.enabled {
			m.mu.Unlock()
			continue
		}
		g := m.procs[rec.procKey]
		if g == nil || !g.started {
			m.mu.Unlock()
			continue
		}

		var act *action
		if degraded {
			rec.health = HealthDegraded
			rec.failures++
			if rec.failures >= m.opts.HealthFailureThreshold {
				switch m.opts.HealthFailurePolicy {
				case HealthPolicyRestart:
					rec.failures = 0
					g.healthRestarts++
					act = &action{sup: g.supervisor, policy: HealthPolicyRestart}
				default:
					rec.failures = 0
					shouldDisable := m.disableRecordLocked(rec)
					if shouldDisable {
						act = &action{sup: g.supervisor, policy: HealthPolicyDisable}
					}
				}
			}
		} else {
			rec.health = HealthHealthy
			rec.failures = 0
			rec.lastHealthy = time.Now()
		}
		m.mu.Unlock()

		if act != nil {
			switch act.policy {
			case HealthPolicyRestart:
				act.sup.Restart()
			default:
				act.sup.Disable()
			}
		}
	}
}

func installationKey(pluginID, version string) string { return pluginID + "\x00" + version }
func instKey(tenant, id string) string                { return tenant + "\x00" + id }

func procKeyFor(inst Instance) string {
	if inst.Isolation == IsolationPerInstance {
		return "per:" + instKey(inst.Tenant, inst.ID)
	}
	return "shared:" + installationKey(inst.PluginID, inst.Version)
}

func cloneMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
