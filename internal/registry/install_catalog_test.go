package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const catalogInstallYAML = `apiVersion: plugins.cloudpath.dev/v1alpha1
kind: PluginCatalog
plugins:
  - id: io.github.example.driver
    slug: example-driver
    kind: Driver
    path: drivers/example
    tagPrefix: drivers/example
    asset: driver
    archived: false
  - id: io.github.example.retired
    slug: retired-driver
    kind: Driver
    path: drivers/retired
    tagPrefix: drivers/retired
    archived: true
`

func catalogInstallServer(t *testing.T, tag string) (*httptest.Server, []byte) {
	t.Helper()
	return catalogInstallServerWithManifest(t, tag, readFixture(t, "plugin.yaml"))
}

func catalogInstallServerWithManifest(t *testing.T, tag string, manifest []byte) (*httptest.Server, []byte) {
	t.Helper()
	assetData := []byte("catalog-payload")
	currentAsset := fmt.Sprintf("driver_0.1.0_%s_%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		currentAsset += ".exe"
	}
	var assetURL, checksumURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/example/plugins/contents/plugins.yaml", func(w http.ResponseWriter, r *http.Request) {
		writeContentsJSON(t, w, []byte(catalogInstallYAML))
	})
	mux.HandleFunc("/repos/example/plugins/contents/drivers/example/plugin.yaml", func(w http.ResponseWriter, r *http.Request) {
		writeContentsJSON(t, w, manifest)
	})
	mux.HandleFunc("/repos/example/plugins/releases", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]Release{
			{TagName: "other/v9.9.9", Name: "other"},
			{TagName: tag, Name: tag, Assets: []ReleaseAsset{
				{Name: currentAsset, URL: assetURL, Size: int64(len(assetData))},
				{Name: "driver_0.1.0_plan9_mips", URL: assetURL, Size: int64(len(assetData))},
				{Name: "checksums.txt", URL: checksumURL},
			}},
		})
	})
	mux.HandleFunc("/download", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(assetData) })
	mux.HandleFunc("/checksum", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(SHA256Bytes(assetData) + "  " + currentAsset + "\n"))
	})
	srv := httptest.NewServer(mux)
	assetURL = srv.URL + "/download"
	checksumURL = srv.URL + "/checksum"
	t.Cleanup(srv.Close)
	return srv, assetData
}

func TestInstallCatalogUsesPrefixReleaseAndRecordsCoordinates(t *testing.T) {
	srv, assetData := catalogInstallServer(t, "drivers/example/v0.1.0")
	pluginsDir := t.TempDir()
	inst := newInstaller(srv, pluginsDir, filepath.Join(pluginsDir, "plugins.lock"))
	res, err := inst.Install(context.Background(), InstallOptions{
		Source:       "example/plugins",
		Plugin:       "example-driver",
		Digest:       SHA256Bytes(assetData),
		ConfirmPerms: true,
	})
	if err != nil {
		t.Fatalf("catalog install: %v", err)
	}
	if res.LockEntry.Tag != "drivers/example/v0.1.0" || res.LockEntry.PluginPath != "drivers/example" {
		t.Fatalf("lock coordinates = tag %q path %q", res.LockEntry.Tag, res.LockEntry.PluginPath)
	}
	wantSuffix := SHA256Bytes(assetData)
	if runtime.GOOS == "windows" {
		wantSuffix += ".exe"
	}
	if !strings.HasSuffix(res.AssetPath, wantSuffix) {
		t.Fatalf("catalog asset for %s/%s was not selected: %s", runtime.GOOS, runtime.GOARCH, res.AssetPath)
	}
	lock, err := LoadLockFile(filepath.Join(pluginsDir, "plugins.lock"))
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := lock.Find("io.github.example.driver")
	if !ok || entry.Tag != "drivers/example/v0.1.0" || entry.PluginPath != "drivers/example" {
		t.Fatalf("persisted lock coordinates missing: %+v", entry)
	}
}

func TestInstallCatalogFailsClosedOnTagVersionMismatch(t *testing.T) {
	srv, assetData := catalogInstallServer(t, "drivers/example/v0.2.0")
	pluginsDir := t.TempDir()
	inst := newInstaller(srv, pluginsDir, filepath.Join(pluginsDir, "plugins.lock"))
	_, err := inst.Install(context.Background(), InstallOptions{
		Source:       "example/plugins",
		Plugin:       "example-driver",
		Digest:       SHA256Bytes(assetData),
		ConfirmPerms: true,
	})
	if err == nil || !strings.Contains(err.Error(), "want") {
		t.Fatalf("tag/version mismatch must fail closed, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(pluginsDir, "plugins.lock")); !os.IsNotExist(statErr) {
		t.Fatalf("mismatch must not write a lock file, stat err=%v", statErr)
	}
}

func TestInstallCatalogRequiresActiveSelector(t *testing.T) {
	srv, assetData := catalogInstallServer(t, "drivers/example/v0.1.0")
	pluginsDir := t.TempDir()
	inst := newInstaller(srv, pluginsDir, filepath.Join(pluginsDir, "plugins.lock"))
	base := InstallOptions{Source: "example/plugins", Digest: SHA256Bytes(assetData), ConfirmPerms: true}

	if _, err := inst.Install(context.Background(), base); err == nil || !strings.Contains(err.Error(), "--plugin") {
		t.Fatalf("missing selector must fail and name --plugin, got %v", err)
	}
	base.Plugin = "retired-driver"
	if _, err := inst.Install(context.Background(), base); err == nil || !strings.Contains(err.Error(), "archived") {
		t.Fatalf("archived selector must fail, got %v", err)
	}
	base.Plugin = "Example-Driver"
	if _, err := inst.Install(context.Background(), base); err == nil {
		t.Fatal("selector matching must be case-sensitive")
	}
}

func TestInstallLegacySinglePluginStillWorks(t *testing.T) {
	manifest := readFixture(t, "plugin.yaml")
	assetData := []byte("legacy-payload")
	srv, _ := installServer(t, manifest, assetData, "driver.bin")
	pluginsDir := t.TempDir()
	inst := newInstaller(srv, pluginsDir, filepath.Join(pluginsDir, "plugins.lock"))
	res, err := inst.Install(context.Background(), InstallOptions{
		Source:       "example/driver",
		Digest:       SHA256Bytes(assetData),
		ConfirmPerms: true,
	})
	if err != nil {
		t.Fatalf("legacy install: %v", err)
	}
	if res.LockEntry.Tag != "v0.1.0" || res.LockEntry.PluginPath != "" {
		t.Fatalf("legacy lock tag/path = %q/%q", res.LockEntry.Tag, res.LockEntry.PluginPath)
	}
}

func TestRegistryBindingChecksOptionalTagAndPath(t *testing.T) {
	manifest := &Manifest{ID: "io.github.example.driver", Version: "0.1.0"}
	entry := &RegistryEntry{
		ID:                manifest.ID,
		Version:           manifest.Version,
		Source:            "https://github.com/example/plugins",
		Digest:            strings.Repeat("a", 64),
		VerifiedPublisher: "example",
		Tag:               "drivers/example/v0.1.0",
		PluginPath:        "drivers/example",
	}
	if err := ValidateRegistryBinding(entry, manifest, entry.Source, RegistryBindingContext{Tag: entry.Tag, PluginPath: entry.PluginPath}); err != nil {
		t.Fatalf("matching optional binding: %v", err)
	}
	for _, context := range []RegistryBindingContext{
		{Tag: "drivers/example/v0.2.0", PluginPath: entry.PluginPath},
		{Tag: entry.Tag, PluginPath: "drivers/other"},
		{},
	} {
		if err := ValidateRegistryBinding(entry, manifest, entry.Source, context); err == nil {
			t.Fatalf("optional binding mismatch must fail: %+v", context)
		}
	}
}

func TestUpdateSourceMigrationGate(t *testing.T) {
	existing := LockedPlugin{ID: "io.github.example.driver", Source: "https://github.com/example/driver", Verified: true}
	repo := Repo{Owner: "example", Name: "plugins", URL: "https://github.com/example/plugins"}
	verifiedPlan := trustPlan{mode: TrustModeExplicitDigest, verified: true}
	if err := validateUpdateTrust(existing, repo, verifiedPlan, false); err == nil {
		t.Fatal("source migration without --allow-source-change must fail")
	}
	if err := validateUpdateTrust(existing, repo, verifiedPlan, true); err != nil {
		t.Fatalf("explicit source migration should pass: %v", err)
	}
	tofu := trustPlan{mode: TrustModeUnreviewedTOFU, verified: false}
	if err := validateUpdateTrust(existing, repo, tofu, true); err == nil {
		t.Fatal("--allow-source-change must never downgrade verified to unreviewed TOFU")
	}
}
func TestCatalogSelectorRejectsManifestIdentityMismatch(t *testing.T) {
	manifest := readFixture(t, "plugin.yaml")
	mismatched := strings.Replace(string(manifest), "id: io.github.example.driver", "id: io.github.example.other", 1)
	if mismatched == string(manifest) {
		t.Fatal("fixture id replacement did not apply")
	}
	srv, assetData := catalogInstallServerWithManifest(t, "drivers/example/v0.1.0", []byte(mismatched))
	pluginsDir := t.TempDir()
	inst := newInstaller(srv, pluginsDir, filepath.Join(pluginsDir, "plugins.lock"))
	_, err := inst.Install(context.Background(), InstallOptions{
		Source:       "example/plugins",
		Plugin:       "example-driver",
		Digest:       SHA256Bytes(assetData),
		ConfirmPerms: true,
	})
	if !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("catalog selector must bind manifest identity, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(pluginsDir, "plugins.lock")); !os.IsNotExist(statErr) {
		t.Fatalf("identity mismatch must not write a lock, stat err=%v", statErr)
	}
}

func TestInstallRejectsExistingTargetManifestMismatch(t *testing.T) {
	srv, assetData := catalogInstallServer(t, "drivers/example/v0.1.0")
	pluginsDir := t.TempDir()
	lockPath := filepath.Join(pluginsDir, "plugins.lock")
	inst := newInstaller(srv, pluginsDir, lockPath)
	_, err := inst.Install(context.Background(), InstallOptions{
		Source:       "example/plugins",
		Plugin:       "example-driver",
		Digest:       SHA256Bytes(assetData),
		ConfirmPerms: true,
		Existing:     &LockedPlugin{ID: "io.github.example.other"},
	})
	if !errors.Is(err, ErrTrustDowngrade) {
		t.Fatalf("existing target/manifest mismatch must fail closed, got %v", err)
	}
	if _, statErr := os.Stat(lockPath); !os.IsNotExist(statErr) {
		t.Fatalf("mismatch must not write a lock, stat err=%v", statErr)
	}
}

func TestInstallAutoLoadsExistingLockAndBlocksTrustDowngrade(t *testing.T) {
	srv, assetData := catalogInstallServer(t, "drivers/example/v0.1.0")
	pluginsDir := t.TempDir()
	lockPath := filepath.Join(pluginsDir, "plugins.lock")
	inst := newInstaller(srv, pluginsDir, lockPath)
	if _, err := inst.Install(context.Background(), InstallOptions{
		Source:       "example/plugins",
		Plugin:       "example-driver",
		Digest:       SHA256Bytes(assetData),
		ConfirmPerms: true,
	}); err != nil {
		t.Fatalf("seed install: %v", err)
	}
	lock, err := LoadLockFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := lock.Find("io.github.example.driver")
	if !ok {
		t.Fatal("seed lock entry missing")
	}
	entry.Source = "https://github.com/example/legacy"
	lock.Upsert(*entry)
	if err := WriteLockFile(lockPath, lock); err != nil {
		t.Fatal(err)
	}
	if _, err := inst.Install(context.Background(), InstallOptions{
		Source:          "example/plugins",
		Plugin:          "example-driver",
		ConfirmPerms:    true,
		AllowUnreviewed: true,
	}); !errors.Is(err, ErrTrustDowngrade) {
		t.Fatalf("install must auto-load the existing lock and reject downgrade, got %v", err)
	}
	after, err := LoadLockFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	afterEntry, ok := after.Find("io.github.example.driver")
	if !ok || afterEntry.Source != "https://github.com/example/legacy" || !afterEntry.Verified {
		t.Fatalf("failed reinstall changed trust state: %+v", afterEntry)
	}
}

func TestCatalogAssetSelectionIsPlatformExact(t *testing.T) {
	release := &Release{Assets: []ReleaseAsset{
		{Name: "driver_0.1.0_windows_amd64.exe"},
		{Name: "driver_0.1.0_windows_arm64.exe"},
	}}
	arm, err := selectCatalogInstallAsset(release, "driver", "0.1.0", "windows", "arm64")
	if err != nil {
		t.Fatalf("arm64 catalog asset: %v", err)
	}
	if arm.Name != "driver_0.1.0_windows_arm64.exe" {
		t.Fatalf("arm64 selected %q", arm.Name)
	}
	amd, err := selectCatalogInstallAsset(release, "driver", "0.1.0", "windows", "amd64")
	if err != nil {
		t.Fatalf("amd64 catalog asset: %v", err)
	}
	if amd.Name != "driver_0.1.0_windows_amd64.exe" {
		t.Fatalf("amd64 selected %q", amd.Name)
	}
	if _, err := selectCatalogInstallAsset(release, "driver", "0.1.0", "darwin", "arm64"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing platform must fail closed, got %v", err)
	}
	generic, err := selectInstallAssetForPlatform(release, "", "windows", "arm64")
	if err != nil {
		t.Fatalf("generic arm64 selection: %v", err)
	}
	if generic.Name != "driver_0.1.0_windows_arm64.exe" {
		t.Fatalf("generic selection ignored architecture: %q", generic.Name)
	}
}
