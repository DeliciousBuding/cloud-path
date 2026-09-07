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
