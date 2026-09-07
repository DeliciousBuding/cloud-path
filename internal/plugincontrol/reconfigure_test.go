package plugincontrol_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/plugincontrol"
	"github.com/DeliciousBuding/cloud-path/internal/pluginhost"
	pluginharness "github.com/DeliciousBuding/cloud-path/testing/plugin-harness"
)

func TestApplySnapshotReconfiguresExistingInstance(t *testing.T) {
	for _, change := range []string{"version", "plugin", "config", "isolation"} {
		t.Run(change, func(t *testing.T) {
			runner := pluginharness.NewFakeRunner()
			manager := pluginhost.NewManager(pluginhost.ManagerOptions{
				Runner: runner, HandshakeTimeout: time.Second, ShutdownTimeout: 100 * time.Millisecond,
			})
			t.Cleanup(func() { _ = manager.Close() })
			host, store, pluginsDir, lockPath := newApplierHost(t, manager, nil)
			desired := api.PluginDesiredInstanceData{
				InstanceID: "worker", PluginID: testPluginID, Version: "0.1.0", Enabled: true,
				Isolation: plugincontrol.IsolationShared, Config: map[string]string{"mode": "safe"},
			}
			apply := func() {
				t.Helper()
				results, err := host.ApplySnapshot(context.Background(), "tenant-a", []api.PluginDesiredInstanceData{desired})
				if err != nil || len(results) != 1 || results[0].Status != api.PluginAckApplied {
					t.Fatalf("ApplySnapshot = %+v, %v", results, err)
				}
			}
			apply()
			waitReconfigured(t, manager, "tenant-a", desired.InstanceID)
			switch change {
			case "version":
				writeTestPlugin(t, pluginsDir, lockPath, "0.2.0", nil, nil)
				desired.Version = "0.2.0"
			case "plugin":
				desired.PluginID = "io.test.other"
				if err := manager.RegisterInstallation(pluginhost.Installation{PluginID: desired.PluginID, Version: desired.Version, Path: "fake-plugin"}); err != nil {
					t.Fatal(err)
				}
			case "config":
				desired.Config = map[string]string{"mode": "changed"}
			case "isolation":
				desired.Isolation = plugincontrol.IsolationPerInstance
			}
			apply()
			snap := waitReconfigured(t, manager, "tenant-a", desired.InstanceID)
			if snap.PluginID != desired.PluginID || snap.Version != desired.Version || snap.Isolation.String() != desired.Isolation || !reflect.DeepEqual(snap.Config, desired.Config) {
				t.Fatalf("applied ACK without runtime convergence: snapshot=%+v desired=%+v", snap, desired)
			}
			state, err := store.Load("tenant-a", desired.InstanceID)
			if err != nil || state.PluginID != desired.PluginID || state.Version != desired.Version || !reflect.DeepEqual(state.Config, desired.Config) {
				t.Fatalf("persisted state = %+v, %v", state, err)
			}
			launches := runner.StartedCount()
			wantLaunches := 2
			if change == "config" {
				wantLaunches = 1
			}
			if launches != wantLaunches {
				t.Fatalf("process launches = %d, want %d", launches, wantLaunches)
			}
			if got := runner.Started()[launches-1].Env()[pluginhost.EnvPluginID]; got != desired.PluginID {
				t.Fatalf("running plugin identity = %q, want %q", got, desired.PluginID)
			}
			apply()
			if got := runner.StartedCount(); got != launches {
				t.Fatalf("identical replay restarted process: %d -> %d", launches, got)
			}
		})
	}
}

func waitReconfigured(t *testing.T, manager *pluginhost.Manager, tenant, id string) pluginhost.InstanceSnapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		snap, err := manager.Snapshot(tenant, id)
		if err != nil {
			t.Fatal(err)
		}
		if snap.State == pluginhost.StateHealthy {
			return snap
		}
		time.Sleep(5 * time.Millisecond)
	}
	snap, _ := manager.Snapshot(tenant, id)
	t.Fatalf("instance never became healthy: %+v", snap)
	return snap
}

// The runner still uses the real launch socket and SDK protocol. Only process
// creation failure is injected; reconciliation, the Store and Syncer are real.
type reconcileFailRunner struct {
	inner *pluginharness.FakeRunner
	mu    sync.Mutex
	fail  bool
}

func (r *reconcileFailRunner) Start(spec pluginhost.CommandSpec) (pluginhost.Process, error) {
	r.mu.Lock()
	fail := r.fail
	r.mu.Unlock()
	if fail {
		return nil, errors.New("test process start failed")
	}
	return r.inner.Start(spec)
}
func (r *reconcileFailRunner) setFail(fail bool) { r.mu.Lock(); defer r.mu.Unlock(); r.fail = fail }

func TestReconfigureFailureKeepsAppliedStateAndRevision(t *testing.T) {
	for _, failure := range []string{"uninstalled", "start", "secret", "invalid-config"} {
		t.Run(failure, func(t *testing.T) {
			runner := &reconcileFailRunner{inner: pluginharness.NewFakeRunner()}
			manager := pluginhost.NewManager(pluginhost.ManagerOptions{Runner: runner, HandshakeTimeout: time.Second, ShutdownTimeout: 100 * time.Millisecond})
			t.Cleanup(func() { _ = manager.Close() })
			host, store, pluginsDir, lockPath := newApplierHost(t, manager, nil)
			syncer, cachePath := newTestSyncer(t, host)
			old := api.PluginDesiredInstanceData{InstanceID: "worker", PluginID: testPluginID, Version: "0.1.0", Enabled: true, Config: map[string]string{"mode": "initial"}}
			first := api.PluginDesiredData{Revision: 1, SnapshotDigest: "revision-one", Instances: []api.PluginDesiredInstanceData{old}}
			if ack := syncer.HandleDesired(context.Background(), first); ack.Status != api.PluginAckApplied {
				t.Fatalf("initial ack=%+v", ack)
			}
			statePath := filepath.Join(store.Dir, "tenant-a", "worker.json")
			oldState, err := os.ReadFile(statePath)
			if err != nil {
				t.Fatal(err)
			}
			oldCache, err := os.ReadFile(cachePath)
			if err != nil {
				t.Fatal(err)
			}
			next := old
			next.Version = "0.2.0"
			if failure != "uninstalled" {
				writeTestPlugin(t, pluginsDir, lockPath, next.Version, nil, nil)
			}
			switch failure {
			case "start":
				runner.setFail(true)
			case "secret":
				next.Config = map[string]string{"key": "secret://missing"}
			case "invalid-config":
				next.Config = map[string]string{"bad\nkey": "value"}
			}
			ack := syncer.HandleDesired(context.Background(), api.PluginDesiredData{Revision: 2, SnapshotDigest: "revision-two", Instances: []api.PluginDesiredInstanceData{next}})
			if ack.Status != api.PluginAckFailed || syncer.AppliedRevision() != 1 {
				t.Fatalf("failed update advanced applied revision: ack=%+v applied=%d", ack, syncer.AppliedRevision())
			}
			for path, want := range map[string][]byte{statePath: oldState, cachePath: oldCache} {
				got, err := os.ReadFile(path)
				if err != nil || string(got) != string(want) {
					t.Fatalf("failed update rewrote applied state/cache: %v", err)
				}
			}
			snap, err := manager.Snapshot("tenant-a", "worker")
			if err != nil || snap.Version != old.Version || !snap.Enabled || !reflect.DeepEqual(snap.Config, old.Config) || runner.inner.Started()[0].Exited() {
				t.Fatalf("failed update damaged last instance: %+v %v", snap, err)
			}
			// A corrected later snapshot remains applicable; failure is not cached as
			// success, and the last-good process is retired only on this successful retry.
			runner.setFail(false)
			writeTestPlugin(t, pluginsDir, lockPath, "0.2.0", nil, nil)
			next = old
			next.Version = "0.2.0"
			ack = syncer.HandleDesired(context.Background(), api.PluginDesiredData{Revision: 3, SnapshotDigest: "revision-three", Instances: []api.PluginDesiredInstanceData{next}})
			if ack.Status != api.PluginAckApplied || syncer.AppliedRevision() != 3 {
				t.Fatalf("corrected update failed: %+v", ack)
			}
			snap, err = manager.Snapshot("tenant-a", "worker")
			if err != nil || snap.Version != "0.2.0" || !runner.inner.Started()[0].Exited() {
				t.Fatalf("corrected update did not switch: %+v %v", snap, err)
			}
		})
	}
}

func TestReconfigurePreservesLocalConfigPath(t *testing.T) {
	runner := pluginharness.NewFakeRunner()
	manager := pluginhost.NewManager(pluginhost.ManagerOptions{Runner: runner, HandshakeTimeout: time.Second, ShutdownTimeout: 100 * time.Millisecond})
	t.Cleanup(func() { _ = manager.Close() })
	host, store, pluginsDir, lockPath := newApplierHost(t, manager, nil)
	state := plugincontrol.InstanceState{Tenant: "tenant-a", InstanceID: "worker", PluginID: testPluginID, Version: "0.1.0", Enabled: true, Isolation: plugincontrol.IsolationShared, ConfigPath: "local-config.json", Config: map[string]string{"mode": "initial"}}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	if _, err := host.LoadTenant(context.Background(), "tenant-a"); err != nil {
		t.Fatal(err)
	}
	writeTestPlugin(t, pluginsDir, lockPath, "0.2.0", nil, nil)
	results, err := host.ApplySnapshot(context.Background(), "tenant-a", []api.PluginDesiredInstanceData{{InstanceID: "worker", PluginID: testPluginID, Version: "0.2.0", Enabled: true, Config: state.Config}})
	if err != nil || len(results) != 1 || results[0].Status != api.PluginAckApplied {
		t.Fatalf("ApplySnapshot=%+v %v", results, err)
	}
	saved, err := store.Load("tenant-a", "worker")
	if err != nil || saved.ConfigPath != state.ConfigPath || !reflect.DeepEqual(saved.Config, state.Config) {
		t.Fatalf("local configuration lost: %+v %v", saved, err)
	}
	snap, err := manager.Snapshot("tenant-a", "worker")
	if err != nil || snap.Config["path"] != state.ConfigPath || snap.Config["mode"] != "initial" {
		t.Fatalf("runtime lost configuration: %+v %v", snap, err)
	}
}

type existingLegacyManager struct{ fakeHostManager }

func (m *existingLegacyManager) CreateInstance(pluginhost.InstanceSpec) (pluginhost.Instance, error) {
	return pluginhost.Instance{}, pluginhost.ErrInstanceExists
}
func TestApplySnapshotLegacyManagerDoesNotClaimReconfigure(t *testing.T) {
	host, store, _, _ := newApplierHost(t, &existingLegacyManager{}, nil)
	results, err := host.ApplySnapshot(context.Background(), "tenant-a", []api.PluginDesiredInstanceData{{InstanceID: "worker", PluginID: testPluginID, Version: "0.1.0", Enabled: true}})
	if err != nil || len(results) != 1 || results[0].Status != api.PluginAckFailed {
		t.Fatalf("unsupported reconfigure accepted: %+v %v", results, err)
	}
	if _, err := store.Load("tenant-a", "worker"); !errors.Is(err, plugincontrol.ErrNotFound) {
		t.Fatalf("unsupported reconfigure persisted: %v", err)
	}
}

// A stopped child whose Wait confirmation is delayed must not be forgotten.
// The fake child still speaks the real authenticated RPC protocol; only the
// OS exit confirmation is controlled, with no production devices involved.
type delayedExitRunner struct {
	inner   *pluginharness.FakeRunner
	release <-chan struct{}
}

func (r *delayedExitRunner) Start(spec pluginhost.CommandSpec) (pluginhost.Process, error) {
	p, err := r.inner.Start(spec)
	if err != nil || r.inner.StartedCount() != 1 {
		return p, err
	}
	return &delayedExitProcess{Process: p, release: r.release}, nil
}

type delayedExitProcess struct {
	pluginhost.Process
	release <-chan struct{}
}

func (p *delayedExitProcess) Wait() error {
	err := p.Process.Wait()
	<-p.release
	return err
}

func TestReconfigureWaitsForRetiredProcessOnRetry(t *testing.T) {
	for _, change := range []string{"version", "disable", "delete"} {
		t.Run(change, func(t *testing.T) {
			release := make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()
			runner := &delayedExitRunner{inner: pluginharness.NewFakeRunner(), release: release}
			manager := pluginhost.NewManager(pluginhost.ManagerOptions{Runner: runner, HandshakeTimeout: time.Second, ShutdownTimeout: 10 * time.Millisecond})
			t.Cleanup(func() { _ = manager.Close() })
			host, store, pluginsDir, lockPath := newApplierHost(t, manager, nil)
			syncer, cachePath := newTestSyncer(t, host)
			old := api.PluginDesiredInstanceData{InstanceID: "worker", PluginID: testPluginID, Version: "0.1.0", Enabled: true}
			first := api.PluginDesiredData{Revision: 1, SnapshotDigest: "revision-one", Instances: []api.PluginDesiredInstanceData{old}}
			if ack := syncer.HandleDesired(context.Background(), first); ack.Status != api.PluginAckApplied {
				t.Fatalf("initial ACK=%+v", ack)
			}
			statePath := filepath.Join(store.Dir, "tenant-a", "worker.json")
			oldState, err := os.ReadFile(statePath)
			if err != nil {
				t.Fatal(err)
			}
			oldCache, err := os.ReadFile(cachePath)
			if err != nil {
				t.Fatal(err)
			}
			next := old
			wantLaunches := 1
			if change == "version" {
				writeTestPlugin(t, pluginsDir, lockPath, "0.2.0", nil, nil)
				next.Version = "0.2.0"
				wantLaunches = 2
			} else {
				next.Enabled = false
			}
			desired := api.PluginDesiredData{Revision: 2, SnapshotDigest: "revision-two", Instances: []api.PluginDesiredInstanceData{next}}
			if change == "delete" {
				desired.Instances = nil
			}
			for attempt := 1; attempt <= 2; attempt++ {
				ack := syncer.HandleDesired(context.Background(), desired)
				if ack.Status != api.PluginAckFailed || syncer.AppliedRevision() != 1 {
					t.Fatalf("attempt %d acknowledged before old exit was confirmed: ACK=%+v applied=%d", attempt, ack, syncer.AppliedRevision())
				}
				for path, want := range map[string][]byte{statePath: oldState, cachePath: oldCache} {
					got, err := os.ReadFile(path)
					if err != nil || string(got) != string(want) {
						t.Fatalf("attempt %d rewrote durable state/cache: %v", attempt, err)
					}
				}
			}
			// A runtime change may already have happened: failure is not rollback.
			if change != "delete" {
				if snap, err := manager.Snapshot("tenant-a", "worker"); err != nil || snap.Version != next.Version || snap.Enabled != next.Enabled {
					t.Fatalf("unexpected forward-converged runtime: %+v %v", snap, err)
				}
			}
			unblock()
			if ack := syncer.HandleDesired(context.Background(), desired); ack.Status != api.PluginAckApplied || syncer.AppliedRevision() != 2 {
				t.Fatalf("same revision did not finish after confirmed exit: %+v", ack)
			}
			if runner.inner.StartedCount() != wantLaunches {
				t.Fatalf("retry needlessly launched another process: %d", runner.inner.StartedCount())
			}
			saved, err := store.Load("tenant-a", "worker")
			if change == "delete" {
				if !errors.Is(err, plugincontrol.ErrNotFound) {
					t.Fatalf("confirmed deletion left state behind: %+v %v", saved, err)
				}
			} else if err != nil || saved.Version != next.Version || saved.Enabled != next.Enabled {
				t.Fatalf("successful retry did not persist runtime: %+v %v", saved, err)
			}
		})
	}
}

// Force Store.Save to fail only after the real Manager has switched. This does
// not add production hooks or replace the Store/Syncer with mocks.
type afterReconcileManager struct {
	*pluginhost.Manager
	after func()
}

func (m *afterReconcileManager) ReconcileInstance(ctx context.Context, spec pluginhost.InstanceSpec, enabled bool) error {
	if err := m.Manager.ReconcileInstance(ctx, spec, enabled); err != nil {
		return err
	}
	if m.after != nil {
		m.after()
	}
	return nil
}

func TestReconfigureStoreFailureRetriesWithoutRuntimeRollbackClaim(t *testing.T) {
	runner := pluginharness.NewFakeRunner()
	manager := &afterReconcileManager{Manager: pluginhost.NewManager(pluginhost.ManagerOptions{Runner: runner, HandshakeTimeout: time.Second, ShutdownTimeout: 100 * time.Millisecond})}
	t.Cleanup(func() { _ = manager.Close() })
	host, store, pluginsDir, lockPath := newApplierHost(t, manager, nil)
	syncer, cachePath := newTestSyncer(t, host)
	old := api.PluginDesiredInstanceData{InstanceID: "worker", PluginID: testPluginID, Version: "0.1.0", Enabled: true, Config: map[string]string{"mode": "initial"}}
	if ack := syncer.HandleDesired(context.Background(), api.PluginDesiredData{Revision: 1, SnapshotDigest: "revision-one", Instances: []api.PluginDesiredInstanceData{old}}); ack.Status != api.PluginAckApplied {
		t.Fatalf("initial ACK=%+v", ack)
	}
	stateDir := store.Dir
	statePath := filepath.Join(stateDir, "tenant-a", "worker.json")
	oldState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	oldCache, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	blockedDir := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockedDir, []byte("block Save"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager.after = func() { store.Dir = blockedDir }
	writeTestPlugin(t, pluginsDir, lockPath, "0.2.0", nil, nil)
	next := old
	next.Version = "0.2.0"
	next.Config = map[string]string{"mode": "updated"}
	desired := api.PluginDesiredData{Revision: 2, SnapshotDigest: "revision-two", Instances: []api.PluginDesiredInstanceData{next}}
	if ack := syncer.HandleDesired(context.Background(), desired); ack.Status != api.PluginAckFailed || syncer.AppliedRevision() != 1 {
		t.Fatalf("failed Store.Save was acknowledged: %+v", ack)
	}
	for path, want := range map[string][]byte{statePath: oldState, cachePath: oldCache} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != string(want) {
			t.Fatalf("failed save rewrote last persisted state/cache: %v", err)
		}
	}
	if snap, err := manager.Snapshot("tenant-a", "worker"); err != nil || snap.Version != next.Version || !reflect.DeepEqual(snap.Config, next.Config) || !runner.Started()[0].Exited() {
		t.Fatalf("test did not reach switched runtime before Save failed: %+v %v", snap, err)
	}
	manager.after = nil
	store.Dir = stateDir
	if ack := syncer.HandleDesired(context.Background(), desired); ack.Status != api.PluginAckApplied || syncer.AppliedRevision() != 2 {
		t.Fatalf("same failed revision could not be retried: %+v", ack)
	}
	if runner.StartedCount() != 2 {
		t.Fatalf("durability-only retry restarted runtime: %d", runner.StartedCount())
	}
	if saved, err := store.Load("tenant-a", "worker"); err != nil || saved.Version != next.Version || !reflect.DeepEqual(saved.Config, next.Config) {
		t.Fatalf("retry failed to persist matching runtime: %+v %v", saved, err)
	}
}

func TestEmptySnapshotFailuresDoNotAdvanceRevision(t *testing.T) {
	for _, failure := range []string{"registry", "state-read"} {
		t.Run(failure, func(t *testing.T) {
			runner := pluginharness.NewFakeRunner()
			manager := pluginhost.NewManager(pluginhost.ManagerOptions{Runner: runner, HandshakeTimeout: time.Second, ShutdownTimeout: 100 * time.Millisecond})
			t.Cleanup(func() { _ = manager.Close() })
			host, store, _, lockPath := newApplierHost(t, manager, nil)
			syncer, _ := newTestSyncer(t, host)
			old := api.PluginDesiredInstanceData{InstanceID: "worker", PluginID: testPluginID, Version: "0.1.0", Enabled: true}
			if ack := syncer.HandleDesired(context.Background(), api.PluginDesiredData{Revision: 1, SnapshotDigest: "one", Instances: []api.PluginDesiredInstanceData{old}}); ack.Status != api.PluginAckApplied {
				t.Fatalf("initial ACK=%+v", ack)
			}
			lock, err := os.ReadFile(lockPath)
			if err != nil {
				t.Fatal(err)
			}
			stateDir := store.Dir
			if failure == "registry" {
				if err := os.WriteFile(lockPath, []byte("invalid: ["), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				store.Dir = t.TempDir()
				tenantDir := filepath.Join(store.Dir, "tenant-a")
				if err := os.Mkdir(tenantDir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(tenantDir, "worker.json"), []byte("invalid JSON"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			desired := api.PluginDesiredData{Revision: 2, SnapshotDigest: "empty"}
			ack := syncer.HandleDesired(context.Background(), desired)
			if ack.Status != api.PluginAckFailed || syncer.AppliedRevision() != 1 || len(ack.Results) != 1 || ack.Results[0].Status != api.PluginAckFailed || ack.Results[0].Detail == "" {
				t.Fatalf("empty snapshot lost failure or diagnostic: %+v applied=%d", ack, syncer.AppliedRevision())
			}
			store.Dir = stateDir
			if err := os.WriteFile(lockPath, lock, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Load("tenant-a", "worker"); err != nil {
				t.Fatalf("failed retirement removed state: %v", err)
			}
			if runner.Started()[0].Exited() {
				t.Fatal("failed preflight stopped the existing process")
			}
			if ack := syncer.HandleDesired(context.Background(), desired); ack.Status != api.PluginAckApplied || syncer.AppliedRevision() != 2 {
				t.Fatalf("empty snapshot retry failed: %+v", ack)
			}
			if _, err := store.Load("tenant-a", "worker"); !errors.Is(err, plugincontrol.ErrNotFound) {
				t.Fatalf("retry did not delete state: %v", err)
			}
		})
	}
}
