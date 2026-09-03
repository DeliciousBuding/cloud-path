package registry

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// trustServer is like installServer but the latest release also carries a
// .sha256 checksum asset, so the unreviewed trust-on-first-use path can resolve
// a same-origin digest.
func trustServer(t *testing.T, manifestData, assetData []byte, assetName string) (*httptest.Server, string) {
	t.Helper()
	var assetURL, checksumURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/example/driver/contents/plugin.yaml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"content":  base64.StdEncoding.EncodeToString(manifestData),
			"encoding": "base64",
		})
	})
	mux.HandleFunc("/repos/example/driver/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(Release{
			TagName: "v0.1.0",
			Assets: []ReleaseAsset{
				{Name: assetName, URL: assetURL, Size: int64(len(assetData))},
				{Name: assetName + ".sha256", URL: checksumURL, Size: 0},
			},
		})
	})
	mux.HandleFunc("/download", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(assetData) })
	mux.HandleFunc("/checksum", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(SHA256Bytes(assetData) + "  " + assetName + "\n"))
	})
	srv := httptest.NewServer(mux)
	assetURL = srv.URL + "/download"
	checksumURL = srv.URL + "/checksum"
	t.Cleanup(srv.Close)
	return srv, assetURL
}

func TestTrustModeVerified(t *testing.T) {
	verified := []TrustMode{TrustModeExplicitDigest, TrustModeVerifiedRegistry, TrustModeAttestation}
	for _, m := range verified {
		if !trustModeVerified(m) {
			t.Fatalf("mode %q should count as verified", m)
		}
	}
	if trustModeVerified(TrustModeUnreviewedTOFU) {
		t.Fatal("unreviewed TOFU must never count as verified")
	}
	if trustModeVerified("") {
		t.Fatal("empty mode must never count as verified")
	}
}

func TestInstallRequiresTrustEvidence(t *testing.T) {
	manifest := readFixture(t, "plugin.yaml")
	assetData := []byte("payload")
	srv, _ := installServer(t, manifest, assetData, "driver.bin")
	defer srv.Close()

	pluginsDir := t.TempDir()
	inst := newInstaller(srv, pluginsDir, filepath.Join(pluginsDir, "plugins.lock"))
	_, err := inst.Install(context.Background(), InstallOptions{
		Source:       "example/driver",
		ConfirmPerms: true,
	})
	if err == nil {
		t.Fatal("install with no independent evidence must fail closed")
	}
	if !errors.Is(err, ErrTrustConfirmationRequired) {
		t.Fatalf("want ErrTrustConfirmationRequired, got %v", err)
	}
}

func TestInstallExplicitDigest(t *testing.T) {
	manifest := readFixture(t, "plugin.yaml")
	assetData := []byte("payload")
	srv, _ := installServer(t, manifest, assetData, "driver.bin")
	defer srv.Close()

	pluginsDir := t.TempDir()
	inst := newInstaller(srv, pluginsDir, filepath.Join(pluginsDir, "plugins.lock"))
	res, err := inst.Install(context.Background(), InstallOptions{
		Source:       "example/driver",
		Digest:       SHA256Bytes(assetData),
		ConfirmPerms: true,
	})
	if err != nil {
		t.Fatalf("explicit digest install: %v", err)
	}
	if res.Mode != TrustModeExplicitDigest || !res.Verified {
		t.Fatalf("mode=%q verified=%v, want explicit-digest true", res.Mode, res.Verified)
	}
	locked, err := LoadLockFile(filepath.Join(pluginsDir, "plugins.lock"))
	if err != nil {
		t.Fatal(err)
	}
	got, ok := locked.Find(res.Manifest.ID)
	if !ok {
		t.Fatalf("lock entry missing for %s", res.Manifest.ID)
	}
	if got.Mode != TrustModeExplicitDigest || !got.Verified {
		t.Fatalf("lock mode=%q verified=%v, want explicit-digest true", got.Mode, got.Verified)
	}
}

func TestInstallExplicitDigestMismatch(t *testing.T) {
	manifest := readFixture(t, "plugin.yaml")
	assetData := []byte("payload")
	srv, _ := installServer(t, manifest, assetData, "driver.bin")
	defer srv.Close()

	pluginsDir := t.TempDir()
	inst := newInstaller(srv, pluginsDir, filepath.Join(pluginsDir, "plugins.lock"))
	_, err := inst.Install(context.Background(), InstallOptions{
		Source:       "example/driver",
		Digest:       "0000000000000000000000000000000000000000000000000000000000000000",
		ConfirmPerms: true,
	})
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("want ErrDigestMismatch, got %v", err)
	}
}

func TestInstallInvalidDigest(t *testing.T) {
	manifest := readFixture(t, "plugin.yaml")
	assetData := []byte("payload")
	srv, _ := installServer(t, manifest, assetData, "driver.bin")
	defer srv.Close()

	pluginsDir := t.TempDir()
	inst := newInstaller(srv, pluginsDir, filepath.Join(pluginsDir, "plugins.lock"))
	_, err := inst.Install(context.Background(), InstallOptions{
		Source:       "example/driver",
		Digest:       "not-a-digest",
		ConfirmPerms: true,
	})
	if !errors.Is(err, ErrInvalidDigest) {
		t.Fatalf("want ErrInvalidDigest, got %v", err)
	}
}

func TestInstallUnreviewedTOFU(t *testing.T) {
	manifest := readFixture(t, "plugin.yaml")
	assetData := []byte("payload")
	srv, _ := trustServer(t, manifest, assetData, "driver.bin")
	defer srv.Close()

	pluginsDir := t.TempDir()
	inst := newInstaller(srv, pluginsDir, filepath.Join(pluginsDir, "plugins.lock"))
	res, err := inst.Install(context.Background(), InstallOptions{
		Source:          "example/driver",
		ConfirmPerms:    true,
		AllowUnreviewed: true,
	})
	if err != nil {
		t.Fatalf("allowed unreviewed TOFU install: %v", err)
	}
	if res.Mode != TrustModeUnreviewedTOFU || res.Verified {
		t.Fatalf("mode=%q verified=%v, want unreviewed-tofu false", res.Mode, res.Verified)
	}
}

func TestInstallUnreviewedTOFURequiresAllow(t *testing.T) {
	manifest := readFixture(t, "plugin.yaml")
	assetData := []byte("payload")
	srv, _ := trustServer(t, manifest, assetData, "driver.bin")
	defer srv.Close()

	pluginsDir := t.TempDir()
	inst := newInstaller(srv, pluginsDir, filepath.Join(pluginsDir, "plugins.lock"))
	_, err := inst.Install(context.Background(), InstallOptions{
		Source:       "example/driver",
		ConfirmPerms: true,
	})
	if !errors.Is(err, ErrTrustConfirmationRequired) {
		t.Fatalf("same-origin checksum without --allow-unreviewed must be refused, got %v", err)
	}
}

func TestInstallVerifiedRegistry(t *testing.T) {
	manifest := readFixture(t, "plugin.yaml")
	assetData := []byte("payload")
	srv, _ := installServer(t, manifest, assetData, "driver.bin")
	defer srv.Close()

	pluginsDir := t.TempDir()
	inst := newInstaller(srv, pluginsDir, filepath.Join(pluginsDir, "plugins.lock"))
	inst.RegistryIndex = &RegistryIndex{Plugins: []RegistryEntry{{
		ID:                "io.github.example.driver",
		Version:           "0.1.0",
		Kind:              "Driver",
		Source:            "https://github.com/example/driver",
		Digest:            SHA256Bytes(assetData),
		VerifiedPublisher: "example",
		Protocol:          1,
		Compatibility:     ">=0.1.0 <0.2.0",
	}}}
	res, err := inst.Install(context.Background(), InstallOptions{
		Source:       "example/driver",
		ConfirmPerms: true,
	})
	if err != nil {
		t.Fatalf("verified registry install: %v", err)
	}
	if res.Mode != TrustModeVerifiedRegistry || !res.Verified {
		t.Fatalf("mode=%q verified=%v, want verified-registry true", res.Mode, res.Verified)
	}
	if res.LockEntry.VerifiedPublisher != "example" {
		t.Fatalf("verifiedPublisher=%q, want example", res.LockEntry.VerifiedPublisher)
	}
}

func TestInstallRegistryBindingMismatch(t *testing.T) {
	manifest := readFixture(t, "plugin.yaml")
	assetData := []byte("payload")
	srv, _ := installServer(t, manifest, assetData, "driver.bin")
	defer srv.Close()

	pluginsDir := t.TempDir()
	inst := newInstaller(srv, pluginsDir, filepath.Join(pluginsDir, "plugins.lock"))
	inst.RegistryIndex = &RegistryIndex{Plugins: []RegistryEntry{{
		ID:                "io.github.example.driver",
		Version:           "0.9.9", // does not match manifest version
		Kind:              "Driver",
		Source:            "https://github.com/example/driver",
		Digest:            SHA256Bytes(assetData),
		VerifiedPublisher: "example",
		Protocol:          1,
		Compatibility:     ">=0.1.0 <0.2.0",
	}}}
	_, err := inst.Install(context.Background(), InstallOptions{
		Source:       "example/driver",
		ConfirmPerms: true,
	})
	if !errors.Is(err, ErrRegistryBindingMismatch) {
		t.Fatalf("want ErrRegistryBindingMismatch, got %v", err)
	}
}

type fakeAttestationVerifier struct {
	att *Attestation
	err error
}

func (f *fakeAttestationVerifier) Verify(_ context.Context, _ AttestationSubject) (*Attestation, error) {
	return f.att, f.err
}

func TestInstallAttestation(t *testing.T) {
	manifest := readFixture(t, "plugin.yaml")
	assetData := []byte("payload")
	srv, _ := installServer(t, manifest, assetData, "driver.bin")
	defer srv.Close()

	pluginsDir := t.TempDir()
	inst := newInstaller(srv, pluginsDir, filepath.Join(pluginsDir, "plugins.lock"))
	inst.Attestation = &fakeAttestationVerifier{att: &Attestation{
		Digest:    SHA256Bytes(assetData),
		Predicate: "slsa",
		Evidence:  "build attestation (slsa)",
	}}
	res, err := inst.Install(context.Background(), InstallOptions{
		Source:       "example/driver",
		ConfirmPerms: true,
	})
	if err != nil {
		t.Fatalf("attestation install: %v", err)
	}
	if res.Mode != TrustModeAttestation || !res.Verified {
		t.Fatalf("mode=%q verified=%v, want attestation true", res.Mode, res.Verified)
	}
}

func TestInstallAttestationFailure(t *testing.T) {
	manifest := readFixture(t, "plugin.yaml")
	assetData := []byte("payload")
	srv, _ := installServer(t, manifest, assetData, "driver.bin")
	defer srv.Close()

	pluginsDir := t.TempDir()
	inst := newInstaller(srv, pluginsDir, filepath.Join(pluginsDir, "plugins.lock"))
	inst.Attestation = &fakeAttestationVerifier{err: errors.New("signature invalid")}
	_, err := inst.Install(context.Background(), InstallOptions{
		Source:       "example/driver",
		ConfirmPerms: true,
	})
	if !errors.Is(err, ErrAttestationFailed) {
		t.Fatalf("want ErrAttestationFailed, got %v", err)
	}
}

func TestInstallTrustDowngradeRejected(t *testing.T) {
	manifest := readFixture(t, "plugin.yaml")
	assetData := []byte("payload")
	srv, _ := trustServer(t, manifest, assetData, "driver.bin")
	defer srv.Close()

	pluginsDir := t.TempDir()
	inst := newInstaller(srv, pluginsDir, filepath.Join(pluginsDir, "plugins.lock"))
	existing := &LockedPlugin{
		ID:       "io.github.example.driver",
		Verified: true,
		Source:   "https://github.com/example/driver",
	}
	_, err := inst.Install(context.Background(), InstallOptions{
		Source:          "example/driver",
		ConfirmPerms:    true,
		AllowUnreviewed: true,
		Existing:        existing,
	})
	if !errors.Is(err, ErrTrustDowngrade) {
		t.Fatalf("want ErrTrustDowngrade, got %v", err)
	}
}

func TestValidateRegistryBinding(t *testing.T) {
	validEntry := &RegistryEntry{
		ID:                "io.github.example.driver",
		Version:           "0.1.0",
		Source:            "https://github.com/example/driver",
		Digest:            "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		VerifiedPublisher: "example",
	}
	manifest := &Manifest{ID: "io.github.example.driver", Version: "0.1.0"}

	if err := ValidateRegistryBinding(nil, manifest, "https://github.com/example/driver"); !errors.Is(err, ErrRegistryBindingMismatch) {
		t.Fatalf("nil entry should fail, got %v", err)
	}
	if err := ValidateRegistryBinding(validEntry, nil, "https://github.com/example/driver"); !errors.Is(err, ErrRegistryBindingMismatch) {
		t.Fatalf("nil manifest should fail, got %v", err)
	}
	badID := *validEntry
	badID.ID = "io.github.other.driver"
	if err := ValidateRegistryBinding(&badID, manifest, "https://github.com/example/driver"); !errors.Is(err, ErrRegistryBindingMismatch) {
		t.Fatalf("id mismatch should fail, got %v", err)
	}
	badSource := *validEntry
	badSource.Source = "https://github.com/evil/driver"
	if err := ValidateRegistryBinding(&badSource, manifest, "https://github.com/example/driver"); !errors.Is(err, ErrRegistryBindingMismatch) {
		t.Fatalf("source mismatch should fail, got %v", err)
	}
	badPublisher := *validEntry
	badPublisher.VerifiedPublisher = ""
	if err := ValidateRegistryBinding(&badPublisher, manifest, "https://github.com/example/driver"); !errors.Is(err, ErrRegistryBindingMismatch) {
		t.Fatalf("missing publisher should fail, got %v", err)
	}
	if err := ValidateRegistryBinding(validEntry, manifest, "https://github.com/example/driver"); err != nil {
		t.Fatalf("valid binding should pass, got %v", err)
	}
}

func TestLoadRegistryIndex(t *testing.T) {
	valid := []byte(`version: 1
plugins:
  - id: io.github.example.driver
    version: 0.1.0
    kind: Driver
    source: https://github.com/example/driver
    digest: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    verifiedPublisher: example
    protocol: 1
    compatibility: ">=0.1.0 <0.2.0"
`)
	path := filepath.Join(t.TempDir(), "registry.yaml")
	if err := os.WriteFile(path, valid, 0o600); err != nil {
		t.Fatal(err)
	}
	idx, err := LoadRegistryIndex(path)
	if err != nil {
		t.Fatalf("LoadRegistryIndex valid: %v", err)
	}
	if _, ok := idx.Find("io.github.example.driver"); !ok {
		t.Fatal("Find should return the loaded entry")
	}

	corrupt := []byte(`version: 1
plugins:
  - id: io.github.example.driver
    version: 0.1.0
    kind: Driver
    source: https://github.com/example/driver
    digest: not-a-digest
    verifiedPublisher: example
    protocol: 1
    compatibility: ">=0.1.0 <0.2.0"
`)
	if err := os.WriteFile(path, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRegistryIndex(path); err == nil {
		t.Fatal("corrupt registry index must fail closed")
	}
}
