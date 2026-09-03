package main

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/DeliciousBuding/cloud-path/internal/registry"
)

// cliTestManifest is a schema-valid Driver manifest for the local fake origin.
const cliTestManifest = `apiVersion: plugins.cloudpath.dev/v1alpha1
kind: Driver
id: io.github.example.driver
version: 0.1.0
protocol: 1
entrypoint: ./driver
compatibility:
  core: ">=0.1.0 <0.2.0"
permissions:
  network: [outbound]
`

// cliTrustServer serves manifest, latest release, asset and same-origin checksum
// for example/driver so CLI trust paths run end to end without real network.
func cliTrustServer(t *testing.T, asset []byte) *httptest.Server {
	t.Helper()
	var assetURL, checksumURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/example/driver/contents/plugin.yaml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"content":  base64.StdEncoding.EncodeToString([]byte(cliTestManifest)),
			"encoding": "base64",
		})
	})
	mux.HandleFunc("/repos/example/driver/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(registry.Release{
			TagName: "v0.1.0",
			Name:    "v0.1.0",
			Assets: []registry.ReleaseAsset{
				{Name: "driver.bin", URL: assetURL, Size: int64(len(asset))},
				{Name: "driver.bin.sha256", URL: checksumURL, Size: 0},
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

// cliTrustEnv points the CLI at a temp install root plus the local API origin and
// returns the lock path. Inherited credentials are cleared so a test can never
// send a real token to the fake server.
func cliTrustEnv(t *testing.T, srv *httptest.Server) (pluginsDir, lockPath string) {
	t.Helper()
	pluginsDir = t.TempDir()
	lockPath = filepath.Join(pluginsDir, "plugins.lock")
	schema := filepath.Join("..", "..", "spec", "plugin-manifest.schema.json")
	t.Setenv("CLOUDPATH_GITHUB_API", srv.URL)
	t.Setenv("CLOUDPATH_PLUGINS_DIR", pluginsDir)
	t.Setenv("CLOUDPATH_LOCK", lockPath)
	t.Setenv("CLOUDPATH_SCHEMA", schema)
	t.Setenv("CLOUDPATH_STATE_DIR", filepath.Join(pluginsDir, "state"))
	t.Setenv("CLOUDPATH_DATA_DIR", filepath.Join(pluginsDir, "data"))
	t.Setenv("CLOUDPATH_REGISTRY_INDEX", "")
	t.Setenv("CLOUDPATH_ALLOW_UNREVIEWED", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	return pluginsDir, lockPath
}

// cliCapture runs fn with stdout and stderr captured and returns the exit code
// plus both streams.
func cliCapture(t *testing.T, fn func() int) (int, string, string) {
	t.Helper()
	oldOut, oldErr := os.Stdout, os.Stderr
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	os.Stdout, os.Stderr = outW, errW
	var wg sync.WaitGroup
	var outBuf, errBuf strings.Builder
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = io.Copy(&outBuf, outR) }()
	go func() { defer wg.Done(); _, _ = io.Copy(&errBuf, errR) }()
	code := fn()
	os.Stdout, os.Stderr = oldOut, oldErr
	_ = outW.Close()
	_ = errW.Close()
	wg.Wait()
	_ = outR.Close()
	_ = errR.Close()
	return code, outBuf.String(), errBuf.String()
}

func TestEnvBoolOr(t *testing.T) {
	cases := []struct {
		raw  string
		def  bool
		want bool
	}{
		{"", false, false},
		{"", true, true},
		{"1", false, true},
		{"true", false, true},
		{"YES", false, true},
		{"on", false, true},
		{"0", true, false},
		{"false", true, false},
		{"off", true, false},
		{"garbage", true, true},
		{"  true  ", false, true},
	}
	for _, c := range cases {
		t.Setenv("CLOUDPATH_TEST_BOOL", c.raw)
		if got := envBoolOr("CLOUDPATH_TEST_BOOL", c.def); got != c.want {
			t.Fatalf("envBoolOr(%q, def=%v) = %v, want %v", c.raw, c.def, got, c.want)
		}
	}
}

// TestCLIInstallRequiresTrustEvidence proves the CLI default is deny: with no
// independent evidence and no explicit opt-in, install fails closed and the
// message names the real opt-in flag that now exists.
func TestCLIInstallRequiresTrustEvidence(t *testing.T) {
	srv := cliTrustServer(t, []byte("payload"))
	cliTrustEnv(t, srv)

	code, _, stderr := cliCapture(t, func() int {
		return runInstall([]string{"example/driver", "-yes"})
	})
	if code == 0 {
		t.Fatal("install without trust evidence must fail closed")
	}
	if !strings.Contains(stderr, "allow-unreviewed") {
		t.Fatalf("error must name the opt-in flag, got: %s", stderr)
	}
	if !strings.Contains(stderr, "ERR_") {
		t.Fatalf("error must carry a stable code, got: %s", stderr)
	}
}

// TestCLIInstallAllowUnreviewedTOFU proves the opt-in flag reaches the registry
// and that TOFU is reported as unverified, never as verified.
func TestCLIInstallAllowUnreviewedTOFU(t *testing.T) {
	asset := []byte("payload")
	srv := cliTrustServer(t, asset)
	pluginsDir, lockPath := cliTrustEnv(t, srv)

	code, stdout, stderr := cliCapture(t, func() int {
		return runInstall([]string{"example/driver", "-yes", "-allow-unreviewed"})
	})
	if code != 0 {
		t.Fatalf("install with -allow-unreviewed should succeed: code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, string(registry.TrustModeUnreviewedTOFU)) {
		t.Fatalf("stdout must report unreviewed-tofu, got: %s", stdout)
	}
	if !strings.Contains(stdout, "verified:    false") {
		t.Fatalf("TOFU must never be reported verified, got: %s", stdout)
	}
	lock, err := registry.LoadLockFile(lockPath)
	if err != nil {
		t.Fatalf("load lock: %v", err)
	}
	entry, ok := lock.Find("io.github.example.driver")
	if !ok {
		t.Fatal("lock entry missing")
	}
	if entry.Verified || entry.Mode != registry.TrustModeUnreviewedTOFU {
		t.Fatalf("persisted mode=%q verified=%v, want unreviewed-tofu false", entry.Mode, entry.Verified)
	}
	if entry.Digest != registry.SHA256Bytes(asset) {
		t.Fatalf("persisted digest %q != artifact digest", entry.Digest)
	}
	_ = pluginsDir
}

// TestCLIInstallRegistryIndexVerified proves the curated Registry index is
// actually reachable from the CLI (previously LoadRegistryIndex had no caller),
// yielding verified-registry trust with a verified publisher.
func TestCLIInstallRegistryIndexVerified(t *testing.T) {
	asset := []byte("payload")
	srv := cliTrustServer(t, asset)
	pluginsDir, _ := cliTrustEnv(t, srv)

	index := "version: 1\nplugins:\n" +
		"  - id: io.github.example.driver\n" +
		"    version: 0.1.0\n" +
		"    kind: Driver\n" +
		"    source: https://github.com/example/driver\n" +
		"    digest: " + registry.SHA256Bytes(asset) + "\n" +
		"    verifiedPublisher: example-publisher\n" +
		"    protocol: 1\n" +
		"    compatibility: \">=0.1.0 <0.2.0\"\n"
	indexPath := filepath.Join(pluginsDir, "registry-index.yaml")
	if err := os.WriteFile(indexPath, []byte(index), 0o600); err != nil {
		t.Fatalf("write index: %v", err)
	}

	code, stdout, stderr := cliCapture(t, func() int {
		return runInstall([]string{"example/driver", "-yes", "-registry-index", indexPath})
	})
	if code != 0 {
		t.Fatalf("verified registry install failed: code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, string(registry.TrustModeVerifiedRegistry)) {
		t.Fatalf("stdout must report verified-registry, got: %s", stdout)
	}
	if !strings.Contains(stdout, "verified:    true") {
		t.Fatalf("registry-bound install must be verified, got: %s", stdout)
	}
	if !strings.Contains(stdout, "example-publisher") {
		t.Fatalf("verified publisher must be surfaced, got: %s", stdout)
	}
}

// TestCLIRegistryIndexMalformedFailsClosed proves a corrupt curated index can
// never silently degrade to unverified metadata.
func TestCLIRegistryIndexMalformedFailsClosed(t *testing.T) {
	srv := cliTrustServer(t, []byte("payload"))
	pluginsDir, _ := cliTrustEnv(t, srv)

	indexPath := filepath.Join(pluginsDir, "bad-index.yaml")
	// Missing digest/publisher/compatibility: ValidateRegistryEntry must reject.
	bad := "version: 1\nplugins:\n  - id: io.github.example.driver\n    version: 0.1.0\n    kind: Driver\n    source: https://github.com/example/driver\n"
	if err := os.WriteFile(indexPath, []byte(bad), 0o600); err != nil {
		t.Fatalf("write index: %v", err)
	}

	code, _, stderr := cliCapture(t, func() int {
		return runInstall([]string{"example/driver", "-yes", "-registry-index", indexPath, "-allow-unreviewed"})
	})
	if code == 0 {
		t.Fatal("malformed registry index must fail closed, not fall back to TOFU")
	}
	if !strings.Contains(stderr, "registry") {
		t.Fatalf("error should name the registry index, got: %s", stderr)
	}
}

// TestCLIUpdateRejectsTrustDowngrade proves the update path now passes Existing,
// so validateUpdateTrust is reachable from the CLI and a verified installation
// cannot be silently downgraded to unreviewed TOFU.
func TestCLIUpdateRejectsTrustDowngrade(t *testing.T) {
	asset := []byte("payload")
	srv := cliTrustServer(t, asset)
	cliTrustEnv(t, srv)

	code, _, stderr := cliCapture(t, func() int {
		return runInstall([]string{"example/driver", "-yes", "-digest", registry.SHA256Bytes(asset)})
	})
	if code != 0 {
		t.Fatalf("seed verified install failed: code=%d stderr=%s", code, stderr)
	}

	code, _, stderr = cliCapture(t, func() int {
		return runUpdate([]string{"io.github.example.driver", "-yes", "-allow-unreviewed"})
	})
	if code == 0 {
		t.Fatal("update must refuse to downgrade a verified install to unreviewed TOFU")
	}
	if !strings.Contains(strings.ToLower(stderr), "downgrade") {
		t.Fatalf("expected trust downgrade refusal, got: %s", stderr)
	}
}

// TestCLIInspectReportsInstalledTrust proves inspect surfaces the recorded trust
// anchor for an installed plugin and reports "not installed" otherwise.
func TestCLIInspectReportsInstalledTrust(t *testing.T) {
	asset := []byte("payload")
	srv := cliTrustServer(t, asset)
	cliTrustEnv(t, srv)

	code, stdout, _ := cliCapture(t, func() int {
		return runInspect([]string{"example/driver"})
	})
	if code != 0 {
		t.Fatalf("inspect failed: code=%d", code)
	}
	if !strings.Contains(stdout, "installed:     no") {
		t.Fatalf("inspect should report not installed before any install, got: %s", stdout)
	}

	if c, _, stderr := cliCapture(t, func() int {
		return runInstall([]string{"example/driver", "-yes", "-digest", registry.SHA256Bytes(asset)})
	}); c != 0 {
		t.Fatalf("install failed: %s", stderr)
	}

	code, stdout, _ = cliCapture(t, func() int {
		return runInspect([]string{"example/driver"})
	})
	if code != 0 {
		t.Fatalf("inspect after install failed: code=%d", code)
	}
	if !strings.Contains(stdout, "installed:     yes") {
		t.Fatalf("inspect should report installed, got: %s", stdout)
	}
	if !strings.Contains(stdout, string(registry.TrustModeExplicitDigest)) {
		t.Fatalf("inspect should report recorded trust mode, got: %s", stdout)
	}
	if !strings.Contains(stdout, "verified:      true") {
		t.Fatalf("inspect should report verified state, got: %s", stdout)
	}
}
