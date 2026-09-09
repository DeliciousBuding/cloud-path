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

func TestValidateInstanceHost(t *testing.T) {
	cases := []struct {
		name         string
		kind         pluginhost.Kind
		serverHosted bool
		want         error
	}{
		{name: "driver edge", kind: pluginhost.KindDriver},
		{name: "application server", kind: pluginhost.KindApplication, serverHosted: true},
		{name: "driver server", kind: pluginhost.KindDriver, serverHosted: true, want: plugincontrol.ErrInstanceHostMismatch},
		{name: "application edge", kind: pluginhost.KindApplication, want: plugincontrol.ErrInstanceHostMismatch},
		{name: "connector edge", kind: pluginhost.KindConnector, want: pluginhost.ErrConnectorUnsupported},
		{name: "connector server", kind: pluginhost.KindConnector, serverHosted: true, want: pluginhost.ErrConnectorUnsupported},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := plugincontrol.ValidateInstanceHost(tc.kind, tc.serverHosted)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("ValidateInstanceHost = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("ValidateInstanceHost = %v, want errors.Is(%v)", err, tc.want)
			}
		})
	}
}

func TestInstalledPluginKind(t *testing.T) {
	root := t.TempDir()
	pluginsDir := filepath.Join(root, "plugins.d")
	lockPath := filepath.Join(root, "plugins.lock")
	pluginDir := filepath.Join(pluginsDir, registry.SafePluginID(testPluginID))
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := []byte("apiVersion: plugins.cloudpath.dev/v1alpha1\n" +
		"kind: Application\n" +
		"id: " + testPluginID + "\n" +
		"version: 0.1.0\n" +
		"protocol: 1\n" +
		"entrypoint: ./plugin\n")
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.yaml"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	lock := registry.NewLockFile()
	lock.Upsert(registry.LockedPlugin{ID: testPluginID, Version: "0.1.0", Digest: strings.Repeat("a", 64), Protocol: 1})
	if err := registry.WriteLockFile(lockPath, lock); err != nil {
		t.Fatal(err)
	}
	kind, err := plugincontrol.InstalledPluginKind(pluginsDir, lockPath, testPluginID)
	if err != nil || kind != pluginhost.KindApplication {
		t.Fatalf("InstalledPluginKind = %v, %v", kind, err)
	}
	if _, err := plugincontrol.InstalledPluginKind(pluginsDir, lockPath, "io.test.missing"); !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("missing plugin error = %v, want registry.ErrNotFound", err)
	}
}
