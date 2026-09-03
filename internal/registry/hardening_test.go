package registry

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
)

// TestInstallWindowsAssetKeepsExeSuffix 锁定跨平台可执行性：Windows 发布工件以
// .exe 结尾，按摘要名存放时必须保留后缀（Windows exec 无法解析无扩展名文件，
// 2026-09-04 真实 install E2E 查出）；非 .exe 工件仍存裸摘要名（其他测试锁定）。
func TestInstallWindowsAssetKeepsExeSuffix(t *testing.T) {
	manifest := readFixture(t, "plugin.yaml")
	assetData := []byte("payload")
	srv, _ := installServer(t, manifest, assetData, "driver_windows_amd64.exe")
	defer srv.Close()

	pluginsDir := t.TempDir()
	inst := newInstaller(srv, pluginsDir, filepath.Join(pluginsDir, "plugins.lock"))
	res, err := inst.Install(context.Background(), InstallOptions{
		Source:       "example/driver",
		Digest:       SHA256Bytes(assetData),
		ConfirmPerms: true,
	})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	digest := SHA256Bytes(assetData)
	pluginDir := filepath.Join(pluginsDir, SafePluginID("io.github.example.driver"))
	want := filepath.Join(pluginDir, "assets", digest+".exe")
	if res.AssetPath != want {
		t.Fatalf("AssetPath = %s, want %s", res.AssetPath, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf(".exe asset should exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(pluginDir, "assets", digest)); err == nil {
		t.Fatal("bare digest asset must not exist alongside the .exe variant")
	}
}

// TestInstallAssetExecutableOnUnix 锁定 unix 执行位：CreateTemp 的 0600 工件
// 若不补 +x，host 在 Linux/macOS 上 exec 会 EACCES（与 Windows .exe 同类缺陷）。
func TestInstallAssetExecutableOnUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix mode bits are not meaningful on Windows")
	}
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
		t.Fatalf("install: %v", err)
	}
	fi, err := os.Stat(res.AssetPath)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o111 == 0 {
		t.Fatalf("asset mode %v lacks the executable bit", fi.Mode())
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

func testSchemaBytes(t *testing.T) []byte {
	t.Helper()
	return testSchema(t)
}

// gwClient builds a GitHubClient pointing at an httptest server.
func ghClient(server *httptest.Server) *GitHubClient {
	return &GitHubClient{
		HTTPClient:   server.Client(),
		BaseURL:      server.URL,
		MaxJSONBytes: 1 << 20,
		MaxRetries:   0,
	}
}

// installServer serves the manifest, latest release and a downloadable asset.
func installServer(t *testing.T, manifestData, assetData []byte, assetName string) (*httptest.Server, string) {
	t.Helper()
	var assetURL string
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
			Name:    "v0.1.0",
			Assets: []ReleaseAsset{{
				Name: assetName,
				URL:  assetURL,
				Size: int64(len(assetData)),
			}},
		})
	})
	mux.HandleFunc("/download", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(assetData)
	})
	srv := httptest.NewServer(mux)
	assetURL = srv.URL + "/download"
	t.Cleanup(srv.Close)
	return srv, assetURL
}

func newInstaller(server *httptest.Server, pluginsDir, lockPath string) *Installer {
	inst := NewInstaller(pluginsDir, lockPath, filepath.Join("..", "..", "spec", "plugin-manifest.schema.json"), "0.1.0")
	inst.Client = ghClient(server)
	inst.MaxDownloadBytes = 1 << 20
	return inst
}

func TestGitHubSearchPagination(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search/repositories", func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		switch page {
		case "", "1":
			w.Header().Set("Link", fmt.Sprintf(`<http://%s/search/repositories?page=2&q=topic%%3Acloudpath-plugin&per_page=20>; rel="next"`, r.Host))
			_, _ = fmt.Fprint(w, `{"total_count":3,"items":[
				{"full_name":"a/b","description":"one"},
				{"full_name":"c/d","description":"two"}
			]}`)
		case "2":
			_, _ = fmt.Fprint(w, `{"total_count":3,"items":[
				{"full_name":"e/f","description":"three"}
			]}`)
		default:
			http.Error(w, "unexpected page "+page, http.StatusBadRequest)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	results, err := ghClient(srv).Search(context.Background(), "driver")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("want 3 results across pages, got %d", len(results))
	}
	names := map[string]bool{}
	for _, r := range results {
		names[r.Name] = true
		if r.Description == "" {
			t.Fatalf("description should be trimmed: %+v", r)
		}
	}
	for _, want := range []string{"a/b", "c/d", "e/f"} {
		if !names[want] {
			t.Fatalf("missing paginated result %q", want)
		}
	}
}

func TestGitHubRateLimit(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search/repositories", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprint(w, `{"message":"API rate limit exceeded"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := ghClient(srv).Search(context.Background(), "driver")
	if err == nil {
		t.Fatal("expected rate-limit error")
	}
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("want ErrRateLimited, got %v", err)
	}
}

func TestDownloadSizeLimit(t *testing.T) {
	big := make([]byte, 512)
	mux := http.NewServeMux()
	mux.HandleFunc("/asset", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(big)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	client := ghClient(srv)

	if _, err := client.Download(context.Background(), srv.URL+"/asset", 64); err == nil {
		t.Fatal("Download should reject an asset larger than the limit")
	} else if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected size-limit message, got %v", err)
	}

	dest := filepath.Join(t.TempDir(), "asset.bin")
	if _, err := client.DownloadToFile(context.Background(), srv.URL+"/asset", dest, 64); err == nil {
		t.Fatal("DownloadToFile should reject an asset larger than the limit")
	}
	if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("no half file should remain on size-limit failure, got err=%v", err)
	}
}

func TestInstallDigestMismatchLeavesNoFile(t *testing.T) {
	manifest := readFixture(t, "plugin.yaml")
	assetData := []byte("payload")
	srv, _ := installServer(t, manifest, assetData, "driver.bin")
	defer srv.Close()

	pluginsDir := t.TempDir()
	lockPath := filepath.Join(pluginsDir, "plugins.lock")
	inst := newInstaller(srv, pluginsDir, lockPath)

	_, err := inst.Install(context.Background(), InstallOptions{
		Source:       "example/driver",
		Digest:       strings.Repeat("0", 64),
		ConfirmPerms: true,
	})
	if err == nil {
		t.Fatal("expected digest mismatch")
	}
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("want ErrDigestMismatch, got %v", err)
	}
	entries, readErr := os.ReadDir(pluginsDir)
	if readErr != nil {
		t.Fatalf("read plugins dir: %v", readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("plugins dir should be empty after digest mismatch, got %d entries", len(entries))
	}
}

func TestInstallDigestMismatchLeavesNoAssetHalfFile(t *testing.T) {
	manifest := readFixture(t, "plugin.yaml")
	assetData := []byte("payload")
	srv, _ := installServer(t, manifest, assetData, "driver.bin")
	defer srv.Close()

	pluginsDir := t.TempDir()
	lockPath := filepath.Join(pluginsDir, "plugins.lock")
	inst := newInstaller(srv, pluginsDir, lockPath)

	_, err := inst.Install(context.Background(), InstallOptions{
		Source:       "example/driver",
		Digest:       strings.Repeat("0", 64),
		ConfirmPerms: true,
	})
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("want ErrDigestMismatch, got %v", err)
	}
	// No asset, manifest or lock file should be left behind.
	err = filepath.Walk(pluginsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			t.Fatalf("unexpected file left behind: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk plugins dir: %v", err)
	}
}

func TestAtomicLockfileConcurrentWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plugins.lock")
	const writers = 24
	ids := make([]string, writers)
	for i := range ids {
		ids[i] = fmt.Sprintf("io.github.writer%d.plugin", i)
	}

	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			lock, err := LoadLockFile(path)
			if err != nil {
				t.Errorf("LoadLockFile: %v", err)
				return
			}
			lock.Upsert(LockedPlugin{
				ID:       id,
				Version:  "0.1.0",
				Digest:   strings.Repeat("a", 64),
				Source:   "https://github.com/example/driver",
				Verified: true,
			})
			if err := WriteLockFile(path, lock); err != nil {
				t.Errorf("WriteLockFile: %v", err)
			}
		}(ids[i])
	}
	wg.Wait()

	lock, err := LoadLockFile(path)
	if err != nil {
		t.Fatalf("final lockfile should parse cleanly: %v", err)
	}
	if lock.FormatVersion != LockFormatVersion {
		t.Fatalf("unexpected format version: %d", lock.FormatVersion)
	}
	if len(lock.Plugins) == 0 {
		t.Fatal("lockfile should contain at least one plugin")
	}
	// At least one writer's entry must have survived; the point is that the file was
	// never torn or corrupted even though concurrent read-modify-write may drop updates.
	if len(lock.Plugins) == 0 {
		t.Fatal("lockfile should contain at least one plugin")
	}
}

func TestRejectArchiveTraversal(t *testing.T) {
	t.Run("SafePluginID never escapes", func(t *testing.T) {
		root := t.TempDir()
		for _, input := range []string{"..", "../escape", "../../etc/passwd", "a/b", "C:\\Windows", "", "\\", "..."} {
			got := SafePluginID(input)
			if got == "" || got == "." || got == ".." {
				t.Fatalf("SafePluginID(%q) = %q is unsafe", input, got)
			}
			if strings.ContainsAny(got, `/\`) {
				t.Fatalf("SafePluginID(%q) = %q contains a separator", input, got)
			}
			joined := filepath.Join(root, got)
			if !pathWithin(root, joined) {
				t.Fatalf("SafePluginID(%q) escapes root: %q", input, joined)
			}
		}
	})

	t.Run("asset name traversal cannot escape plugins root", func(t *testing.T) {
		manifest := readFixture(t, "plugin.yaml")
		assetData := []byte("payload")
		srv, _ := installServer(t, manifest, assetData, "../escape")
		defer srv.Close()

		pluginsDir := t.TempDir()
		inst := newInstaller(srv, pluginsDir, filepath.Join(pluginsDir, "plugins.lock"))
		res, err := inst.Install(context.Background(), InstallOptions{
			Source:       "example/driver",
			Digest:       SHA256Bytes(assetData),
			ConfirmPerms: true,
		})
		if err != nil {
			t.Fatalf("Install with traversal asset name: %v", err)
		}
		if !pathWithin(pluginsDir, res.AssetPath) {
			t.Fatalf("asset escaped plugins root: %s", res.AssetPath)
		}
		if _, err := os.Stat(filepath.Join(pluginsDir, "..", "escape")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("no file should be written outside the plugins root, got %v", err)
		}
	})

	t.Run("traversal plugin id is rejected fail-closed", func(t *testing.T) {
		manifest := readFixture(t, "plugin-traversal-id.yaml")
		srv, _ := installServer(t, manifest, []byte("payload"), "driver.bin")
		defer srv.Close()

		pluginsDir := t.TempDir()
		inst := newInstaller(srv, pluginsDir, filepath.Join(pluginsDir, "plugins.lock"))
		_, err := inst.Install(context.Background(), InstallOptions{Source: "example/driver", ConfirmPerms: true})
		if err == nil {
			t.Fatal("traversal plugin id should be rejected")
		}
		if !errors.Is(err, ErrInvalidManifest) {
			t.Fatalf("want ErrInvalidManifest, got %v", err)
		}
		if _, statErr := os.Stat(filepath.Join(pluginsDir, "..", "escape")); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("traversal id wrote outside the plugins root: %v", statErr)
		}
	})
}

func TestPermissionExpansionRequiresConfirmation(t *testing.T) {
	t.Run("PermissionExpansion reports additions", func(t *testing.T) {
		oldPerms := &Permissions{Network: []string{"outbound"}}
		newPerms := &Permissions{Network: []string{"outbound"}, Filesystem: []string{"/tmp"}, Secrets: []string{"token"}}
		added := PermissionExpansion(oldPerms, newPerms)
		want := []string{"filesystem:/tmp", "secrets:token"}
		if strings.Join(added, "|") != strings.Join(want, "|") {
			t.Fatalf("added = %v, want %v", added, want)
		}
		if got := PermissionExpansion(newPerms, oldPerms); len(got) != 0 {
			t.Fatalf("subset should not be an expansion: %v", got)
		}
	})

	t.Run("install refuses expansion without confirmation", func(t *testing.T) {
		existing := readFixture(t, "plugin.yaml")
		expanded := readFixture(t, "plugin-expanded.yaml")
		srv, _ := installServer(t, expanded, []byte("payload"), "driver.bin")
		defer srv.Close()

		pluginsDir := t.TempDir()
		pluginDir := filepath.Join(pluginsDir, SafePluginID("io.github.example.driver"))
		if err := os.MkdirAll(pluginDir, 0o755); err != nil {
			t.Fatal(err)
		}
		existingManifest, err := ValidateManifest(existing, testSchemaBytes(t))
		if err != nil {
			t.Fatal(err)
		}
		if err := writeAtomic(filepath.Join(pluginDir, "plugin.yaml"), readFixture(t, "plugin.yaml")); err != nil {
			t.Fatal(err)
		}
		_ = existingManifest

		inst := newInstaller(srv, pluginsDir, filepath.Join(pluginsDir, "plugins.lock"))
		_, err = inst.Install(context.Background(), InstallOptions{Source: "example/driver", ConfirmPerms: false})
		if err == nil {
			t.Fatal("expected permission confirmation error")
		}
		if !errors.Is(err, ErrPermissionConfirmationRequired) {
			t.Fatalf("want ErrPermissionConfirmationRequired, got %v", err)
		}
	})

	t.Run("install proceeds on expansion when confirmed", func(t *testing.T) {
		expanded := readFixture(t, "plugin-expanded.yaml")
		srv, _ := installServer(t, expanded, []byte("payload"), "driver.bin")
		defer srv.Close()

		pluginsDir := t.TempDir()
		pluginDir := filepath.Join(pluginsDir, SafePluginID("io.github.example.driver"))
		if err := os.MkdirAll(pluginDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := writeAtomic(filepath.Join(pluginDir, "plugin.yaml"), readFixture(t, "plugin.yaml")); err != nil {
			t.Fatal(err)
		}

		inst := newInstaller(srv, pluginsDir, filepath.Join(pluginsDir, "plugins.lock"))
		res, err := inst.Install(context.Background(), InstallOptions{
			Source:       "example/driver",
			Digest:       SHA256Bytes([]byte("payload")),
			ConfirmPerms: true,
		})
		if err != nil {
			t.Fatalf("confirmed expansion should install: %v", err)
		}
		if res.Manifest == nil {
			t.Fatal("expected install result")
		}
	})

	t.Run("no expansion proceeds without confirmation", func(t *testing.T) {
		// Same perms: installed network=[outbound], incoming network=[outbound].
		manifest := readFixture(t, "plugin.yaml")
		srv, _ := installServer(t, manifest, []byte("payload"), "driver.bin")
		defer srv.Close()

		pluginsDir := t.TempDir()
		pluginDir := filepath.Join(pluginsDir, SafePluginID("io.github.example.driver"))
		if err := os.MkdirAll(pluginDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := writeAtomic(filepath.Join(pluginDir, "plugin.yaml"), manifest); err != nil {
			t.Fatal(err)
		}
		inst := newInstaller(srv, pluginsDir, filepath.Join(pluginsDir, "plugins.lock"))
		if _, err := inst.Install(context.Background(), InstallOptions{
			Source:       "example/driver",
			Digest:       SHA256Bytes([]byte("payload")),
			ConfirmPerms: false,
		}); err != nil {
			t.Fatalf("non-expanding reinstall should not require confirmation: %v", err)
		}
	})
}

func TestCompatibilityFailClosed(t *testing.T) {
	base := func() *Manifest {
		return &Manifest{
			ID:            "io.github.example.driver",
			Version:       "0.1.0",
			Protocol:      1,
			Compatibility: Compatibility{Core: ">=0.1.0 <0.2.0"},
		}
	}

	valid := base()
	if err := ValidateManifestContract(valid, "0.1.0", 1); err != nil {
		t.Fatalf("valid manifest should pass: %v", err)
	}

	cases := []struct {
		name   string
		mut    func(*Manifest)
		target error
	}{
		{"nil manifest", func(m *Manifest) {}, ErrInvalidManifest},
		{"empty id", func(m *Manifest) { m.ID = "" }, ErrInvalidManifest},
		{"traversal id", func(m *Manifest) { m.ID = "../escape" }, ErrInvalidManifest},
		{"bad version", func(m *Manifest) { m.Version = "not-semver" }, ErrInvalidManifest},
		{"wrong protocol", func(m *Manifest) { m.Protocol = 2 }, ErrProtocolIncompatible},
		{"zero protocol", func(m *Manifest) { m.Protocol = 0 }, ErrProtocolIncompatible},
		{"empty compat", func(m *Manifest) { m.Compatibility.Core = "" }, ErrCoreIncompatible},
		{"non-matching compat", func(m *Manifest) { m.Compatibility.Core = ">=2.0.0" }, ErrCoreIncompatible},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := base()
			if tc.name != "nil manifest" {
				tc.mut(m)
			} else {
				m = nil
			}
			var err error
			if m == nil {
				err = ValidateManifestContract(nil, "0.1.0", 1)
			} else {
				err = ValidateManifestContract(m, "0.1.0", 1)
			}
			if err == nil {
				t.Fatal("expected an error")
			}
			if !errors.Is(err, tc.target) {
				t.Fatalf("want %v, got %v", tc.target, err)
			}
		})
	}
}

func TestConcurrentInstallSamePlugin(t *testing.T) {
	manifest := readFixture(t, "plugin.yaml")
	assetData := []byte("concurrent-payload")
	srv, _ := installServer(t, manifest, assetData, "driver.bin")
	defer srv.Close()

	pluginsDir := t.TempDir()
	lockPath := filepath.Join(pluginsDir, "plugins.lock")
	inst := newInstaller(srv, pluginsDir, lockPath)
	digest := SHA256Bytes(assetData)

	const workers = 6
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := inst.Install(context.Background(), InstallOptions{
				Source:       "example/driver",
				Digest:       digest,
				ConfirmPerms: true,
			})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent install failed: %v", err)
		}
	}

	// The asset is stored by digest under plugins/<id>/assets and must be intact.
	pluginDir := filepath.Join(pluginsDir, SafePluginID("io.github.example.driver"))
	assetPath := filepath.Join(pluginDir, "assets", digest)
	data, err := os.ReadFile(assetPath)
	if err != nil {
		t.Fatalf("asset should exist and be readable: %v", err)
	}
	if SHA256Bytes(data) != digest {
		t.Fatal("asset content digest mismatch after concurrent install")
	}
	if _, err := os.Stat(filepath.Join(pluginDir, "plugin.yaml")); err != nil {
		t.Fatalf("manifest should exist: %v", err)
	}
	lock, err := LoadLockFile(lockPath)
	if err != nil {
		t.Fatalf("lock should parse: %v", err)
	}
	entry, ok := lock.Find("io.github.example.driver")
	if !ok || entry.Digest != digest || !entry.Verified {
		t.Fatalf("lock entry should be present and verified: %+v", entry)
	}
}

func TestSafeURLRedactsSecrets(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"https://github.com/owner/repo/asset?access_token=SECRET", "https://github.com/owner/repo/asset?access_token=REDACTED"},
		{"https:" + "//user:pass@example.com/asset", "https://REDACTED@example.com/asset"},
		{"https://example.com/asset?token=abc&x=1", "https://example.com/asset?token=REDACTED&x=1"},
		{"https://example.com/asset?a=1", "https://example.com/asset?a=1"},
	}
	for _, tc := range cases {
		if got := safeURL(tc.in); got != tc.want {
			t.Fatalf("safeURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestIsWindowsSharingViolation(t *testing.T) {
	sharing := &os.PathError{Op: "open", Path: "plugins.lock", Err: syscall.Errno(32)}
	if !isWindowsSharingViolation(sharing) {
		t.Fatal("ERROR_SHARING_VIOLATION should be detected")
	}
	lock := &os.PathError{Op: "open", Path: "plugins.lock", Err: syscall.Errno(33)}
	if !isWindowsSharingViolation(lock) {
		t.Fatal("ERROR_LOCK_VIOLATION should be detected")
	}
	notExist := &os.PathError{Op: "open", Path: "plugins.lock", Err: os.ErrNotExist}
	if isWindowsSharingViolation(notExist) {
		t.Fatal("ErrNotExist should not be treated as a sharing violation")
	}
}
