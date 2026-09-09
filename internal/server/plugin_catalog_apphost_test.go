package server

import (
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/plugincatalog"
	"github.com/DeliciousBuding/cloud-path/internal/registry"
)

func listPluginViews(t *testing.T, srv *Server, tenantID int64, slug string) []plugincatalog.PluginView {
	t.Helper()
	rec := servePlugin(t, srv, http.MethodGet, "/api/plugins", "", tenantID, slug, string(api.RoleViewer))
	if rec.Code != http.StatusOK {
		t.Fatalf("list plugins = %d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Plugins []plugincatalog.PluginView `json:"plugins"`
	}
	decodeJSON(t, rec, &body)
	return body.Plugins
}

func getPluginView(t *testing.T, srv *Server, tenantID int64, slug, pluginID string) plugincatalog.PluginView {
	t.Helper()
	rec := servePlugin(t, srv, http.MethodGet, "/api/plugins/"+pluginID, "", tenantID, slug, string(api.RoleViewer))
	if rec.Code != http.StatusOK {
		t.Fatalf("get plugin %s = %d body=%s", pluginID, rec.Code, rec.Body.String())
	}
	var view plugincatalog.PluginView
	decodeJSON(t, rec, &view)
	return view
}

func setAppHostDirs(t *testing.T, srv *Server, pluginsDir, lockPath, stateDir string) {
	t.Helper()
	ah, err := NewAppHost(srv, AppHostConfig{
		Enabled: true, PluginsDir: pluginsDir, LockPath: lockPath, StateDir: stateDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv.SetAppHost(ah)
	t.Cleanup(ah.Close)
}

func installAppHostManifests(t *testing.T, srv *Server, manifests map[string]string) {
	t.Helper()
	root := t.TempDir()
	pluginsDir := filepath.Join(root, "plugins.d")
	lockPath := filepath.Join(root, "plugins.lock")
	lock := registry.NewLockFile()
	for id, body := range manifests {
		manifest, err := registry.ParseManifest([]byte(body))
		if err != nil {
			t.Fatalf("parse test manifest %s: %v", id, err)
		}
		dir := filepath.Join(pluginsDir, registry.SafePluginID(id))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		lock.Upsert(registry.LockedPlugin{
			ID: id, Version: manifest.Version, Digest: strings.Repeat("a", 64),
			Source: "test", Mode: registry.TrustModeVerifiedRegistry, Verified: true,
			VerifiedPublisher: "acme", Protocol: 1, Compatibility: manifest.Compatibility.Core,
		})
	}
	if err := registry.WriteLockFile(lockPath, lock); err != nil {
		t.Fatal(err)
	}
	setAppHostDirs(t, srv, pluginsDir, lockPath, filepath.Join(root, "state"))
}

func applicationManifest(id, version string) string {
	return `apiVersion: plugins.cloudpath.dev/v1alpha1
kind: Application
id: ` + id + `
version: ` + version + `
protocol: 1
entrypoint: ./app
compatibility:
  core: ">=0.1.0 <0.2.0"
`
}

// TestPluginsAPICanonicalizesEdgeAndAppHost 锁定列表与详情的同一份去重事实：
// Edge 上报优先于 AppHost；同一 plugin_id 多 Edge 重版取最高 semver；
// AppHost Application 目录是 Server 级事实，对所有租户可见。
func TestPluginsAPICanonicalizesEdgeAndAppHost(t *testing.T) {
	srv, _, _, mem, a, b := setupPluginPlane(t)
	seedInstallations(t, srv, mem, a, "e1", []api.PluginInstallationStatusData{{
		PluginID: "io.test.shared", Version: "1.0.0", Kind: "Driver", Protocol: 1, Digest: "edge-low",
	}})
	seedInstallations(t, srv, mem, a, "e2", []api.PluginInstallationStatusData{{
		PluginID: "io.test.shared", Version: "2.0.0", Kind: "Driver", Protocol: 1, Digest: "edge-high",
	}})
	installAppHostManifests(t, srv, map[string]string{
		"io.test.shared":      applicationManifest("io.test.shared", "3.0.0"),
		"io.test.server-only": applicationManifest("io.test.server-only", "4.0.0"),
	})

	listA := listPluginViews(t, srv, a, "tenant-a")
	byID := map[string]plugincatalog.PluginView{}
	for _, view := range listA {
		byID[view.ID] = view
	}
	sharedA, ok := byID["io.test.shared"]
	if !ok || sharedA.Version != "2.0.0" || sharedA.Kind != "Driver" || sharedA.Digest != "edge-high" {
		t.Fatalf("Edge 应优先且同源取最高版本: %+v", sharedA)
	}
	if view, ok := byID["io.test.server-only"]; !ok || view.Version != "4.0.0" || view.Kind != "Application" {
		t.Fatalf("AppHost 插件缺失: %+v", byID)
	}
	if detail := getPluginView(t, srv, a, "tenant-a", "io.test.shared"); !reflect.DeepEqual(detail, sharedA) {
		t.Fatalf("列表与详情不一致: list=%+v detail=%+v", sharedA, detail)
	}
	if again := listPluginViews(t, srv, a, "tenant-a"); !reflect.DeepEqual(again, listA) {
		t.Fatalf("列表去重不确定: first=%+v again=%+v", listA, again)
	}

	listB := listPluginViews(t, srv, b, "tenant-b")
	byID = map[string]plugincatalog.PluginView{}
	for _, view := range listB {
		byID[view.ID] = view
	}
	sharedB, ok := byID["io.test.shared"]
	if !ok || sharedB.Version != "3.0.0" || sharedB.Kind != "Application" {
		t.Fatalf("AppHost 对无 Edge 租户应可见: %+v", sharedB)
	}
	if detail := getPluginView(t, srv, b, "tenant-b", "io.test.shared"); !reflect.DeepEqual(detail, sharedB) {
		t.Fatalf("租户-b 列表与详情不一致: list=%+v detail=%+v", sharedB, detail)
	}
}

// TestPluginsAPIAppHostMetadataFailClosed 锁定 AppHost lock/manifest 损坏时
// 读面返回 500，不静默隐藏安装事实。
func TestPluginsAPIAppHostMetadataFailClosed(t *testing.T) {
	t.Run("malformed lock", func(t *testing.T) {
		srv, _, _, _, a, _ := setupPluginPlane(t)
		root := t.TempDir()
		lockPath := filepath.Join(root, "plugins.lock")
		if err := os.WriteFile(lockPath, []byte("plugins: [\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		setAppHostDirs(t, srv, filepath.Join(root, "plugins.d"), lockPath, filepath.Join(root, "state"))
		rec := servePlugin(t, srv, http.MethodGet, "/api/plugins", "", a, "tenant-a", string(api.RoleViewer))
		if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "plugin catalog unavailable") {
			t.Fatalf("malformed lock list = %d body=%s", rec.Code, rec.Body.String())
		}
		rec = servePlugin(t, srv, http.MethodGet, "/api/plugins/io.test.missing", "", a, "tenant-a", string(api.RoleViewer))
		if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "plugin catalog unavailable") {
			t.Fatalf("malformed lock detail = %d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("missing manifest", func(t *testing.T) {
		srv, _, _, _, a, _ := setupPluginPlane(t)
		root := t.TempDir()
		pluginsDir := filepath.Join(root, "plugins.d")
		lockPath := filepath.Join(root, "plugins.lock")
		lock := registry.NewLockFile()
		lock.Upsert(registry.LockedPlugin{
			ID: "io.test.missing", Version: "1.0.0", Digest: strings.Repeat("b", 64),
			Source: "test", Verified: true, Protocol: 1,
		})
		if err := registry.WriteLockFile(lockPath, lock); err != nil {
			t.Fatal(err)
		}
		setAppHostDirs(t, srv, pluginsDir, lockPath, filepath.Join(root, "state"))
		rec := servePlugin(t, srv, http.MethodGet, "/api/plugins", "", a, "tenant-a", string(api.RoleViewer))
		if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "plugin catalog unavailable") {
			t.Fatalf("missing manifest list = %d body=%s", rec.Code, rec.Body.String())
		}
		rec = servePlugin(t, srv, http.MethodGet, "/api/plugins/io.test.missing", "", a, "tenant-a", string(api.RoleViewer))
		if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "plugin catalog unavailable") {
			t.Fatalf("missing manifest detail = %d body=%s", rec.Code, rec.Body.String())
		}
	})
}

// TestPluginsAPIAppHostVisibleWithoutEdgePlane 锁定 AppHost 目录不依赖 Edge 插件面。
func TestPluginsAPIAppHostVisibleWithoutEdgePlane(t *testing.T) {
	srv := New(Config{Version: "test"})
	t.Cleanup(srv.CloseAll)
	installAppHostManifests(t, srv, map[string]string{
		"io.test.server-only": applicationManifest("io.test.server-only", "1.0.0"),
	})
	views := listPluginViews(t, srv, 1, "tenant-a")
	if len(views) != 1 || views[0].ID != "io.test.server-only" || views[0].Kind != "Application" {
		t.Fatalf("Edge 插件面未接线时 AppHost 插件应可见: %+v", views)
	}
	if detail := getPluginView(t, srv, 1, "tenant-a", "io.test.server-only"); !reflect.DeepEqual(detail, views[0]) {
		t.Fatalf("未接线 Edge 插件面时列表与详情不一致: list=%+v detail=%+v", views[0], detail)
	}
}

// TestPluginsAPIAppHostPublicFieldsAndRedaction 锁定公开字段映射与路径边界：
// 权限/贡献/capability 可见，entrypoint/descriptor/configSchema/capabilityCatalog
// 的本地绝对路径不得进入 HTTP 响应。
func TestPluginsAPIAppHostPublicFieldsAndRedaction(t *testing.T) {
	srv, _, _, _, a, _ := setupPluginPlane(t)
	manifest := `apiVersion: plugins.cloudpath.dev/v1alpha1
kind: Application
id: io.test.server-app
version: 1.0.0
protocol: 1
entrypoint: C:\secret\app.exe
compatibility:
  core: ">=0.1.0 <0.2.0"
permissions:
  hardware: [serial]
  network: [outbound]
  filesystem: [config]
  secrets: [api_token]
capabilities:
  - cloudpath.dev/capability/test@1
contributes:
  applications:
    - id: app.test
      title: Test App
  drivers:
    - id: driver.test
      title: Test Driver
      descriptor: C:\secret\descriptor.json
      configSchema: /secret/config.json
      discovery: manual
      capabilityCatalog: C:\secret\catalog.json
  connectors:
    - id: connector.test
      title: Test Connector
      direction: inbound
      host: example.test
`
	installAppHostManifests(t, srv, map[string]string{"io.test.server-app": manifest})
	views := listPluginViews(t, srv, a, "tenant-a")
	if len(views) != 1 {
		t.Fatalf("views = %+v", views)
	}
	view := views[0]
	if !view.Verified || view.Digest != strings.Repeat("a", 64) {
		t.Fatalf("信任字段错误: %+v", view)
	}
	if view.Permissions.Secrets[0] != "api_token" || view.Permissions.Hardware[0] != "serial" ||
		len(view.Contributes.Applications) != 1 || len(view.Contributes.Drivers) != 1 ||
		len(view.Contributes.Connectors) != 1 {
		t.Fatalf("公开字段缺失: %+v", view)
	}
	if view.Contributes.Drivers[0].Descriptor != "" || view.Contributes.Drivers[0].ConfigSchema != "" ||
		view.Contributes.Drivers[0].CapabilityCatalog != "" {
		t.Fatalf("贡献路径字段未收口: %+v", view.Contributes.Drivers[0])
	}
	rec := servePlugin(t, srv, http.MethodGet, "/api/plugins", "", a, "tenant-a", string(api.RoleViewer))
	raw := rec.Body.String()
	for _, banned := range []string{`C:\secret`, `/secret/`, "descriptor.json", "config.json", "catalog.json",
		"acme", string(registry.TrustModeVerifiedRegistry), "cloudpath.dev/capability/test@1"} {
		if strings.Contains(raw, banned) {
			t.Fatalf("响应泄漏 %q: %s", banned, raw)
		}
	}
}
