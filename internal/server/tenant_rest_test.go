package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/auth"
	"github.com/DeliciousBuding/cloud-path/internal/store"
)

// setupTenantREST 构造一个含 tenant-a/tenant-b 两个租户设备的 server，并返回两个租户 id。
func setupTenantREST(t *testing.T) (*Server, int64, int64) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "tenant-rest.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	insertTenant := func(slug string) int64 {
		t.Helper()
		if res, err := st.GetTenantBySlug(slug); err == nil {
			return res.ID
		}
		id, err := st.CreateTenant(slug, slug)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	a := insertTenant("tenant-a")
	b := insertTenant("tenant-b")
	if err := st.UpsertDeviceTenant("a/d1", "a", "stcb", "A", "COM1", a); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertDeviceTenant("b/d2", "b", "stcb", "B", "COM2", b); err != nil {
		t.Fatal(err)
	}
	if err := st.SetState("a/d1", `{"x":1}`, true, 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetState("b/d2", `{"x":2}`, true, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddEvent("a/d1", "BOOT", "{}", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddEvent("b/d2", "MISSED", "{}", 2); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateCommandTenant("a/d1", "sync", "", a); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateCommandTenant("b/d2", "dump", "", b); err != nil {
		t.Fatal(err)
	}
	srv := New(Config{Store: st, Version: "test"})
	t.Cleanup(func() { srv.CloseAll() })
	return srv, a, b
}

// serveTenant 以指定租户身份请求，并返回 httptest.ResponseRecorder。
func serveTenant(t *testing.T, srv *Server, method, target, body string, tenantID int64, tenantSlug string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:1234"
	req = req.WithContext(auth.WithPrincipal(req.Context(), &auth.Principal{
		TenantID: tenantID, TenantSlug: tenantSlug, Role: string(api.RoleAdmin),
	}))
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	return rec
}

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder, out any) {
	t.Helper()
	if err := json.NewDecoder(rec.Body).Decode(out); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
}

// TestRESTTenantDeviceIsolation 锁定 /api/devices、单设备与 /api/edges 的租户过滤。
func TestRESTTenantDeviceIsolation(t *testing.T) {
	srv, a, b := setupTenantREST(t)

	var listA struct {
		Devices []api.DeviceView `json:"devices"`
	}
	rec := serveTenant(t, srv, http.MethodGet, "/api/devices", "", a, "tenant-a")
	if rec.Code != http.StatusOK {
		t.Fatalf("tenant-a list = %d", rec.Code)
	}
	decodeJSON(t, rec, &listA)
	if len(listA.Devices) != 1 || listA.Devices[0].ID != "a/d1" {
		t.Fatalf("tenant-a devices leaked: %+v", listA.Devices)
	}

	var listB struct {
		Devices []api.DeviceView `json:"devices"`
	}
	rec = serveTenant(t, srv, http.MethodGet, "/api/devices", "", b, "tenant-b")
	decodeJSON(t, rec, &listB)
	if len(listB.Devices) != 1 || listB.Devices[0].ID != "b/d2" {
		t.Fatalf("tenant-b devices leaked: %+v", listB.Devices)
	}

	if rec := serveTenant(t, srv, http.MethodGet, "/api/devices/a/d1", "", b, "tenant-b"); rec.Code != http.StatusNotFound {
		t.Fatalf("tenant-b access tenant-a device = %d, want 404", rec.Code)
	}
	if rec := serveTenant(t, srv, http.MethodGet, "/api/devices/a/d1", "", a, "tenant-a"); rec.Code != http.StatusOK {
		t.Fatalf("tenant-a own device = %d, want 200", rec.Code)
	}

	var edgesA struct {
		Edges []api.EdgeView `json:"edges"`
	}
	rec = serveTenant(t, srv, http.MethodGet, "/api/edges", "", a, "tenant-a")
	decodeJSON(t, rec, &edgesA)
	if len(edgesA.Edges) != 1 || edgesA.Edges[0].EdgeID != "a" {
		t.Fatalf("tenant-a edges leaked: %+v", edgesA.Edges)
	}
}

// TestRESTTenantEventIsolation 锁定 /api/events 的租户过滤与跨租户 device 过滤不可枚举。
func TestRESTTenantEventIsolation(t *testing.T) {
	srv, a, b := setupTenantREST(t)

	var evA struct {
		Events []api.EventView `json:"events"`
	}
	rec := serveTenant(t, srv, http.MethodGet, "/api/events", "", a, "tenant-a")
	decodeJSON(t, rec, &evA)
	if len(evA.Events) != 1 || evA.Events[0].DeviceID != "a/d1" {
		t.Fatalf("tenant-a events leaked: %+v", evA.Events)
	}
	var evB struct {
		Events []api.EventView `json:"events"`
	}
	rec = serveTenant(t, srv, http.MethodGet, "/api/events", "", b, "tenant-b")
	decodeJSON(t, rec, &evB)
	if len(evB.Events) != 1 || evB.Events[0].DeviceID != "b/d2" {
		t.Fatalf("tenant-b events leaked: %+v", evB.Events)
	}

	// tenant A 即使用已知 tenant B device key 也拿不到 B 的事件。
	var cross struct {
		Events []api.EventView `json:"events"`
	}
	rec = serveTenant(t, srv, http.MethodGet, "/api/events?device=b/d2", "", a, "tenant-a")
	decodeJSON(t, rec, &cross)
	if len(cross.Events) != 0 {
		t.Fatalf("tenant-a enumerated tenant-b events: %+v", cross.Events)
	}
}

// TestRESTTenantCommandIsolation 锁定 /api/commands 的租户过滤与跨租户 device 过滤不可枚举。
func TestRESTTenantCommandIsolation(t *testing.T) {
	srv, a, b := setupTenantREST(t)

	var cmdA struct {
		Commands []api.CommandView `json:"commands"`
	}
	rec := serveTenant(t, srv, http.MethodGet, "/api/commands", "", a, "tenant-a")
	decodeJSON(t, rec, &cmdA)
	if len(cmdA.Commands) != 1 || cmdA.Commands[0].DeviceID != "a/d1" {
		t.Fatalf("tenant-a commands leaked: %+v", cmdA.Commands)
	}
	var cmdB struct {
		Commands []api.CommandView `json:"commands"`
	}
	rec = serveTenant(t, srv, http.MethodGet, "/api/commands", "", b, "tenant-b")
	decodeJSON(t, rec, &cmdB)
	if len(cmdB.Commands) != 1 || cmdB.Commands[0].DeviceID != "b/d2" {
		t.Fatalf("tenant-b commands leaked: %+v", cmdB.Commands)
	}

	var cross struct {
		Commands []api.CommandView `json:"commands"`
	}
	rec = serveTenant(t, srv, http.MethodGet, "/api/commands?device=b/d2", "", a, "tenant-a")
	decodeJSON(t, rec, &cross)
	if len(cross.Commands) != 0 {
		t.Fatalf("tenant-a enumerated tenant-b commands: %+v", cross.Commands)
	}
}

// TestCrossTenantCommandReturns404 锁定跨租户命令下发与不存在同语义（404）。
func TestCrossTenantCommandReturns404(t *testing.T) {
	srv, _, b := setupTenantREST(t)
	rec := serveTenant(t, srv, http.MethodPost, "/api/devices/a/d1/commands", `{"cmd":"sync"}`, b, "tenant-b")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant command = %d, want 404", rec.Code)
	}
	var body struct {
		Error string `json:"error"`
	}
	decodeJSON(t, rec, &body)
	if body.Error == "" {
		t.Fatalf("404 body should be {error:...}: %+v", body)
	}
}

// TestStoreNilTenantPath 锁定 Store=nil 时 API 读路径与命令路径不 panic。
func TestStoreNilTenantPath(t *testing.T) {
	srv := New(Config{Version: "test"})
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()
	defer srv.CloseAll()

	for _, path := range []string{"/api/devices", "/api/events", "/api/commands", "/api/stats", "/api/edges"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("nil store GET %s = %d, want 200", path, resp.StatusCode)
		}
	}
	resp, err := http.Post(ts.URL+"/api/devices/e1/d1/commands", "application/json", strings.NewReader(`{"cmd":"sync"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("nil store unknown command = %d, want 404", resp.StatusCode)
	}
}
