package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeliciousBuding/cloud-path/internal/plugincatalog"
	"github.com/DeliciousBuding/cloud-path/internal/registry"
)

type assetTestCatalog struct {
	views map[string]map[string]plugincatalog.PluginView
}

func (c assetTestCatalog) Plugins(tenant string) ([]plugincatalog.PluginView, error) {
	out := make([]plugincatalog.PluginView, 0, len(c.views[tenant]))
	for _, view := range c.views[tenant] {
		out = append(out, view)
	}
	return out, nil
}

func (c assetTestCatalog) Plugin(tenant, id string) (plugincatalog.PluginView, bool, error) {
	view, ok := c.views[tenant][id]
	return view, ok, nil
}

func installUIAssetPlugin(t *testing.T, srv *Server, id, version, manifest string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	pluginsDir := filepath.Join(root, "plugins.d")
	lockPath := filepath.Join(root, "plugins.lock")
	pluginRoot := filepath.Join(pluginsDir, registry.SafePluginID(id))
	if err := os.MkdirAll(pluginRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, "plugin.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	for rel, body := range files {
		full := filepath.Join(pluginRoot, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	lock := registry.NewLockFile()
	lock.Upsert(registry.LockedPlugin{
		ID: id, Version: version, Digest: strings.Repeat("a", 64),
		Source: "test", Mode: registry.TrustModeVerifiedRegistry, Verified: true, Protocol: 1,
	})
	if err := registry.WriteLockFile(lockPath, lock); err != nil {
		t.Fatal(err)
	}
	setAppHostDirs(t, srv, pluginsDir, lockPath, filepath.Join(root, "state"))
	return pluginRoot
}

func uiAssetManifest(id, version string, custom bool) string {
	section := "              - type: status\n"
	if custom {
		section += "              - type: custom\n                entry: ui/index.html\n                scopes: [instance.read]\n"
	}
	return `apiVersion: plugins.cloudpath.dev/v1alpha1
kind: Application
id: ` + id + `
version: ` + version + `
protocol: 1
entrypoint: ./app
compatibility:
  core: ">=0.1.0 <0.2.0"
contributes:
  applications:
    - id: app
      ui:
        apiVersion: 1
        navigation:
          title: Test App
          route: test-app
        pages:
          - id: home
            title: Test App
            sections:
` + section
}

func TestPluginUIAssetEndpointServesDeclaredCustomUI(t *testing.T) {
	srv, _, _, _, a, _ := setupPluginPlane(t)
	const id, version = "io.test.ui-app", "1.0.0"
	root := installUIAssetPlugin(t, srv, id, version, uiAssetManifest(id, version, true), map[string]string{
		"ui/index.html": "<!doctype html><script src=\"app.js\"></script>",
		"ui/app.js":     "window.parent.postMessage({type:'ready'}, '*');",
	})

	path := "/api/plugin-ui/assets/" + id + "/" + version + "/ui/index.html"
	rec := servePlugin(t, srv, http.MethodGet, path, "", a, "tenant-a", "viewer")
	if rec.Code != http.StatusOK {
		t.Fatalf("asset = %d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "SAMEORIGIN" {
		t.Fatalf("X-Frame-Options = %q", got)
	}
	if got := rec.Header().Get("Content-Security-Policy"); !strings.Contains(got, "frame-ancestors 'self'") {
		t.Fatalf("CSP = %q", got)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Fatalf("Content-Type = %q", got)
	}
	if strings.Contains(rec.Body.String(), root) || strings.Contains(rec.Body.String(), "plugins.d") {
		t.Fatalf("asset response leaked local path: %s", rec.Body.String())
	}

	rec = servePlugin(t, srv, http.MethodGet, "/api/plugin-ui/assets/"+id+"/"+version+"/ui/app.js", "", a, "tenant-a", "viewer")
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/javascript") {
		t.Fatalf("js asset = %d content-type=%q", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestPluginUIAssetEndpointOpenModeAndDeclaredCustomEntry(t *testing.T) {
	srv, _, _, _, a, _ := setupPluginPlane(t)
	const id, version = "io.test.ui-no-custom", "1.0.0"
	installUIAssetPlugin(t, srv, id, version, uiAssetManifest(id, version, false), map[string]string{
		"ui/index.html": "should not be served",
	})
	path := "/api/plugin-ui/assets/" + id + "/" + version + "/ui/index.html"
	rec := servePlugin(t, srv, http.MethodGet, path, "", a, "tenant-a", "viewer")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("plugin without custom entry = %d, want 404", rec.Code)
	}

	open := httptest.NewRecorder()
	srv.Routes().ServeHTTP(open, httptest.NewRequest(http.MethodGet, path, nil))
	if open.Code != http.StatusNotFound {
		t.Fatalf("open mode must still fail closed without custom entry: %d", open.Code)
	}

	validID, validVersion := "io.test.ui-open", "1.0.0"
	installUIAssetPlugin(t, srv, validID, validVersion, uiAssetManifest(validID, validVersion, true), map[string]string{
		"ui/index.html": "open",
	})
	openPath := "/api/plugin-ui/assets/" + validID + "/" + validVersion + "/ui/index.html"
	open = httptest.NewRecorder()
	srv.Routes().ServeHTTP(open, httptest.NewRequest(http.MethodGet, openPath, nil))
	if open.Code != http.StatusOK {
		t.Fatalf("open mode anonymous asset = %d body=%s", open.Code, open.Body.String())
	}

	srv.cfg.RequireAuth = true
	account := httptest.NewRecorder()
	srv.Routes().ServeHTTP(account, httptest.NewRequest(http.MethodGet, openPath, nil))
	if account.Code != http.StatusUnauthorized {
		t.Fatalf("account mode anonymous asset = %d, want 401", account.Code)
	}
}

func TestPluginUIAssetEndpointRejectsTraversalMIMEAndSize(t *testing.T) {
	srv, _, _, _, a, _ := setupPluginPlane(t)
	const id, version = "io.test.ui-paths", "1.0.0"
	installUIAssetPlugin(t, srv, id, version, uiAssetManifest(id, version, true), map[string]string{
		"ui/index.html": "ok",
		"ui/app.txt":    "not allowed",
		"ui/large.html": strings.Repeat("x", pluginUIAssetMaxBytes+1),
	})
	base := "/api/plugin-ui/assets/" + id + "/" + version + "/"
	for _, tc := range []struct {
		name string
		path string
		want int
	}{
		{"traversal", base + "ui/../plugin.yaml", http.StatusForbidden},
		{"mime", base + "ui/app.txt", http.StatusUnsupportedMediaType},
		{"size", base + "ui/large.html", http.StatusRequestEntityTooLarge},
		{"wrong version", "/api/plugin-ui/assets/" + id + "/2.0.0/ui/index.html", http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := servePlugin(t, srv, http.MethodGet, tc.path, "", a, "tenant-a", "viewer")
			if rec.Code != tc.want {
				t.Fatalf("%s = %d body=%s, want %d", tc.name, rec.Code, rec.Body.String(), tc.want)
			}
		})
	}
}

func TestPluginUIAssetEndpointRejectsSymlinkEscape(t *testing.T) {
	srv, _, _, _, a, _ := setupPluginPlane(t)
	const id, version = "io.test.ui-link", "1.0.0"
	root := installUIAssetPlugin(t, srv, id, version, uiAssetManifest(id, version, true), map[string]string{
		"ui/index.html": "ok",
	})
	outside := filepath.Join(t.TempDir(), "outside.html")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "ui", "leak.html")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	rec := servePlugin(t, srv, http.MethodGet, "/api/plugin-ui/assets/"+id+"/"+version+"/ui/leak.html", "", a, "tenant-a", "viewer")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("symlink escape = %d body=%s, want 404", rec.Code, rec.Body.String())
	}
}

func TestPluginUIAssetEndpointRespectsTenantVisibility(t *testing.T) {
	srv, _, _, _, a, b := setupPluginPlane(t)
	const id, version = "io.test.ui-tenant", "1.0.0"
	installUIAssetPlugin(t, srv, id, version, uiAssetManifest(id, version, true), map[string]string{
		"ui/index.html": "tenant scoped",
	})
	srv.pluginCatalog = assetTestCatalog{views: map[string]map[string]plugincatalog.PluginView{
		"tenant-a": {id: {ID: id, Version: version, Kind: "Application"}},
	}}
	path := "/api/plugin-ui/assets/" + id + "/" + version + "/ui/index.html"
	if rec := servePlugin(t, srv, http.MethodGet, path, "", a, "tenant-a", "viewer"); rec.Code != http.StatusOK {
		t.Fatalf("visible tenant asset = %d body=%s", rec.Code, rec.Body.String())
	}
	if rec := servePlugin(t, srv, http.MethodGet, path, "", b, "tenant-b", "viewer"); rec.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant asset = %d, want 404", rec.Code)
	}
}
