package edgedriverhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeliciousBuding/cloud-path/internal/registry"
)

func TestRejectBuiltinExternalDriverConflict(t *testing.T) {
	err := CheckConflicts([]string{"stcb", "gpio"}, []string{"stcb", "modbus"})
	if err == nil {
		t.Fatal("期望冲突被拒绝")
	}
	if !strings.Contains(err.Error(), "stcb") {
		t.Fatalf("冲突信息应包含 stcb: %v", err)
	}
	if err := CheckConflicts([]string{"stcb"}, []string{"modbus"}); err != nil {
		t.Fatalf("无冲突不应报错: %v", err)
	}
	if err := CheckConflicts(nil, nil); err != nil {
		t.Fatalf("空输入不应报错: %v", err)
	}
}

func TestCheckConflictsIgnoresEmptyExternalID(t *testing.T) {
	if err := CheckConflicts([]string{"stcb"}, []string{"", "  "}); err != nil {
		t.Fatalf("空 driver ID 不应触发冲突: %v", err)
	}
}

func writeManifest(t *testing.T, root, pluginID, body string) {
	t.Helper()
	dir := filepath.Join(root, registry.SafePluginID(pluginID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDriverIDsFromLockfileManifests(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, "io.github.example.driver-stcb", "contributes:\n  drivers:\n    - id: stcb\n")
	writeManifest(t, root, "io.github.example.driver-modbus", "contributes:\n  drivers:\n    - id: modbus\n    - id: modbus-tcp\n")
	writeManifest(t, root, "io.github.example.app-alarm", "kind: Application\n")

	lock := registry.NewLockFile()
	lock.Plugins = []registry.LockedPlugin{
		{ID: "io.github.example.driver-stcb", Version: "0.1.0", Digest: "d1"},
		{ID: "io.github.example.driver-modbus", Version: "0.1.0", Digest: "d2"},
		{ID: "io.github.example.app-alarm", Version: "0.1.0", Digest: "d3"},
	}
	lockPath := filepath.Join(root, "plugins.lock")
	if err := registry.WriteLockFile(lockPath, lock); err != nil {
		t.Fatal(err)
	}

	ids, err := DriverIDs(root, lockPath)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"modbus", "modbus-tcp", "stcb"}
	if len(ids) != len(want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("ids[%d] = %q, want %q", i, ids[i], want[i])
		}
	}
}

func TestDriverIDsMissingManifestFailsClosed(t *testing.T) {
	root := t.TempDir()
	lock := registry.NewLockFile()
	lock.Plugins = []registry.LockedPlugin{
		{ID: "io.github.example.missing", Version: "0.1.0", Digest: "d1"},
	}
	lockPath := filepath.Join(root, "plugins.lock")
	if err := registry.WriteLockFile(lockPath, lock); err != nil {
		t.Fatal(err)
	}
	if _, err := DriverIDs(root, lockPath); err == nil {
		t.Fatal("缺失 manifest 应 fail-closed")
	}
}

func TestDriverIDsEmptyLockfile(t *testing.T) {
	root := t.TempDir()
	lockPath := filepath.Join(root, "plugins.lock")
	if err := registry.WriteLockFile(lockPath, registry.NewLockFile()); err != nil {
		t.Fatal(err)
	}
	ids, err := DriverIDs(root, lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatalf("空 lockfile 应无 driver ID，got %v", ids)
	}
}

func TestEdgeUsesRegistryContributions(t *testing.T) {
	root := t.TempDir()
	driverManifest := `apiVersion: plugins.cloudpath.dev/v1alpha1
kind: Driver
id: io.github.example.driver-stcb
version: 0.1.0
protocol: 1
entrypoint: ./driver
compatibility:
  core: ">=0.1.0 <0.2.0"
contributes:
  drivers:
    - id: stcb
    - id: modbus
`
	writeManifest(t, root, "io.github.example.driver-stcb", driverManifest)

	lock := registry.NewLockFile()
	lock.Plugins = []registry.LockedPlugin{
		{ID: "io.github.example.driver-stcb", Version: "0.1.0", Digest: "d1"},
	}
	lockPath := filepath.Join(root, "plugins.lock")
	if err := registry.WriteLockFile(lockPath, lock); err != nil {
		t.Fatal(err)
	}

	ids, err := DriverIDs(root, lockPath)
	if err != nil {
		t.Fatalf("DriverIDs: %v", err)
	}
	want := []string{"modbus", "stcb"}
	if len(ids) != len(want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("ids[%d] = %q, want %q", i, ids[i], want[i])
		}
	}

	// Conflict detection consumes the registry-derived driver ids.
	if err := CheckConflicts([]string{"stcb"}, ids); err == nil {
		t.Fatal("expected a builtin conflict with registry-derived id stcb")
	}
}
