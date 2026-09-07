package pluginhost_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/pluginhost"
	pluginharness "github.com/DeliciousBuding/cloud-path/testing/plugin-harness"
)

func TestManagerReconcileConcurrentReplay(t *testing.T) {
	runner := pluginharness.NewFakeRunner()
	m := newManager(runner)
	t.Cleanup(func() { _ = m.Close() })
	mustRegister(t, m, "io.test.plugin", "0.1.0")
	spec := pluginhost.InstanceSpec{Tenant: "tenant-a", ID: "worker", PluginID: "io.test.plugin", Version: "0.1.0", Config: map[string]string{"mode": "initial"}}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Go(func() { errs <- m.ReconcileInstance(context.Background(), spec, true) })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if runner.StartedCount() != 1 {
		t.Fatalf("concurrent identical replay started %d processes", runner.StartedCount())
	}
	// Neither an input map nor a returned snapshot may rewrite the active spec.
	spec.Config["mode"] = "caller-mutated"
	snap, err := m.Snapshot(spec.Tenant, spec.ID)
	if err != nil || snap.Config["mode"] != "initial" {
		t.Fatalf("aliased input config: %+v %v", snap, err)
	}
}

func TestManagerReconcileDisabledDefinition(t *testing.T) {
	runner := pluginharness.NewFakeRunner()
	m := newManager(runner)
	t.Cleanup(func() { _ = m.Close() })
	mustRegister(t, m, "io.test.plugin", "0.1.0")
	spec := pluginhost.InstanceSpec{Tenant: "tenant-a", ID: "worker", PluginID: "io.test.plugin", Version: "0.1.0", Config: map[string]string{"mode": "initial"}}
	created := mustCreate(t, m, spec)
	created.Config["mode"] = "returned-map-mutated"
	if snap, _ := m.Snapshot(spec.Tenant, spec.ID); snap.Config["mode"] != "initial" {
		t.Fatalf("CreateInstance exposed its mutable definition: %+v", snap)
	}
	if err := m.ReconcileInstance(context.Background(), spec, true); err != nil {
		t.Fatal(err)
	}
	spec.Version = "0.2.0"
	spec.Config = map[string]string{"mode": "disabled-config"}
	spec.Isolation = pluginhost.IsolationPerInstance
	if err := m.ReconcileInstance(context.Background(), spec, false); err != nil {
		t.Fatal(err)
	}
	snap, err := m.Snapshot(spec.Tenant, spec.ID)
	if err != nil || snap.Enabled || snap.Version != spec.Version || snap.Isolation != spec.Isolation || snap.Config["mode"] != "disabled-config" {
		t.Fatalf("disabled definition not updated: %+v %v", snap, err)
	}
	if !runner.Started()[0].Exited() {
		t.Fatal("disabled old process still running")
	}
	if err := m.Enable(spec.Tenant, spec.ID); !errors.Is(err, pluginhost.ErrInstallationNotFound) {
		t.Fatalf("uninstalled disabled version started: %v", err)
	}
	mustRegister(t, m, "io.test.plugin", "0.2.0")
	if err := m.ReconcileInstance(context.Background(), spec, true); err != nil {
		t.Fatal(err)
	}
	snap, err = m.Snapshot(spec.Tenant, spec.ID)
	if err != nil || !snap.Enabled || snap.Version != spec.Version || runner.StartedCount() != 2 {
		t.Fatalf("disabled configuration not applied on enable: %+v %v", snap, err)
	}
}

type staleReconcileChecker struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *staleReconcileChecker) Check(ctx context.Context, target pluginhost.HealthTarget) (pluginhost.Health, error) {
	if target.Version == "0.1.0" {
		c.once.Do(func() { close(c.entered) })
		select {
		case <-c.release:
		case <-ctx.Done():
			return pluginhost.HealthUnknown, ctx.Err()
		}
		return pluginhost.HealthDegraded, nil
	}
	return pluginhost.HealthHealthy, nil
}
func TestManagerReconcileIgnoresOldHealthProbe(t *testing.T) {
	runner := pluginharness.NewFakeRunner()
	checker := &staleReconcileChecker{entered: make(chan struct{}), release: make(chan struct{})}
	m := newManager(runner, func(o *pluginhost.ManagerOptions) { o.HealthChecker = checker; o.HealthFailureThreshold = 1 })
	t.Cleanup(func() { _ = m.Close() })
	mustRegister(t, m, "io.test.plugin", "0.1.0")
	mustRegister(t, m, "io.test.plugin", "0.2.0")
	spec := pluginhost.InstanceSpec{Tenant: "tenant-a", ID: "worker", PluginID: "io.test.plugin", Version: "0.1.0"}
	if err := m.ReconcileInstance(context.Background(), spec, true); err != nil {
		t.Fatal(err)
	}
	select {
	case <-checker.entered:
	case <-time.After(time.Second):
		t.Fatal("health probe did not begin")
	}
	spec.Version = "0.2.0"
	if err := m.ReconcileInstance(context.Background(), spec, true); err != nil {
		t.Fatal(err)
	}
	close(checker.release)
	waitInstanceHealth(t, m, spec.Tenant, spec.ID, pluginhost.HealthHealthy)
	snap, err := m.Snapshot(spec.Tenant, spec.ID)
	if err != nil || !snap.Enabled || snap.Version != "0.2.0" || snap.State != pluginhost.StateHealthy {
		t.Fatalf("old health failure disabled the replacement: %+v %v", snap, err)
	}
}

func TestManagerReconcileApplicationProtocol(t *testing.T) {
	runner := newRecordingRunner()
	m := pluginhost.NewManager(pluginhost.ManagerOptions{
		Runner: runner, CommandArgs: []string{"-test.run=^TestHelperProcess$"}, CommandEnv: []string{helperProcessEnv + "=1"},
		HandshakeTimeout: time.Second, ShutdownTimeout: time.Second,
	})
	t.Cleanup(func() { _ = m.Close() })
	if err := m.RegisterInstallation(pluginhost.Installation{PluginID: "io.test.application", Version: "0.1.0", Path: os.Args[0], Kind: pluginhost.KindApplication}); err != nil {
		t.Fatal(err)
	}
	spec := pluginhost.InstanceSpec{Tenant: "tenant-a", ID: "app", PluginID: "io.test.application", Version: "0.1.0", Config: map[string]string{"mode": "initial"}}
	if err := m.ReconcileInstance(context.Background(), spec, true); err != nil {
		t.Fatal(err)
	}
	spec.Config = map[string]string{"mode": "updated"}
	if err := m.ReconcileInstance(context.Background(), spec, true); err != nil {
		t.Fatal(err)
	}
	cli, err := m.ApplicationClient(spec.PluginID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cli.Health(context.Background()); err != nil {
		t.Fatal(err)
	}
	snap, err := m.Snapshot(spec.Tenant, spec.ID)
	if err != nil || snap.Config["mode"] != "updated" || runner.StartCount() != 1 {
		t.Fatalf("application config failed or restarted process: %+v %v", snap, err)
	}
}

func TestManagerReconcileReusesSharedTargetVersion(t *testing.T) {
	runner := pluginharness.NewFakeRunner()
	m := newManager(runner)
	t.Cleanup(func() { _ = m.Close() })
	mustRegister(t, m, "io.test.plugin", "0.1.0")
	mustRegister(t, m, "io.test.plugin", "0.2.0")
	a := pluginhost.InstanceSpec{Tenant: "tenant-a", ID: "worker", PluginID: "io.test.plugin", Version: "0.1.0", Config: map[string]string{"mode": "a"}}
	b, c := a, a
	b.Tenant = "tenant-b"
	c.Tenant = "tenant-c"
	c.Version = "0.2.0"
	for _, s := range []pluginhost.InstanceSpec{a, b, c} {
		if err := m.ReconcileInstance(context.Background(), s, true); err != nil {
			t.Fatal(err)
		}
	}
	a.Version = "0.2.0"
	if err := m.ReconcileInstance(context.Background(), a, true); err != nil {
		t.Fatal(err)
	}
	if runner.StartedCount() != 2 {
		t.Fatalf("rebind did not reuse the serving version: launches=%d", runner.StartedCount())
	}
	if runner.Started()[0].Exited() || runner.Started()[1].Exited() {
		t.Fatal("rebind stopped a source/target sibling")
	}
	if err := m.ReconcileInstance(context.Background(), a, false); err != nil {
		t.Fatal(err)
	}
	if err := m.ReconcileInstance(context.Background(), b, false); err != nil {
		t.Fatal(err)
	}
	if !runner.Started()[0].Exited() || runner.Started()[1].Exited() {
		t.Fatal("shared reference release affected the wrong version")
	}
	snap, err := m.Snapshot(c.Tenant, c.ID)
	if err != nil || !snap.Enabled || snap.Version != "0.2.0" || snap.State != pluginhost.StateHealthy {
		t.Fatalf("target tenant stopped: %+v %v", snap, err)
	}
}
