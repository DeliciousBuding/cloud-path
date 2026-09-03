package pluginhost_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/pluginhost"
	pluginharness "github.com/DeliciousBuding/cloud-path/testing/plugin-harness"
)

func newManager(runner pluginhost.Runner, mutate ...func(*pluginhost.ManagerOptions)) *pluginhost.Manager {
	o := pluginhost.ManagerOptions{
		Runner:              runner,
		Protocol:            "driver",
		ProtocolVersion:     1,
		HandshakeTimeout:    300 * time.Millisecond,
		ShutdownTimeout:     200 * time.Millisecond,
		BaseBackoff:         5 * time.Millisecond,
		MaxBackoff:          20 * time.Millisecond,
		Jitter:              func(time.Duration) time.Duration { return 0 },
		HealthCheckInterval: 2 * time.Millisecond,
	}
	for _, m := range mutate {
		m(&o)
	}
	return pluginhost.NewManager(o)
}

func mustRegister(t *testing.T, m *pluginhost.Manager, pluginID, version string) {
	t.Helper()
	if err := m.RegisterInstallation(pluginhost.Installation{PluginID: pluginID, Version: version, Path: "fake-plugin"}); err != nil {
		t.Fatalf("RegisterInstallation: %v", err)
	}
}

func mustCreate(t *testing.T, m *pluginhost.Manager, spec pluginhost.InstanceSpec) pluginhost.Instance {
	t.Helper()
	inst, err := m.CreateInstance(spec)
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	return inst
}

func mustStart(t *testing.T, m *pluginhost.Manager, tenant, id string) {
	t.Helper()
	if err := m.Start(tenant, id); err != nil {
		t.Fatalf("Start: %v", err)
	}
}

func waitInstanceState(t *testing.T, m *pluginhost.Manager, tenant, id string, want pluginhost.State) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		snap, err := m.Snapshot(tenant, id)
		if err != nil {
			t.Fatalf("Snapshot(%s, %s): %v", tenant, id, err)
		}
		if snap.State == want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	snap, _ := m.Snapshot(tenant, id)
	t.Fatalf("instance %s/%s state = %s, want %s (snapshot=%+v)", tenant, id, snap.State, want, snap)
}

func waitInstanceHealth(t *testing.T, m *pluginhost.Manager, tenant, id string, want pluginhost.Health) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		snap, err := m.Snapshot(tenant, id)
		if err != nil {
			t.Fatalf("Snapshot(%s, %s): %v", tenant, id, err)
		}
		if snap.Health == want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	snap, _ := m.Snapshot(tenant, id)
	t.Fatalf("instance %s/%s health = %s, want %s (snapshot=%+v)", tenant, id, snap.Health, want, snap)
}

type healthyChecker struct{}

func (healthyChecker) Check(context.Context, pluginhost.HealthTarget) (pluginhost.Health, error) {
	return pluginhost.HealthHealthy, nil
}

type degradedChecker struct{}

func (degradedChecker) Check(context.Context, pluginhost.HealthTarget) (pluginhost.Health, error) {
	return pluginhost.HealthDegraded, nil
}

type toggleChecker struct {
	mu       sync.Mutex
	degraded bool
}

func (c *toggleChecker) Check(context.Context, pluginhost.HealthTarget) (pluginhost.Health, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.degraded {
		return pluginhost.HealthDegraded, nil
	}
	return pluginhost.HealthHealthy, nil
}

func (c *toggleChecker) setDegraded(v bool) {
	c.mu.Lock()
	c.degraded = v
	c.mu.Unlock()
}

type fixedCollector struct {
	cpu        time.Duration
	rss        int64
	handles    int
	goroutines int
	rate       float64
}

func (c fixedCollector) Collect(context.Context, pluginhost.MetricsTarget) (pluginhost.Metrics, error) {
	return pluginhost.Metrics{
		CPUTime:     c.cpu,
		RSSBytes:    c.rss,
		Handles:     c.handles,
		Goroutines:  c.goroutines,
		MessageRate: c.rate,
	}, nil
}

func TestManagerMultiInstance(t *testing.T) {
	runner := pluginharness.NewFakeRunner()
	m := newManager(runner)
	defer m.Close()

	mustRegister(t, m, "io.test.shared", "0.1.0")

	mustCreate(t, m, pluginhost.InstanceSpec{ID: "a1", Tenant: "tenant-a", PluginID: "io.test.shared", Version: "0.1.0", Isolation: pluginhost.IsolationShared})
	mustCreate(t, m, pluginhost.InstanceSpec{ID: "a2", Tenant: "tenant-a", PluginID: "io.test.shared", Version: "0.1.0", Isolation: pluginhost.IsolationShared})
	mustCreate(t, m, pluginhost.InstanceSpec{ID: "b1", Tenant: "tenant-b", PluginID: "io.test.shared", Version: "0.1.0", Isolation: pluginhost.IsolationPerInstance})

	mustStart(t, m, "tenant-a", "a1")
	mustStart(t, m, "tenant-a", "a2")
	mustStart(t, m, "tenant-b", "b1")

	waitInstanceState(t, m, "tenant-a", "a1", pluginhost.StateHealthy)
	waitInstanceState(t, m, "tenant-a", "a2", pluginhost.StateHealthy)
	waitInstanceState(t, m, "tenant-b", "b1", pluginhost.StateHealthy)

	// Shared instances share one process; per-instance gets its own.
	if got := runner.StartedCount(); got != 2 {
		t.Fatalf("StartedCount = %d, want 2 (one shared + one per-instance)", got)
	}

	procs := runner.Started()
	if len(procs) != 2 {
		t.Fatalf("started processes = %d, want 2", len(procs))
	}

	// Start order is asynchronous, so identify processes by identity instead of
	// position: exactly one shared process and one per-instance process.
	var shared, perInstance *pluginharness.FakeProcess
	for _, p := range procs {
		if _, ok := p.Env()[pluginhost.EnvTenant]; ok {
			perInstance = p
		} else {
			shared = p
		}
	}
	if shared == nil {
		t.Fatal("no shared process found")
	}
	if perInstance == nil {
		t.Fatal("no per-instance process found")
	}
	if got := perInstance.Env()[pluginhost.EnvTenant]; got != "tenant-b" {
		t.Fatalf("per-instance tenant env = %q, want tenant-b", got)
	}
	if got := perInstance.Env()[pluginhost.EnvInstanceID]; got != "b1" {
		t.Fatalf("per-instance instance env = %q, want b1", got)
	}

	if got := m.ListInstances("tenant-a"); len(got) != 2 {
		t.Fatalf("ListInstances(tenant-a) = %d, want 2", len(got))
	}
	if got := m.ListInstances("tenant-b"); len(got) != 1 {
		t.Fatalf("ListInstances(tenant-b) = %d, want 1", len(got))
	}
}

func TestInstanceSingleVersion(t *testing.T) {
	runner := pluginharness.NewFakeRunner()
	m := newManager(runner)
	defer m.Close()

	mustRegister(t, m, "io.test.plugin", "0.1.0")
	mustRegister(t, m, "io.test.plugin", "0.2.0")

	inst := mustCreate(t, m, pluginhost.InstanceSpec{ID: "x", Tenant: "tenant-a", PluginID: "io.test.plugin", Version: "0.1.0"})
	if inst.Version != "0.1.0" {
		t.Fatalf("instance version = %q, want 0.1.0", inst.Version)
	}

	// The same tenant+id cannot bind a second version simultaneously.
	if _, err := m.CreateInstance(pluginhost.InstanceSpec{ID: "x", Tenant: "tenant-a", PluginID: "io.test.plugin", Version: "0.2.0"}); !errors.Is(err, pluginhost.ErrInstanceExists) {
		t.Fatalf("rebind error = %v, want ErrInstanceExists", err)
	}

	snap, err := m.Snapshot("tenant-a", "x")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Version != "0.1.0" {
		t.Fatalf("snapshot version = %q, want 0.1.0", snap.Version)
	}

	// A different tenant may reuse the instance id with a different version.
	inst2 := mustCreate(t, m, pluginhost.InstanceSpec{ID: "x", Tenant: "tenant-b", PluginID: "io.test.plugin", Version: "0.2.0"})
	if inst2.Version != "0.2.0" {
		t.Fatalf("tenant-b instance version = %q, want 0.2.0", inst2.Version)
	}
}

func TestManagerTenantIsolation(t *testing.T) {
	runner := pluginharness.NewFakeRunner()
	m := newManager(runner)
	defer m.Close()

	mustRegister(t, m, "io.test.plugin", "0.1.0")
	mustCreate(t, m, pluginhost.InstanceSpec{ID: "x", Tenant: "tenant-a", PluginID: "io.test.plugin", Version: "0.1.0", Config: map[string]string{"port": "COM3"}})
	mustStart(t, m, "tenant-a", "x")
	waitInstanceState(t, m, "tenant-a", "x", pluginhost.StateHealthy)

	// Reverse: tenant B must never see tenant A's instance.
	if _, err := m.Snapshot("tenant-b", "x"); !errors.Is(err, pluginhost.ErrInstanceNotFound) {
		t.Fatalf("Snapshot(tenant-b) error = %v, want ErrInstanceNotFound", err)
	}
	if _, err := m.Metrics("tenant-b", "x"); !errors.Is(err, pluginhost.ErrInstanceNotFound) {
		t.Fatalf("Metrics(tenant-b) error = %v, want ErrInstanceNotFound", err)
	}
	if err := m.Disable("tenant-b", "x"); !errors.Is(err, pluginhost.ErrInstanceNotFound) {
		t.Fatalf("Disable(tenant-b) error = %v, want ErrInstanceNotFound", err)
	}
	if err := m.Enable("tenant-b", "x"); !errors.Is(err, pluginhost.ErrInstanceNotFound) {
		t.Fatalf("Enable(tenant-b) error = %v, want ErrInstanceNotFound", err)
	}
	if _, err := m.Remove("tenant-b", "x"); !errors.Is(err, pluginhost.ErrInstanceNotFound) {
		t.Fatalf("Remove(tenant-b) error = %v, want ErrInstanceNotFound", err)
	}
	if got := m.ListInstances("tenant-b"); len(got) != 0 {
		t.Fatalf("ListInstances(tenant-b) = %d, want 0", len(got))
	}
	if got := m.ListInstances("tenant-a"); len(got) != 1 {
		t.Fatalf("ListInstances(tenant-a) = %d, want 1", len(got))
	}
}

func TestHealthDegradedAndRecovery(t *testing.T) {
	runner := pluginharness.NewFakeRunner()
	checker := &toggleChecker{degraded: true}
	m := newManager(runner, func(o *pluginhost.ManagerOptions) {
		o.HealthChecker = checker
		o.HealthFailureThreshold = 1000 // suppress policy while we flip health
	})
	defer m.Close()

	mustRegister(t, m, "io.test.plugin", "0.1.0")
	mustCreate(t, m, pluginhost.InstanceSpec{ID: "x", Tenant: "tenant-a", PluginID: "io.test.plugin", Version: "0.1.0"})
	mustStart(t, m, "tenant-a", "x")
	waitInstanceState(t, m, "tenant-a", "x", pluginhost.StateHealthy)

	waitInstanceHealth(t, m, "tenant-a", "x", pluginhost.HealthDegraded)
	checker.setDegraded(false)
	waitInstanceHealth(t, m, "tenant-a", "x", pluginhost.HealthHealthy)

	metrics, err := m.Metrics("tenant-a", "x")
	if err != nil {
		t.Fatalf("Metrics: %v", err)
	}
	if metrics.LastHealthy.IsZero() {
		t.Fatal("LastHealthy was not recorded after recovery")
	}
}

func TestHealthFailurePolicy(t *testing.T) {
	t.Run("disable", func(t *testing.T) {
		runner := pluginharness.NewFakeRunner()
		m := newManager(runner, func(o *pluginhost.ManagerOptions) {
			o.HealthChecker = degradedChecker{}
			o.HealthFailureThreshold = 2
			o.HealthFailurePolicy = pluginhost.HealthPolicyDisable
		})
		defer m.Close()

		mustRegister(t, m, "io.test.plugin", "0.1.0")
		mustCreate(t, m, pluginhost.InstanceSpec{ID: "x", Tenant: "tenant-a", PluginID: "io.test.plugin", Version: "0.1.0"})
		mustStart(t, m, "tenant-a", "x")
		waitInstanceState(t, m, "tenant-a", "x", pluginhost.StateHealthy)

		waitInstanceState(t, m, "tenant-a", "x", pluginhost.StateDisabled)
		snap, err := m.Snapshot("tenant-a", "x")
		if err != nil {
			t.Fatalf("Snapshot: %v", err)
		}
		if snap.Enabled {
			t.Fatal("instance still enabled after health-driven disable")
		}
		if snap.Crashes != 0 || snap.Restarts != 0 {
			t.Fatalf("health disable touched the crash budget: crashes=%d restarts=%d", snap.Crashes, snap.Restarts)
		}
	})

	t.Run("restart", func(t *testing.T) {
		runner := pluginharness.NewFakeRunner()
		m := newManager(runner, func(o *pluginhost.ManagerOptions) {
			o.HealthChecker = degradedChecker{}
			o.HealthFailureThreshold = 2
			o.HealthFailurePolicy = pluginhost.HealthPolicyRestart
		})
		defer m.Close()

		mustRegister(t, m, "io.test.plugin", "0.1.0")
		mustCreate(t, m, pluginhost.InstanceSpec{ID: "x", Tenant: "tenant-a", PluginID: "io.test.plugin", Version: "0.1.0"})
		mustStart(t, m, "tenant-a", "x")
		waitInstanceState(t, m, "tenant-a", "x", pluginhost.StateHealthy)

		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if runner.StartedCount() >= 2 {
				break
			}
			time.Sleep(2 * time.Millisecond)
		}
		if runner.StartedCount() < 2 {
			t.Fatalf("StartedCount = %d, want >= 2 (health restart relaunched the process)", runner.StartedCount())
		}

		snap, err := m.Snapshot("tenant-a", "x")
		if err != nil {
			t.Fatalf("Snapshot: %v", err)
		}
		if snap.Crashes != 0 || snap.Restarts != 0 {
			t.Fatalf("health restart touched the crash budget: crashes=%d restarts=%d", snap.Crashes, snap.Restarts)
		}
		metrics, err := m.Metrics("tenant-a", "x")
		if err != nil {
			t.Fatalf("Metrics: %v", err)
		}
		if metrics.RestartCount < 1 {
			t.Fatalf("Metrics.RestartCount = %d, want >= 1", metrics.RestartCount)
		}
	})
}

func TestMetricsSnapshot(t *testing.T) {
	runner := pluginharness.NewFakeRunner()
	collector := fixedCollector{cpu: 1500 * time.Millisecond, rss: 12 << 20, handles: 7, goroutines: 42, rate: 3.5}
	m := newManager(runner, func(o *pluginhost.ManagerOptions) {
		o.HealthChecker = healthyChecker{}
		o.MetricsCollector = collector
	})
	defer m.Close()

	mustRegister(t, m, "io.test.plugin", "0.1.0")
	mustCreate(t, m, pluginhost.InstanceSpec{ID: "x", Tenant: "tenant-a", PluginID: "io.test.plugin", Version: "0.1.0"})
	mustStart(t, m, "tenant-a", "x")
	waitInstanceState(t, m, "tenant-a", "x", pluginhost.StateHealthy)
	waitInstanceHealth(t, m, "tenant-a", "x", pluginhost.HealthHealthy)

	metrics, err := m.Metrics("tenant-a", "x")
	if err != nil {
		t.Fatalf("Metrics: %v", err)
	}
	if metrics.CPUTime != collector.cpu {
		t.Fatalf("CPUTime = %s, want %s", metrics.CPUTime, collector.cpu)
	}
	if metrics.RSSBytes != collector.rss {
		t.Fatalf("RSSBytes = %d, want %d", metrics.RSSBytes, collector.rss)
	}
	if metrics.Handles != 7 {
		t.Fatalf("Handles = %d, want 7", metrics.Handles)
	}
	if metrics.Goroutines != 42 {
		t.Fatalf("Goroutines = %d, want 42", metrics.Goroutines)
	}
	if metrics.MessageRate != 3.5 {
		t.Fatalf("MessageRate = %v, want 3.5", metrics.MessageRate)
	}
	if metrics.LastHealthy.IsZero() {
		t.Fatal("LastHealthy was not recorded")
	}
}

func TestRemovePreservesDataByDefault(t *testing.T) {
	runner := pluginharness.NewFakeRunner()
	m := newManager(runner)
	defer m.Close()

	mustRegister(t, m, "io.test.plugin", "0.1.0")
	mustCreate(t, m, pluginhost.InstanceSpec{ID: "x", Tenant: "tenant-a", PluginID: "io.test.plugin", Version: "0.1.0", Isolation: pluginhost.IsolationPerInstance})
	mustStart(t, m, "tenant-a", "x")
	waitInstanceState(t, m, "tenant-a", "x", pluginhost.StateHealthy)

	res, err := m.Remove("tenant-a", "x")
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if res.Purged {
		t.Fatal("Purged = true, want false by default")
	}
	if !res.DataPreserved {
		t.Fatal("DataPreserved = false, want true by default")
	}

	if _, err := m.Snapshot("tenant-a", "x"); !errors.Is(err, pluginhost.ErrInstanceNotFound) {
		t.Fatalf("Snapshot after remove = %v, want ErrInstanceNotFound", err)
	}
	if !runner.Started()[0].Exited() {
		t.Fatal("per-instance process still running after Remove")
	}
}

func TestExplicitPurge(t *testing.T) {
	runner := pluginharness.NewFakeRunner()
	m := newManager(runner)
	defer m.Close()

	mustRegister(t, m, "io.test.plugin", "0.1.0")
	mustCreate(t, m, pluginhost.InstanceSpec{ID: "x", Tenant: "tenant-a", PluginID: "io.test.plugin", Version: "0.1.0"})

	res, err := m.Remove("tenant-a", "x", pluginhost.WithPurge())
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !res.Purged {
		t.Fatal("Purged = false, want true with WithPurge")
	}
	if res.DataPreserved {
		t.Fatal("DataPreserved = true, want false with WithPurge")
	}
}
