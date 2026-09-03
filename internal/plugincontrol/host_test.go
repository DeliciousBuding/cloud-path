package plugincontrol_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/DeliciousBuding/cloud-path/internal/plugincontrol"
	"github.com/DeliciousBuding/cloud-path/internal/pluginhost"
)

type fakeHostManager struct {
	mu            sync.Mutex
	installations []pluginhost.Installation
	created       []pluginhost.InstanceSpec
	started       []string
	disabled      []string
	removed       []string
	closed        bool
}

func (f *fakeHostManager) RegisterInstallation(inst pluginhost.Installation) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.installations = append(f.installations, inst)
	return nil
}

func (f *fakeHostManager) CreateInstance(spec pluginhost.InstanceSpec) (pluginhost.Instance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, spec)
	return pluginhost.Instance{
		ID:        spec.ID,
		Tenant:    spec.Tenant,
		PluginID:  spec.PluginID,
		Version:   spec.Version,
		Config:    spec.Config,
		Isolation: spec.Isolation,
	}, nil
}

func (f *fakeHostManager) Start(tenant, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = append(f.started, tenant+"/"+id)
	return nil
}

func (f *fakeHostManager) Disable(tenant, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.disabled = append(f.disabled, tenant+"/"+id)
	return nil
}

func (f *fakeHostManager) Remove(tenant, id string, _ ...pluginhost.RemoveOption) (pluginhost.RemoveResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, tenant+"/"+id)
	return pluginhost.RemoveResult{DataPreserved: true}, nil
}

func (f *fakeHostManager) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakeHostManager) snapshot() (installs int, created []pluginhost.InstanceSpec, started []string, closed bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.installations), append([]pluginhost.InstanceSpec(nil), f.created...), append([]string(nil), f.started...), f.closed
}

func TestHostLoadsEnabledInstances(t *testing.T) {
	root := t.TempDir()
	pluginsDir := filepath.Join(root, "plugins.d")
	lockPath := filepath.Join(root, "plugins.lock")
	store := plugincontrol.NewStore(filepath.Join(root, "state"))
	writeTestPlugin(t, pluginsDir, lockPath, "0.1.0", nil, nil)

	if err := store.Save(plugincontrol.InstanceState{
		Tenant: "tenant-a", InstanceID: "enabled", PluginID: testPluginID,
		Version: "0.1.0", Enabled: true, Isolation: plugincontrol.IsolationShared,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(plugincontrol.InstanceState{
		Tenant: "tenant-a", InstanceID: "disabled", PluginID: testPluginID,
		Version: "0.1.0", Enabled: false, Isolation: plugincontrol.IsolationShared,
	}); err != nil {
		t.Fatal(err)
	}

	manager := &fakeHostManager{}
	host, err := plugincontrol.NewHost(plugincontrol.HostOptions{
		Manager:    manager,
		Store:      store,
		PluginsDir: pluginsDir,
		LockPath:   lockPath,
	})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}

	res, err := host.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if res.Idle {
		t.Fatal("host reported idle with an enabled instance")
	}
	if res.Installations != 1 || res.Instances != 1 || res.Started != 1 {
		t.Fatalf("Load result = %+v, want one installation and one started instance", res)
	}

	installs, created, started, _ := manager.snapshot()
	if installs != 1 {
		t.Fatalf("registered installations = %d, want 1", installs)
	}
	if len(created) != 1 {
		t.Fatalf("created instances = %+v, want exactly the enabled instance", created)
	}
	if created[0].ID != "enabled" {
		t.Fatalf("created instance = %+v, want enabled", created[0])
	}
	if len(started) != 1 || started[0] != "tenant-a/enabled" {
		t.Fatalf("started = %v, want tenant-a/enabled only", started)
	}
}

func TestDisableStopsOnHostReload(t *testing.T) {
	root := t.TempDir()
	pluginsDir := filepath.Join(root, "plugins.d")
	lockPath := filepath.Join(root, "plugins.lock")
	store := plugincontrol.NewStore(filepath.Join(root, "state"))
	writeTestPlugin(t, pluginsDir, lockPath, "0.1.0", nil, nil)

	if err := store.Save(plugincontrol.InstanceState{
		Tenant: "tenant-a", InstanceID: "inst-1", PluginID: testPluginID,
		Version: "0.1.0", Enabled: false, Isolation: plugincontrol.IsolationShared,
	}); err != nil {
		t.Fatal(err)
	}

	manager := &fakeHostManager{}
	host, err := plugincontrol.NewHost(plugincontrol.HostOptions{
		Manager:    manager,
		Store:      store,
		PluginsDir: pluginsDir,
		LockPath:   lockPath,
	})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}

	res, err := host.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if res.Started != 0 {
		t.Fatalf("disabled instance was started: %+v", res)
	}
	_, _, started, _ := manager.snapshot()
	if len(started) != 0 {
		t.Fatalf("disabled instance started on reload: %v", started)
	}
}

func TestHostLoadIgnoresMissingState(t *testing.T) {
	root := t.TempDir()
	pluginsDir := filepath.Join(root, "plugins.d")
	lockPath := filepath.Join(root, "plugins.lock")
	store := plugincontrol.NewStore(filepath.Join(root, "state"))
	writeTestPlugin(t, pluginsDir, lockPath, "0.1.0", nil, nil)

	manager := &fakeHostManager{}
	host, err := plugincontrol.NewHost(plugincontrol.HostOptions{
		Manager:    manager,
		Store:      store,
		PluginsDir: pluginsDir,
		LockPath:   lockPath,
	})
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}

	res, err := host.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if res.Idle {
		t.Fatalf("Load result = %+v, want not-idle when an installation exists", res)
	}
	if res.Started != 0 {
		t.Fatalf("Load result = %+v, want zero started processes", res)
	}
	_, _, started, _ := manager.snapshot()
	if len(started) != 0 {
		t.Fatalf("no state should start no process: %v", started)
	}
}

func TestHostLoadTenantScopesInstances(t *testing.T) {
	root := t.TempDir()
	pluginsDir := filepath.Join(root, "plugins.d")
	lockPath := filepath.Join(root, "plugins.lock")
	store := plugincontrol.NewStore(filepath.Join(root, "state"))
	writeTestPlugin(t, pluginsDir, lockPath, "0.1.0", nil, nil)

	for _, tc := range []struct{ tenant, id string }{
		{"tenant-a", "i-a"},
		{"tenant-b", "i-b"},
	} {
		if err := store.Save(plugincontrol.InstanceState{
			Tenant: tc.tenant, InstanceID: tc.id, PluginID: testPluginID,
			Version: "0.1.0", Enabled: true, Isolation: plugincontrol.IsolationShared,
		}); err != nil {
			t.Fatal(err)
		}
	}

	manager := &fakeHostManager{}
	host, err := plugincontrol.NewHost(plugincontrol.HostOptions{
		Manager: manager, Store: store, PluginsDir: pluginsDir, LockPath: lockPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := host.LoadTenant(context.Background(), "tenant-a")
	if err != nil {
		t.Fatalf("LoadTenant: %v", err)
	}
	if res.Instances != 1 || res.Started != 1 {
		t.Fatalf("LoadTenant result = %+v, want exactly one tenant-a instance", res)
	}
	_, _, started, _ := manager.snapshot()
	if len(started) != 1 || started[0] != "tenant-a/i-a" {
		t.Fatalf("started = %v, want [tenant-a/i-a] only", started)
	}
}

func TestHostLoadTenantEmptyDefaultsToDefault(t *testing.T) {
	root := t.TempDir()
	pluginsDir := filepath.Join(root, "plugins.d")
	lockPath := filepath.Join(root, "plugins.lock")
	store := plugincontrol.NewStore(filepath.Join(root, "state"))
	writeTestPlugin(t, pluginsDir, lockPath, "0.1.0", nil, nil)

	if err := store.Save(plugincontrol.InstanceState{
		Tenant: "default", InstanceID: "i-default", PluginID: testPluginID,
		Version: "0.1.0", Enabled: true, Isolation: plugincontrol.IsolationShared,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(plugincontrol.InstanceState{
		Tenant: "tenant-a", InstanceID: "i-a", PluginID: testPluginID,
		Version: "0.1.0", Enabled: true, Isolation: plugincontrol.IsolationShared,
	}); err != nil {
		t.Fatal(err)
	}

	manager := &fakeHostManager{}
	host, err := plugincontrol.NewHost(plugincontrol.HostOptions{
		Manager: manager, Store: store, PluginsDir: pluginsDir, LockPath: lockPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.LoadTenant(context.Background(), ""); err != nil {
		t.Fatalf("LoadTenant(empty): %v", err)
	}
	_, _, started, _ := manager.snapshot()
	if len(started) != 1 || started[0] != "default/i-default" {
		t.Fatalf("空 tenant 应按 default，started = %v, want [default/i-default]", started)
	}
}
