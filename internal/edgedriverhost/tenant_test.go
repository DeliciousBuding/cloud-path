package edgedriverhost

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/plugincontrol"
	"github.com/DeliciousBuding/cloud-path/internal/pluginhost"
	"github.com/DeliciousBuding/cloud-path/internal/registry"
)

type recordingManager struct {
	mu      sync.Mutex
	created []string
	started []string
	closed  bool
}

func (m *recordingManager) RegisterInstallation(pluginhost.Installation) error { return nil }
func (m *recordingManager) CreateInstance(spec pluginhost.InstanceSpec) (pluginhost.Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.created = append(m.created, spec.Tenant+"/"+spec.ID)
	return pluginhost.Instance{ID: spec.ID, Tenant: spec.Tenant, PluginID: spec.PluginID, Version: spec.Version}, nil
}
func (m *recordingManager) Start(tenant, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.started = append(m.started, tenant+"/"+id)
	return nil
}
func (m *recordingManager) Disable(tenant, id string) error { return nil }
func (m *recordingManager) Remove(tenant, id string, _ ...pluginhost.RemoveOption) (pluginhost.RemoveResult, error) {
	return pluginhost.RemoveResult{DataPreserved: true}, nil
}
func (m *recordingManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}
func (m *recordingManager) snapshot() (created, started []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.created...), append([]string(nil), m.started...)
}

func setupDriverState(t *testing.T) (root, lockPath, stateDir string) {
	t.Helper()
	root = t.TempDir()
	writeManifest(t, root, "io.github.example.driver-stcb", "contributes:\n  drivers:\n    - id: stcb\n")
	lock := registry.NewLockFile()
	lock.Plugins = []registry.LockedPlugin{{ID: "io.github.example.driver-stcb", Version: "0.1.0", Digest: "d1"}}
	lockPath = filepath.Join(root, "plugins.lock")
	if err := registry.WriteLockFile(lockPath, lock); err != nil {
		t.Fatal(err)
	}
	stateDir = filepath.Join(root, "state")
	return root, lockPath, stateDir
}

func saveEnabledState(t *testing.T, stateDir, tenant, instanceID string) {
	t.Helper()
	if err := plugincontrol.NewStore(stateDir).Save(plugincontrol.InstanceState{
		Tenant:     tenant,
		InstanceID: instanceID,
		PluginID:   "io.github.example.driver-stcb",
		Version:    "0.1.0",
		Enabled:    true,
		Isolation:  plugincontrol.IsolationShared,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestEdgeHostTenantIsolation(t *testing.T) {
	root, lockPath, stateDir := setupDriverState(t)
	saveEnabledState(t, stateDir, "tenant-a", "i-a")
	saveEnabledState(t, stateDir, "tenant-b", "i-b")

	rec := &recordingManager{}
	h, err := New(Options{
		Manager:      rec,
		PluginsDir:   root,
		StateDir:     stateDir,
		LockPath:     lockPath,
		Tenant:       "tenant-a",
		CloseTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	created, started := rec.snapshot()
	want := "tenant-a/i-a"
	if len(created) != 1 || created[0] != want {
		t.Fatalf("CreateInstance = %v, want [%s] only（不得创建 tenant-b）", created, want)
	}
	if len(started) != 1 || started[0] != want {
		t.Fatalf("Start = %v, want [%s] only（不得启动 tenant-b）", started, want)
	}
}

func TestEmptyTenantDefaultsToDefault(t *testing.T) {
	root, lockPath, stateDir := setupDriverState(t)
	saveEnabledState(t, stateDir, "default", "i-default")
	saveEnabledState(t, stateDir, "tenant-a", "i-a")

	rec := &recordingManager{}
	h, err := New(Options{
		Manager:      rec,
		PluginsDir:   root,
		StateDir:     stateDir,
		LockPath:     lockPath,
		Tenant:       "",
		CloseTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	created, started := rec.snapshot()
	want := "default/i-default"
	if len(created) != 1 || created[0] != want {
		t.Fatalf("空 tenant 应按 default 创建实例，CreateInstance = %v, want [%s]", created, want)
	}
	if len(started) != 1 || started[0] != want {
		t.Fatalf("空 tenant 应按 default 启动实例，Start = %v, want [%s]", started, want)
	}
}
