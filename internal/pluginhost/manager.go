package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/url"
	"sort"
	"sync"
	"time"

	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/application"
	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/driver"
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

// ConfigPathKey is the existing Host-only config-file reference. It is not
// a plugin JSON property and must not be forwarded to ConfigureInstance.
const ConfigPathKey = "path"

// Sentinel errors returned by the Manager. Callers can compare with
// errors.Is to distinguish "does not exist" from other failures.
var (
	ErrInstallationNotFound = errors.New("pluginhost: installation not found")
	ErrInstallationExists   = errors.New("pluginhost: installation already registered")
	ErrInstanceNotFound     = errors.New("pluginhost: instance not found")
	ErrInstanceExists       = errors.New("pluginhost: instance already exists")
	ErrInvalidArgument      = errors.New("pluginhost: invalid argument")
	ErrAmbiguousInstance    = errors.New("pluginhost: multiple running processes require an instance target")
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
	// Lifecycle operations serialize while observations remain available under mu.
	ops  sync.Mutex
	mu   sync.Mutex
	opts ManagerOptions

	ctx    context.Context
	cancel context.CancelFunc

	installations map[string]Installation
	instances     map[string]*instanceRecord
	procs         map[string]*procGroup
	retiring      map[*procGroup]string // detached groups awaiting exit, keyed to the owning instance

	healthDone chan struct{}
	closeOnce  sync.Once
	closeErr   error
}

// instanceRecord is the Manager-side bookkeeping for one instance. It is
// created at CreateInstance and removed at Remove.
type instanceRecord struct {
	inst              Instance
	enabled           bool
	procKey           string
	health            Health
	failures          int
	lastHealthy       time.Time
	configuredSession *runtimeSession
	configRevision    uint32
}

// procGroup is one supervised plugin process. In shared isolation several
// instances reference the same procGroup; in per-instance isolation exactly
// one instance does.
type procGroup struct {
	key            string
	supervisor     *Supervisor
	ctx            context.Context
	cancel         context.CancelFunc
	done           chan struct{}
	err            error // published by closing done
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
		retiring:      map[*procGroup]string{},
		healthDone:    make(chan struct{}),
	}
	go m.healthLoop()
	return m
}

// Close stops the health loop and gracefully shuts down every supervised
// process. It is idempotent.
func (m *Manager) Close() error {
	m.closeOnce.Do(func() {
		m.cancel() // Interrupt an in-flight reconcile before waiting for it.
		m.ops.Lock()
		m.mu.Lock()
		groups := make(map[*procGroup]struct{}, len(m.procs)+len(m.retiring))
		for _, g := range m.procs {
			groups[g] = struct{}{}
		}
		for g := range m.retiring {
			groups[g] = struct{}{}
		}
		m.procs = map[string]*procGroup{}
		m.retiring = map[*procGroup]string{}
		m.mu.Unlock()
		m.ops.Unlock()

		for g := range groups {
			g.cancel()
		}

		<-m.healthDone
		for g := range groups {
			m.closeErr = errors.Join(m.closeErr, m.stopProcGroup(g))
		}
	})
	return m.closeErr
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
	m.ops.Lock()
	defer m.ops.Unlock()
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
	inst.Config = cloneMap(inst.Config)
	return inst, nil
}

// ReconcileInstance applies a complete instance definition. Candidate processes
// must establish an authenticated, serving session before replacing a live
// binding. Config updates use the existing per-instance RPC, not process-wide
// environment variables, so shared siblings (including other tenants) stay up.
// No plugin data is removed. A disabled definition need not be installed.
func (m *Manager) ReconcileInstance(ctx context.Context, spec InstanceSpec, enabled bool) error {
	m.ops.Lock()
	defer m.ops.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := m.ctx.Err(); err != nil {
		return err
	}
	if spec.Tenant == "" || spec.ID == "" {
		return ErrInvalidArgument
	}
	inst := Instance(spec)
	inst.Config = cloneMap(spec.Config)
	if inst.Isolation != IsolationPerInstance {
		inst.Isolation = IsolationShared
	}
	key := instKey(inst.Tenant, inst.ID)
	// A prior attempt may have switched bindings but timed out waiting for
	// retirement. The unchanged-spec fast path must not bypass that failure.
	if err := m.retireProcGroups(key); err != nil {
		return err
	}
	rec := &instanceRecord{inst: inst, enabled: enabled, procKey: procKeyFor(inst), health: HealthUnknown}

	m.mu.Lock()
	old := m.instances[key]
	var previous *procGroup
	if old != nil {
		previous = m.procs[old.procKey]
	}
	if !enabled {
		// Retain the new definition without requiring its binary to be present.
		if old != nil {
			m.instances[key] = rec
		}
		stop := m.unusedProcGroupLocked(previous)
		m.mu.Unlock()
		return m.retireProcGroups(key, stop)
	}
	if _, ok := m.installations[installationKey(inst.PluginID, inst.Version)]; !ok {
		m.mu.Unlock()
		return fmt.Errorf("%w: %s@%s", ErrInstallationNotFound, inst.PluginID, inst.Version)
	}
	g := m.procs[rec.procKey]
	// A per-instance key is stable across upgrades; it must not retain the old
	// installation's Supervisor. An unused disabled group is replaced, not revived.
	if g != nil && (inst.Isolation == IsolationPerInstance && old != nil &&
		(old.inst.PluginID != inst.PluginID || old.inst.Version != inst.Version) ||
		g.supervisor.State() == StateDisabled && !m.groupInUseLocked(g)) {
		g = nil
	}
	if old != nil && old.enabled && sameInstance(old.inst, inst) && g != nil &&
		old.configuredSession != nil && old.configuredSession == procSession(g) {
		m.mu.Unlock()
		return nil
	}
	if old != nil {
		rec.configRevision = old.configRevision
	}
	created := g == nil
	if created {
		g = m.newProcGroupLocked(rec)
	}
	m.mu.Unlock()

	if created {
		m.launchProcGroup(g)
	}
	timeout := m.opts.HandshakeTimeout
	if timeout <= 0 {
		timeout = defaultHandshakeTimeout
	}
	applyCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	sess, err := m.waitProcSession(applyCtx, g)
	if err == nil {
		err = initializeInstanceSession(applyCtx, g, sess)
	}
	config := pluginConfig(inst.Config)
	var previousConfig map[string]string
	previousConfigured := false
	m.mu.Lock()
	if old != nil {
		previousConfig = pluginConfig(old.inst.Config)
		previousConfigured = old.configuredSession == sess
		rec.configRevision = old.configRevision
	}
	m.mu.Unlock()
	needsConfig := !maps.Equal(config, previousConfig) || (len(config) > 0 || rec.configRevision > 0) && !previousConfigured
	configured := false
	if err == nil && needsConfig {
		rec.configRevision++
		configured = true
		err = configureInstance(applyCtx, sess, inst, rec.configRevision)
	}
	if err == nil {
		err = sess.health(applyCtx)
	}
	if err == nil {
		err = applyCtx.Err()
	}
	if err == nil {
		err = m.ctx.Err()
	}
	if err == nil && procSession(g) != sess {
		err = errors.New("pluginhost: session changed during reconcile")
	}
	if err != nil {
		// A rejected in-place config must not leave the Manager or the applied cache
		// ahead. Restore the last config on the same session; report rollback failure
		// too, since a transport error can leave the plugin's result uncertain.
		m.mu.Lock()
		restorePrevious := configured && !created && old != nil && old.configuredSession == sess
		if restorePrevious {
			old.configRevision = max(old.configRevision, rec.configRevision) + 1
		}
		rollbackRevision := uint32(0)
		if old != nil {
			rollbackRevision = old.configRevision
		}
		m.mu.Unlock()
		if restorePrevious {
			rollbackCtx, rollbackCancel := context.WithTimeout(m.ctx, timeout)
			rollbackErr := configureInstance(rollbackCtx, sess, old.inst, rollbackRevision)
			rollbackCancel()
			if rollbackErr != nil {
				m.mu.Lock()
				if old.configuredSession == sess {
					old.configuredSession = nil // An unchanged replay must retry an uncertain config.
				}
				m.mu.Unlock()
				err = errors.Join(err, fmt.Errorf("restore previous config: %w", rollbackErr))
			}
		}
		if created {
			err = errors.Join(err, m.retireProcGroups(key, g))
		}
		return fmt.Errorf("reconcile instance %s/%s: %w", inst.Tenant, inst.ID, err)
	}
	rec.configuredSession = sess
	m.mu.Lock()
	replaced := m.procs[rec.procKey]
	m.instances[key] = rec
	m.procs[rec.procKey] = g
	stopPrevious := m.unusedProcGroupLocked(previous)
	var stopReplaced *procGroup
	if replaced != previous {
		stopReplaced = m.unusedProcGroupLocked(replaced)
	}
	m.mu.Unlock()
	return m.retireProcGroups(key, stopPrevious, stopReplaced)
}

func sameInstance(a, b Instance) bool {
	return a.Tenant == b.Tenant && a.ID == b.ID && a.PluginID == b.PluginID &&
		a.Version == b.Version && a.Isolation == b.Isolation && maps.Equal(a.Config, b.Config)
}

// groupInUseLocked compares group identity, not just its key: an upgraded
// per-instance process has the same key but a different Supervisor.
func (m *Manager) groupInUseLocked(g *procGroup) bool {
	for _, rec := range m.instances {
		if rec.enabled && m.procs[rec.procKey] == g {
			return true
		}
	}
	return false
}

func (m *Manager) unusedProcGroupLocked(g *procGroup) *procGroup {
	if g == nil || m.groupInUseLocked(g) {
		return nil
	}
	if m.procs[g.key] == g {
		delete(m.procs, g.key)
	}
	return g
}

// retireProcGroups retains detached process ownership until exit is confirmed.
// An error can leave the new binding active, but retry/Close must still finish
// stopping its predecessor before the same instance can be acknowledged.
func (m *Manager) retireProcGroups(owner string, groups ...*procGroup) error {
	m.mu.Lock()
	for _, g := range groups {
		if g != nil {
			m.retiring[g] = owner
		}
	}
	var pending []*procGroup
	for g, key := range m.retiring {
		if key == owner {
			pending = append(pending, g)
		}
	}
	m.mu.Unlock()
	var result error
	for _, g := range pending {
		if err := m.stopProcGroup(g); err != nil {
			result = errors.Join(result, err)
			continue
		}
		m.mu.Lock()
		delete(m.retiring, g)
		m.mu.Unlock()
	}
	return result
}

func (m *Manager) stopProcGroup(g *procGroup) error {
	if g == nil {
		return nil
	}
	g.cancel()
	timeout := m.opts.ShutdownTimeout
	if timeout <= 0 {
		timeout = defaultShutdownTimeout
	}
	// Supervisor allows a deadline for RPC, exit after RPC, and signal fallback.
	timer := time.NewTimer(3*timeout + 500*time.Millisecond)
	defer timer.Stop()
	select {
	case <-g.done:
		return nil
	case <-timer.C:
		return errors.New("pluginhost: timed out stopping previous process")
	}
}

func procSession(g *procGroup) *runtimeSession {
	g.supervisor.mu.Lock()
	defer g.supervisor.mu.Unlock()
	return g.supervisor.session
}

func (m *Manager) waitProcSession(ctx context.Context, g *procGroup) (*runtimeSession, error) {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		snap := g.supervisor.Snapshot()
		if snap.State == StateHealthy && snap.HandshakeCompleted && snap.RPCConnections == 1 {
			if sess := procSession(g); sess != nil {
				return sess, nil
			}
		}
		if snap.Crashes > 0 || snap.State == StateDisabled {
			return nil, fmt.Errorf("pluginhost: process failed to become ready (%s)", snap.State)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-g.ctx.Done():
			return nil, g.ctx.Err()
		case <-g.done:
			return nil, fmt.Errorf("pluginhost: process stopped before ready: %w", errors.Join(g.err, errors.New("session unavailable")))
		case <-ticker.C:
		}
	}
}

func initializeInstanceSession(ctx context.Context, g *procGroup, sess *runtimeSession) error {
	g.supervisor.mu.Lock()
	launchID := g.supervisor.launchID
	g.supervisor.mu.Unlock()
	if cli := sess.driverClient(); cli != nil {
		resp, err := cli.Initialize(ctx, &driver.InitializeRequest{
			ProtocolVersion: driver.ProtocolVersion, SupportedProtocolVersions: []uint32{driver.ProtocolVersion},
			LaunchID: launchID, RuntimeType: "plugin-host",
		})
		if err != nil {
			return fmt.Errorf("initialize driver: %w", err)
		}
		if resp == nil {
			return errors.New("pluginhost: empty driver initialize response")
		}
		if resp.Status != nil && !resp.Status.IsOK() {
			return fmt.Errorf("initialize driver: %v", resp.Status)
		}
		return nil
	}
	if cli := sess.appClient(); cli != nil {
		resp, err := cli.Initialize(ctx, &application.InitializeRequest{
			ProtocolVersion: application.ProtocolVersion, SupportedProtocolVersions: []uint32{application.ProtocolVersion},
			LaunchID: launchID, RuntimeType: "plugin-host",
		})
		if err != nil {
			return fmt.Errorf("initialize application: %w", err)
		}
		if resp == nil {
			return errors.New("pluginhost: empty application initialize response")
		}
		if resp.Status != nil && !resp.Status.IsOK() {
			return fmt.Errorf("initialize application: %v", resp.Status)
		}
		return nil
	}
	return errors.New("pluginhost: no initialization session")
}

// pluginConfig excludes only the already-reserved Host path reference. Keep
// the full map on Instance for comparison/snapshots; do not read the referenced
// file or invent a file format/merge rule as part of runtime reconfiguration.
func pluginConfig(config map[string]string) map[string]string {
	out := cloneMap(config)
	delete(out, ConfigPathKey)
	return out
}

// Empty configs are not sent on initial enable: existing plugins may rely on
// a later device binding. An explicit change from nonempty to empty does send
// {}, so clearing configuration is not confused with an unchanged definition.
func configureInstance(ctx context.Context, sess *runtimeSession, inst Instance, revision uint32) error {
	config := pluginConfig(inst.Config)
	if config == nil {
		config = map[string]string{}
	}
	data, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("encode instance config: %w", err)
	}
	// The same local instance id may occur in multiple tenants of a shared host.
	id := url.PathEscape(inst.Tenant) + "/" + url.PathEscape(inst.ID)
	if cli := sess.driverClient(); cli != nil {
		resp, err := cli.ConfigureInstance(ctx, &driver.ConfigureInstanceRequest{PluginInstanceID: id, Config: data, ConfigRevision: revision})
		if err != nil {
			return fmt.Errorf("configure driver instance: %w", err)
		}
		if resp == nil {
			return errors.New("pluginhost: empty driver configure response")
		}
		if resp.Status != nil && !resp.Status.IsOK() {
			return fmt.Errorf("configure driver instance: %v", resp.Status)
		}
		return nil
	}
	if cli := sess.appClient(); cli != nil {
		resp, err := cli.ConfigureInstance(ctx, &application.ConfigureInstanceRequest{PluginInstanceID: id, Config: data, ConfigRevision: revision})
		if err != nil {
			return fmt.Errorf("configure application instance: %w", err)
		}
		if resp == nil {
			return errors.New("pluginhost: empty application configure response")
		}
		if resp.Status != nil && !resp.Status.IsOK() {
			return fmt.Errorf("configure application instance: %v", resp.Status)
		}
		return nil
	}
	return errors.New("pluginhost: no configuration session")
}

// Start launches the instance's process (or binds it to the running shared
// process). It is idempotent for an already-enabled instance.
func (m *Manager) Start(tenant, id string) error { return m.enableInstance(tenant, id) }

// Enable re-enables a previously disabled instance and relaunches its process.
func (m *Manager) Enable(tenant, id string) error { return m.enableInstance(tenant, id) }

func (m *Manager) enableInstance(tenant, id string) error {
	m.ops.Lock()
	defer m.ops.Unlock()
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
	if _, ok := m.installations[installationKey(rec.inst.PluginID, rec.inst.Version)]; !ok {
		m.mu.Unlock()
		return ErrInstallationNotFound
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
	m.ops.Lock()
	defer m.ops.Unlock()
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
	m.ops.Lock()
	defer m.ops.Unlock()
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
	g := &procGroup{
		key:        rec.procKey,
		supervisor: NewSupervisor(cfg, m.opts.Runner, m.opts.Logger),
		ctx:        gctx,
		cancel:     cancel,
		done:       make(chan struct{}),
	}
	g.supervisor.prepareSession = func(ctx context.Context, sess *runtimeSession) error {
		return m.restoreInstanceSessions(ctx, g, sess)
	}
	return g
}

// restoreInstanceSessions runs after authentication but before a restarted
// session is published or declared healthy. It replays only Manager-applied
// configs; device bindings and application JSON remain owned by their callers.
// It must not acquire ops: Reconcile may be waiting for this session while
// holding that lock. A fresh candidate has no live bindings here and is still
// configured by Reconcile before the Manager publishes its binding.
func (m *Manager) restoreInstanceSessions(ctx context.Context, g *procGroup, sess *runtimeSession) error {
	type restore struct {
		key      string
		record   *instanceRecord
		inst     Instance
		revision uint32
	}
	var targets []restore
	m.mu.Lock()
	for key, rec := range m.instances {
		if rec.enabled && m.procs[rec.procKey] == g && (rec.configuredSession != nil || rec.configRevision > 0) {
			targets = append(targets, restore{key: key, record: rec, inst: rec.inst, revision: rec.configRevision})
		}
	}
	m.mu.Unlock()
	if len(targets) == 0 {
		return nil // Preserve CreateInstance/Start's transport-only contract.
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].key < targets[j].key })
	if err := initializeInstanceSession(ctx, g, sess); err != nil {
		return err
	}
	for i := range targets {
		t := &targets[i]
		// revision > 0 retains an explicit clear; a never-configured empty
		// instance must still wait for its upper-layer device/app binding.
		if t.revision > 0 || len(pluginConfig(t.inst.Config)) > 0 {
			t.revision++
			if err := configureInstance(ctx, sess, t.inst, t.revision); err != nil {
				return fmt.Errorf("restore instance %s/%s: %w", t.inst.Tenant, t.inst.ID, err)
			}
		}
	}
	if err := sess.health(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range targets {
		rec := m.instances[t.key]
		if rec == t.record && rec.enabled && m.procs[rec.procKey] == g {
			rec.configuredSession = sess
			rec.configRevision = max(rec.configRevision, t.revision)
		}
	}
	return nil
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
	go func() {
		g.err = g.supervisor.Run(g.ctx)
		close(g.done)
	}()
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
		key    string
		record *instanceRecord
		group  *procGroup
		probe  HealthTarget
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
			key:    key,
			record: rec,
			group:  g,
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

		m.ops.Lock()
		m.mu.Lock()
		rec := m.instances[t.key]
		if rec != t.record || !rec.enabled || m.procs[rec.procKey] != t.group {
			m.mu.Unlock()
			m.ops.Unlock()
			continue
		}
		g := t.group

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
		m.ops.Unlock()
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

// pluginSupervisor serves legacy plugin-only callers only when every enabled
// binding resolves to the same process. Map iteration is never a routing rule.
func (m *Manager) pluginSupervisor(pluginID string) (*Supervisor, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var selected *procGroup
	for _, rec := range m.instances {
		if rec.inst.PluginID != pluginID || !rec.enabled {
			continue
		}
		g := m.procs[rec.procKey]
		if g == nil {
			continue
		}
		if selected != nil && selected != g {
			return nil, fmt.Errorf("%w: %s", ErrAmbiguousInstance, pluginID)
		}
		selected = g
	}
	if selected == nil {
		return nil, fmt.Errorf("pluginhost: no running instance for plugin %q", pluginID)
	}
	return selected.supervisor, nil
}

func (m *Manager) instanceSupervisor(tenant, id string) (*Supervisor, error) {
	if tenant == "" || id == "" {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rec := m.instances[instKey(tenant, id)]
	if rec == nil || !rec.enabled || m.procs[rec.procKey] == nil {
		return nil, fmt.Errorf("%w: %s/%s", ErrInstanceNotFound, tenant, id)
	}
	return m.procs[rec.procKey].supervisor, nil
}

// DriverClient returns a session-scoped client for an unambiguous plugin
// process. Multiple enabled instances may share it; multiple processes require
// DriverClientForInstance. Callers must re-resolve after a process restart.
func (m *Manager) DriverClient(pluginID string) (driver.DriverClient, error) {
	sup, err := m.pluginSupervisor(pluginID)
	if err != nil {
		return nil, err
	}
	if cli := sup.DriverClient(); cli != nil {
		return cli, nil
	}
	return nil, fmt.Errorf("pluginhost: no established Driver RPC session for plugin %q", pluginID)
}

// DriverClientForInstance resolves exactly one enabled tenant/instance binding,
// with no fallback to another version, process or tenant.
func (m *Manager) DriverClientForInstance(tenant, id string) (driver.DriverClient, error) {
	sup, err := m.instanceSupervisor(tenant, id)
	if err != nil {
		return nil, err
	}
	if cli := sup.DriverClient(); cli != nil {
		return cli, nil
	}
	return nil, fmt.Errorf("pluginhost: no established Driver RPC session for instance %s/%s", tenant, id)
}

// ApplicationClient is the application-kind equivalent of DriverClient. It
// rejects ambiguous plugin-only lookups instead of selecting an arbitrary process.
func (m *Manager) ApplicationClient(pluginID string) (application.ApplicationClient, error) {
	sup, err := m.pluginSupervisor(pluginID)
	if err != nil {
		return nil, err
	}
	if cli := sup.ApplicationClient(); cli != nil {
		return cli, nil
	}
	return nil, fmt.Errorf("pluginhost: no established Application RPC session for plugin %q", pluginID)
}

// ApplicationClientForInstance resolves the exact enabled tenant/instance
// process; the returned client must be re-resolved after a restart.
func (m *Manager) ApplicationClientForInstance(tenant, id string) (application.ApplicationClient, error) {
	sup, err := m.instanceSupervisor(tenant, id)
	if err != nil {
		return nil, err
	}
	if cli := sup.ApplicationClient(); cli != nil {
		return cli, nil
	}
	return nil, fmt.Errorf("pluginhost: no established Application RPC session for instance %s/%s", tenant, id)
}
