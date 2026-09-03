package plugincontrol_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeliciousBuding/cloud-path/internal/plugincontrol"
	"github.com/DeliciousBuding/cloud-path/internal/pluginhost"
	"github.com/DeliciousBuding/cloud-path/internal/registry"
)

const testPluginID = "io.test.plugin"

func testSchemaPath() string {
	return filepath.Join("..", "..", "spec", "plugin-manifest.schema.json")
}

func testManifest(version string, hardware, network []string) string {
	return `apiVersion: plugins.cloudpath.dev/v1alpha1
kind: Driver
id: ` + testPluginID + `
version: ` + version + `
protocol: 1
entrypoint: ./plugin
compatibility:
  core: ">=0.1.0 <0.2.0"
permissions:
  hardware: [` + strings.Join(hardware, ", ") + `]
  network: [` + strings.Join(network, ", ") + `]
`
}

func newTestController(t *testing.T) (*plugincontrol.Controller, string, string, string) {
	t.Helper()
	root := t.TempDir()
	pluginsDir := filepath.Join(root, "plugins.d")
	lockPath := filepath.Join(root, "plugins.lock")
	stateDir := filepath.Join(root, "state")
	dataDir := filepath.Join(root, "plugin-data")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctrl, err := plugincontrol.NewController(plugincontrol.ControllerOptions{
		Store:       plugincontrol.NewStore(stateDir),
		PluginsDir:  pluginsDir,
		LockPath:    lockPath,
		SchemaPath:  testSchemaPath(),
		CoreVersion: "0.1.0",
		DataDir:     dataDir,
	})
	if err != nil {
		t.Fatalf("NewController: %v", err)
	}
	return ctrl, pluginsDir, lockPath, dataDir
}

func writeTestPlugin(t *testing.T, pluginsDir, lockPath, version string, hardware, network []string) {
	t.Helper()
	pluginDir := filepath.Join(pluginsDir, registry.SafePluginID(testPluginID))
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(testManifest(version, hardware, network))
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.yaml"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	lock := registry.NewLockFile()
	lock.Upsert(registry.LockedPlugin{
		ID:            testPluginID,
		Version:       version,
		Digest:        strings.Repeat("a", 64),
		Source:        "https://github.com/example/plugin",
		Verified:      true,
		Protocol:      1,
		Compatibility: ">=0.1.0 <0.2.0",
	})
	if err := registry.WriteLockFile(lockPath, lock); err != nil {
		t.Fatal(err)
	}
}

func TestEnablePersistsDesiredState(t *testing.T) {
	ctrl, pluginsDir, lockPath, _ := newTestController(t)
	writeTestPlugin(t, pluginsDir, lockPath, "0.1.0", []string{"serial"}, nil)

	res, err := ctrl.Enable(plugincontrol.EnableOptions{
		Tenant:     "tenant-a",
		InstanceID: "inst-1",
		PluginID:   testPluginID,
		Isolation:  pluginhost.IsolationShared,
	})
	if err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if !res.Desired.Enabled {
		t.Fatal("desired state is not enabled")
	}
	if res.Desired.Version != "0.1.0" {
		t.Fatalf("desired version = %q, want 0.1.0", res.Desired.Version)
	}
	if res.Desired.Isolation != plugincontrol.IsolationShared {
		t.Fatalf("desired isolation = %q", res.Desired.Isolation)
	}

	got, err := plugincontrol.NewStore(filepath.Join(filepath.Dir(lockPath), "state")).Load("tenant-a", "inst-1")
	if err != nil {
		t.Fatalf("reload persisted state: %v", err)
	}
	if !got.Enabled || got.PluginID != testPluginID || got.Version != "0.1.0" {
		t.Fatalf("persisted state mismatch: %+v", got)
	}
}

func TestDesiredObservedSeparation(t *testing.T) {
	ctrl, pluginsDir, lockPath, _ := newTestController(t)
	writeTestPlugin(t, pluginsDir, lockPath, "0.1.0", nil, nil)

	res, err := ctrl.Enable(plugincontrol.EnableOptions{
		Tenant:     "tenant-a",
		InstanceID: "inst-1",
		PluginID:   testPluginID,
	})
	if err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if !res.Desired.Enabled {
		t.Fatal("desired state must be enabled")
	}
	if res.Observed.HostOnline {
		t.Fatal("one-shot CLI must not report a live host")
	}
	if res.Observed.State == pluginhost.StateHealthy {
		t.Fatal("one-shot enable must not report HEALTHY")
	}
	if !strings.Contains(res.Observed.String(), "STOPPED") {
		t.Fatalf("observed string should be STOPPED, got %q", res.Observed.String())
	}
	if strings.Contains(res.Observed.String(), "HEALTHY") {
		t.Fatalf("observed string must not contain HEALTHY: %q", res.Observed.String())
	}
}

func TestTenantScopedInstanceState(t *testing.T) {
	store := plugincontrol.NewStore(filepath.Join(t.TempDir(), "state"))
	a := plugincontrol.InstanceState{
		Tenant: "tenant-a", InstanceID: "same", PluginID: "io.a", Version: "0.1.0",
		Enabled: true, Isolation: plugincontrol.IsolationShared,
	}
	b := plugincontrol.InstanceState{
		Tenant: "tenant-b", InstanceID: "same", PluginID: "io.b", Version: "0.2.0",
		Enabled: false, Isolation: plugincontrol.IsolationPerInstance,
	}
	if err := store.Save(a); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(b); err != nil {
		t.Fatal(err)
	}

	gotA, err := store.Load("tenant-a", "same")
	if err != nil {
		t.Fatal(err)
	}
	if gotA.PluginID != "io.a" || gotA.Version != "0.1.0" || !gotA.Enabled {
		t.Fatalf("tenant-a state leaked: %+v", gotA)
	}
	gotB, err := store.Load("tenant-b", "same")
	if err != nil {
		t.Fatal(err)
	}
	if gotB.PluginID != "io.b" || gotB.Version != "0.2.0" || gotB.Enabled {
		t.Fatalf("tenant-b state leaked: %+v", gotB)
	}

	listA, err := store.ListTenant("tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(listA) != 1 || listA[0].Tenant != "tenant-a" {
		t.Fatalf("ListTenant(tenant-a) = %+v", listA)
	}
	if _, err := store.Load("tenant-b", "missing"); !errors.Is(err, plugincontrol.ErrNotFound) {
		t.Fatalf("missing load error = %v, want ErrNotFound", err)
	}
}

func TestRemovePreservesData(t *testing.T) {
	ctrl, pluginsDir, lockPath, dataDir := newTestController(t)
	writeTestPlugin(t, pluginsDir, lockPath, "0.1.0", nil, nil)
	if _, err := ctrl.Enable(plugincontrol.EnableOptions{
		Tenant: "tenant-a", InstanceID: "inst-1", PluginID: testPluginID,
	}); err != nil {
		t.Fatalf("Enable: %v", err)
	}

	dataPath := filepath.Join(dataDir, "tenant-a", "inst-1")
	if err := os.MkdirAll(dataPath, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(dataPath, "keep.txt")
	if err := os.WriteFile(keep, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := ctrl.Remove(plugincontrol.RemoveOptions{Tenant: "tenant-a", InstanceID: "inst-1"})
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if res.Purged {
		t.Fatal("Purged = true, want false by default")
	}
	if !res.DataPreserved {
		t.Fatal("DataPreserved = false, want true by default")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("plugin data was not preserved: %v", err)
	}
	if _, err := plugincontrol.NewStore(filepath.Join(filepath.Dir(lockPath), "state")).Load("tenant-a", "inst-1"); !errors.Is(err, plugincontrol.ErrNotFound) {
		t.Fatalf("instance state still exists after Remove: %v", err)
	}
}

func TestPurgeRequiresExplicitFlag(t *testing.T) {
	ctrl, pluginsDir, lockPath, dataDir := newTestController(t)
	writeTestPlugin(t, pluginsDir, lockPath, "0.1.0", nil, nil)

	enable := func() {
		if _, err := ctrl.Enable(plugincontrol.EnableOptions{
			Tenant: "tenant-a", InstanceID: "inst-1", PluginID: testPluginID,
		}); err != nil {
			t.Fatalf("Enable: %v", err)
		}
	}
	dataPath := filepath.Join(dataDir, "tenant-a", "inst-1")
	keep := filepath.Join(dataPath, "keep.txt")

	// Default Remove must preserve data.
	enable()
	if err := os.MkdirAll(dataPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keep, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := ctrl.Remove(plugincontrol.RemoveOptions{Tenant: "tenant-a", InstanceID: "inst-1"})
	if err != nil {
		t.Fatalf("Remove(default): %v", err)
	}
	if res.Purged || !res.DataPreserved {
		t.Fatalf("default Remove result = %+v, want preserve", res)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("default Remove deleted plugin data: %v", err)
	}

	// Explicit purge must delete data.
	enable()
	if err := os.WriteFile(keep, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = ctrl.Remove(plugincontrol.RemoveOptions{Tenant: "tenant-a", InstanceID: "inst-1", Purge: true})
	if err != nil {
		t.Fatalf("Remove(purge): %v", err)
	}
	if !res.Purged || res.DataPreserved {
		t.Fatalf("purge Remove result = %+v, want purge", res)
	}
	if _, err := os.Stat(keep); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("purge did not delete plugin data: %v", err)
	}
}

func TestUpdateRejectsPermissionExpansion(t *testing.T) {
	ctrl, pluginsDir, lockPath, _ := newTestController(t)
	writeTestPlugin(t, pluginsDir, lockPath, "0.1.0", []string{"serial"}, nil)

	incoming := &registry.Manifest{
		APIVersion: "plugins.cloudpath.dev/v1alpha1",
		Kind:       "Driver",
		ID:         testPluginID,
		Version:    "0.2.0",
		Protocol:   1,
		Entrypoint: "./plugin",
		Compatibility: registry.Compatibility{
			Core: ">=0.1.0 <0.2.0",
		},
		Permissions: registry.Permissions{
			Hardware: []string{"serial", "usb"},
			Network:  []string{"outbound"},
		},
	}

	added, err := ctrl.UpdateCheck(testPluginID, incoming, false)
	if err == nil {
		t.Fatal("UpdateCheck accepted permission expansion without confirmation")
	}
	if !errors.Is(err, plugincontrol.ErrPermissionConfirmationRequired) {
		t.Fatalf("UpdateCheck error = %v, want ErrPermissionConfirmationRequired", err)
	}
	if len(added) == 0 {
		t.Fatal("UpdateCheck did not report added permissions")
	}

	// Confirming the same expansion removes the confirmation error.
	if _, err := ctrl.UpdateCheck(testPluginID, incoming, true); err != nil {
		t.Fatalf("UpdateCheck(confirmed) = %v, want nil", err)
	}
}
