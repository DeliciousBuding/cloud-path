package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/auth"
	"github.com/DeliciousBuding/cloud-path/internal/plugincatalog"
	"github.com/DeliciousBuding/cloud-path/internal/plugincontrol"
	"github.com/DeliciousBuding/cloud-path/internal/registry"
)

// newPluginCatalog 构造一个含 tenant-a/tenant-b 各一实例的只读目录。
func newPluginCatalog(t *testing.T) plugincatalog.Catalog {
	t.Helper()
	lock := registry.NewLockFile()
	lock.Plugins = []registry.LockedPlugin{{
		ID: "io.github.acme.driver", Version: "0.1.0", Digest: "sha256:abc123",
		Source: "https://github.com/acme/driver", Verified: true,
		Protocol: 1, Compatibility: ">=0.2.0",
	}}
	manifest := &registry.Manifest{
		APIVersion: "plugins.cloudpath.dev/v1alpha1", Kind: "Driver",
		ID: "io.github.acme.driver", Version: "0.1.0", Protocol: 1,
		Compatibility: registry.Compatibility{Core: ">=0.2.0"},
		Permissions:   registry.Permissions{Secrets: []string{"api-key"}},
		Contributes: &registry.Contributes{Drivers: []registry.DriverContribution{
			{ID: "stcb", Title: "STC-B Driver", Discovery: "manual"},
		}},
	}
	desired := []plugincontrol.InstanceState{
		{Tenant: "tenant-a", InstanceID: "a1", PluginID: "io.github.acme.driver", Version: "0.1.0", Enabled: true, ConfigPath: `/tmp/instances/a.json`, Isolation: plugincontrol.IsolationShared},
		{Tenant: "tenant-b", InstanceID: "b1", PluginID: "io.github.acme.driver", Version: "0.1.0", Enabled: false, Isolation: plugincontrol.IsolationShared},
	}
	rd := &plugincatalog.SourceReader{
		LockfileFn: func() (*registry.LockFile, error) { return lock, nil },
		ManifestFn: func(id string) (*registry.Manifest, error) { return manifest, nil },
		DesiredFn: func(tenant string) ([]plugincontrol.InstanceState, error) {
			var out []plugincontrol.InstanceState
			for _, s := range desired {
				if tenant == "" || s.Tenant == tenant {
					out = append(out, s)
				}
			}
			return out, nil
		},
		ObservedFn: func(tenant, id string) (plugincatalog.Observed, bool) {
			return plugincatalog.Observed{}, false
		},
	}
	return plugincatalog.New(rd)
}

// servePluginAs 以指定身份请求插件目录端点。
func servePluginAs(t *testing.T, srv *Server, method, target, tenantSlug, role string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	req = req.WithContext(auth.WithPrincipal(req.Context(), &auth.Principal{
		TenantSlug: tenantSlug, Role: role,
	}))
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	return rec
}

// TestPluginsAPIViewerAccess 锁定 viewer 可读本租户插件/实例；nil catalog 返回空列表不崩溃。
func TestPluginsAPIViewerAccess(t *testing.T) {
	srv := New(Config{Version: "test", PluginCatalog: newPluginCatalog(t)})
	t.Cleanup(func() { srv.CloseAll() })

	rec := servePluginAs(t, srv, http.MethodGet, "/api/plugins", "tenant-a", string(api.RoleViewer))
	if rec.Code != http.StatusOK {
		t.Fatalf("list plugins = %d, want 200", rec.Code)
	}
	var list struct {
		Plugins []plugincatalog.PluginView `json:"plugins"`
	}
	decodeJSON(t, rec, &list)
	if len(list.Plugins) != 1 || list.Plugins[0].ID != "io.github.acme.driver" {
		t.Fatalf("plugins leaked/incomplete: %+v", list.Plugins)
	}

	rec = servePluginAs(t, srv, http.MethodGet, "/api/plugins/io.github.acme.driver", "tenant-a", string(api.RoleViewer))
	if rec.Code != http.StatusOK {
		t.Fatalf("get plugin = %d, want 200", rec.Code)
	}
	var p plugincatalog.PluginView
	decodeJSON(t, rec, &p)
	if p.ID != "io.github.acme.driver" || p.Kind != "Driver" {
		t.Fatalf("plugin view = %+v", p)
	}

	rec = servePluginAs(t, srv, http.MethodGet, "/api/plugin-instances", "tenant-a", string(api.RoleViewer))
	if rec.Code != http.StatusOK {
		t.Fatalf("list instances = %d, want 200", rec.Code)
	}
	var inst struct {
		Instances []plugincatalog.InstanceView `json:"instances"`
	}
	decodeJSON(t, rec, &inst)
	if len(inst.Instances) != 1 || inst.Instances[0].ID != "a1" {
		t.Fatalf("instances leaked/incomplete: %+v", inst.Instances)
	}

	rec = servePluginAs(t, srv, http.MethodGet, "/api/plugin-instances/a1", "tenant-a", string(api.RoleViewer))
	if rec.Code != http.StatusOK {
		t.Fatalf("get instance = %d, want 200", rec.Code)
	}
	var iv plugincatalog.InstanceView
	raw := rec.Body.String()
	decodeJSON(t, rec, &iv)
	if iv.ID != "a1" || iv.ObservedState != "unknown" {
		t.Fatalf("instance view = %+v", iv)
	}
	// 反向验证：config 路径/token 字符串不得出现在 API JSON。
	for _, banned := range []string{"/tmp/instances/a.json", "sk-"} {
		if strings.Contains(raw, banned) {
			t.Fatalf("instance JSON leaked %q: %s", banned, raw)
		}
	}

	// nil catalog -> 空列表，不崩溃。
	nilSrv := New(Config{Version: "test"})
	t.Cleanup(func() { nilSrv.CloseAll() })
	rec = servePluginAs(t, nilSrv, http.MethodGet, "/api/plugins", "tenant-a", string(api.RoleViewer))
	if rec.Code != http.StatusOK {
		t.Fatalf("nil catalog list plugins = %d, want 200", rec.Code)
	}
	var empty struct {
		Plugins []plugincatalog.PluginView `json:"plugins"`
	}
	decodeJSON(t, rec, &empty)
	if len(empty.Plugins) != 0 {
		t.Fatalf("nil catalog plugins = %+v, want empty", empty.Plugins)
	}
}

// TestPluginInstanceCrossTenant404 锁定跨租户/未知实例一律 404。
func TestPluginInstanceCrossTenant404(t *testing.T) {
	srv := New(Config{Version: "test", PluginCatalog: newPluginCatalog(t)})
	t.Cleanup(func() { srv.CloseAll() })

	if rec := servePluginAs(t, srv, http.MethodGet, "/api/plugin-instances/b1", "tenant-a", string(api.RoleAdmin)); rec.Code != http.StatusNotFound {
		t.Fatalf("tenant-a read tenant-b instance = %d, want 404", rec.Code)
	}
	if rec := servePluginAs(t, srv, http.MethodGet, "/api/plugin-instances/a1", "tenant-b", string(api.RoleAdmin)); rec.Code != http.StatusNotFound {
		t.Fatalf("tenant-b read tenant-a instance = %d, want 404", rec.Code)
	}
	if rec := servePluginAs(t, srv, http.MethodGet, "/api/plugin-instances/a1", "tenant-a", string(api.RoleAdmin)); rec.Code != http.StatusOK {
		t.Fatalf("tenant-a own instance = %d, want 200", rec.Code)
	}
	if rec := servePluginAs(t, srv, http.MethodGet, "/api/plugin-instances/unknown-id", "tenant-a", string(api.RoleAdmin)); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown instance = %d, want 404", rec.Code)
	}
}
