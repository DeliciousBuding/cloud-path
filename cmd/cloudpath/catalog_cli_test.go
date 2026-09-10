package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/DeliciousBuding/cloud-path/internal/plugincontrol"
	"github.com/DeliciousBuding/cloud-path/internal/registry"
)

const cliCatalogYAML = `apiVersion: plugins.cloudpath.dev/v1alpha1
kind: PluginCatalog
plugins:
  - id: io.github.example.driver
    slug: example-driver
    kind: Driver
    path: drivers/example
    tagPrefix: drivers/example
    asset: driver.bin
    archived: false
  - id: io.github.example.retired
    slug: retired-driver
    kind: Driver
    path: drivers/retired
    tagPrefix: drivers/retired
    archived: true
`

func cliCatalogServer(t *testing.T, asset []byte) *httptest.Server {
	t.Helper()
	var assetURL, checksumURL string
	mux := http.NewServeMux()
	writeContents := func(w http.ResponseWriter, data []byte) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"content":  base64.StdEncoding.EncodeToString(data),
			"encoding": "base64",
		})
	}
	mux.HandleFunc("/repos/example/plugins/contents/plugins.yaml", func(w http.ResponseWriter, r *http.Request) {
		writeContents(w, []byte(cliCatalogYAML))
	})
	mux.HandleFunc("/repos/example/plugins/contents/drivers/example/plugin.yaml", func(w http.ResponseWriter, r *http.Request) {
		writeContents(w, []byte(cliTestManifest))
	})
	mux.HandleFunc("/repos/example/plugins/releases", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]registry.Release{{
			TagName: "drivers/example/v0.1.0",
			Name:    "drivers/example/v0.1.0",
			Assets: []registry.ReleaseAsset{
				{Name: "driver.bin", URL: assetURL, Size: int64(len(asset))},
				{Name: "driver.bin.sha256", URL: checksumURL},
			},
		}})
	})
	mux.HandleFunc("/repos/example/driver/contents/plugin.yaml", func(w http.ResponseWriter, r *http.Request) {
		writeContents(w, []byte(cliTestManifest))
	})
	mux.HandleFunc("/repos/example/driver/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(registry.Release{
			TagName: "v0.1.0",
			Name:    "v0.1.0",
			Assets: []registry.ReleaseAsset{
				{Name: "driver.bin", URL: assetURL, Size: int64(len(asset))},
				{Name: "driver.bin.sha256", URL: checksumURL},
			},
		})
	})
	mux.HandleFunc("/download", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(asset) })
	mux.HandleFunc("/checksum", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(registry.SHA256Bytes(asset) + "  driver.bin\n"))
	})
	srv := httptest.NewServer(mux)
	assetURL = srv.URL + "/download"
	checksumURL = srv.URL + "/checksum"
	t.Cleanup(srv.Close)
	return srv
}

func TestPluginHelpMentionsCatalogFlags(t *testing.T) {
	code, stdout, stderr := cliCapture(t, func() int { return runPlugin([]string{"--help"}) })
	if code != 0 {
		t.Fatalf("plugin help failed: code=%d stderr=%s", code, stderr)
	}
	for _, want := range []string{"--plugin", "--allow-source-change", "Monorepo"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("plugin help missing %q:\n%s", want, stdout)
		}
	}
}

func TestCLIInspectCatalogSelector(t *testing.T) {
	srv := cliCatalogServer(t, []byte("payload"))
	cliTrustEnv(t, srv)

	code, stdout, stderr := cliCapture(t, func() int {
		return runInspect([]string{"example/plugins", "-plugin", "example-driver"})
	})
	if code != 0 {
		t.Fatalf("catalog inspect failed: code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "io.github.example.driver") || !strings.Contains(stdout, "drivers/example/plugin.yaml") {
		t.Fatalf("catalog inspect output missing selector/version: %s", stdout)
	}

	code, _, stderr = cliCapture(t, func() int {
		return runInspect([]string{"example/plugins"})
	})
	if code == 0 || !strings.Contains(stderr, "--plugin") {
		t.Fatalf("catalog inspect without selector must fail with --plugin hint: code=%d stderr=%s", code, stderr)
	}

	code, _, stderr = cliCapture(t, func() int {
		return runInspect([]string{"example/plugins", "-plugin", "retired-driver"})
	})
	if code == 0 || !strings.Contains(stderr, "archived") {
		t.Fatalf("archived inspect should fail: code=%d stderr=%s", code, stderr)
	}
}

func TestCLIInstallCatalogSelectorRecordsCoordinates(t *testing.T) {
	asset := []byte("payload")
	srv := cliCatalogServer(t, asset)
	_, lockPath := cliTrustEnv(t, srv)

	code, stdout, stderr := cliCapture(t, func() int {
		return runInstall([]string{"example/plugins", "-plugin", "example-driver", "-yes", "-digest", registry.SHA256Bytes(asset)})
	})
	if code != 0 {
		t.Fatalf("catalog install failed: code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "drivers/example/v0.1.0") {
		t.Fatalf("install output missing release tag: %s", stdout)
	}
	lock, err := registry.LoadLockFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := lock.Find("io.github.example.driver")
	if !ok || entry.Tag != "drivers/example/v0.1.0" || entry.PluginPath != "drivers/example" {
		t.Fatalf("catalog install lock coordinates missing: %+v", entry)
	}
}

func TestCLIUpdateSourceMigrationRequiresExplicitFlag(t *testing.T) {
	asset := []byte("payload")
	srv := cliCatalogServer(t, asset)
	pluginsDir, lockPath := cliTrustEnv(t, srv)

	code, _, stderr := cliCapture(t, func() int {
		return runInstall([]string{"example/driver", "-yes", "-digest", registry.SHA256Bytes(asset)})
	})
	if code != 0 {
		t.Fatalf("legacy seed install failed: code=%d stderr=%s", code, stderr)
	}

	state := plugincontrol.InstanceState{
		Tenant:     defaultTenant,
		InstanceID: registry.SafePluginID("io.github.example.driver"),
		PluginID:   "io.github.example.driver",
		Version:    "0.1.0",
		Enabled:    true,
		Isolation:  plugincontrol.IsolationShared,
	}
	if err := plugincontrol.NewStore(os.Getenv("CLOUDPATH_STATE_DIR")).Save(state); err != nil {
		t.Fatalf("seed instance state: %v", err)
	}

	code, _, stderr = cliCapture(t, func() int {
		return runUpdate([]string{"io.github.example.driver", "-source", "example/plugins", "-digest", registry.SHA256Bytes(asset), "-yes"})
	})
	if code == 0 || !strings.Contains(stderr, "allow-source-change") {
		t.Fatalf("source migration without flag must fail closed: code=%d stderr=%s", code, stderr)
	}

	code, stdout, stderr := cliCapture(t, func() int {
		return runUpdate([]string{"io.github.example.driver", "-source", "example/plugins", "-digest", registry.SHA256Bytes(asset), "-yes", "-allow-source-change"})
	})
	if code != 0 {
		t.Fatalf("explicit source migration failed: code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "drivers/example/v0.1.0") {
		t.Fatalf("update output missing migrated tag: %s", stdout)
	}
	lock, err := registry.LoadLockFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := lock.Find("io.github.example.driver")
	if !ok || entry.Source != "https://github.com/example/plugins" || entry.PluginPath != "drivers/example" {
		t.Fatalf("migrated lock coordinates missing: %+v", entry)
	}
	_ = pluginsDir
}
