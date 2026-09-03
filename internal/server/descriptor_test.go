package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	_ "github.com/DeliciousBuding/cloud-path/examples/stcb"
	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/auth"
	"github.com/DeliciousBuding/cloud-path/internal/model"
)

// TestCapabilitiesAPI 锁定 /api/capabilities 返回已注册适配器的 Capability catalog。
func TestCapabilitiesAPI(t *testing.T) {
	_, ts := setup(t)
	var resp struct {
		Capabilities []model.Capability `json:"capabilities"`
	}
	getJSON(t, ts.URL+"/api/capabilities", &resp)
	if len(resp.Capabilities) != 3 {
		t.Fatalf("capabilities = %d, want 3", len(resp.Capabilities))
	}
	ids := map[string]bool{}
	for _, c := range resp.Capabilities {
		if c.Metadata.ID == "" || c.Metadata.Version < 0 {
			t.Fatalf("bad capability metadata: %+v", c.Metadata)
		}
		ids[c.Metadata.ID] = true
	}
	for _, want := range []string{
		"cloudpath.dev/capability/clock@1",
		"cloudpath.dev/capability/alarm@1",
		"cloudpath.dev/capability/contact@1",
	} {
		if !ids[want] {
			t.Fatalf("missing capability %q in %v", want, ids)
		}
	}
}

// TestDescriptorsAPI 锁定批量端点：注册 2 台 stcb 设备后 /api/descriptors 返回其 Descriptor。
func TestDescriptorsAPI(t *testing.T) {
	_, ts := setup(t)
	registerEdge(t, ts, "e1",
		api.DeviceMeta{ID: "d1", Adapter: "stcb", Name: "节点1", Port: "COM9"},
		api.DeviceMeta{ID: "d2", Adapter: "stcb", Name: "节点2", Port: "COM10"},
	)

	var resp struct {
		Descriptors  []model.Descriptor `json:"descriptors"`
		Capabilities []model.Capability `json:"capabilities"`
	}
	getJSON(t, ts.URL+"/api/descriptors", &resp)
	if len(resp.Descriptors) != 2 {
		t.Fatalf("descriptors = %d, want 2", len(resp.Descriptors))
	}
	byDevice := map[string]model.Descriptor{}
	for _, d := range resp.Descriptors {
		byDevice[d.DeviceID] = d
	}
	d1, ok := byDevice["e1/d1"]
	if !ok {
		t.Fatalf("missing e1/d1 descriptor: %+v", byDevice)
	}
	if d1.ExternalID != "d1" || len(d1.Entities) != 5 {
		t.Fatalf("bad e1/d1 descriptor: %+v", d1)
	}
	if len(resp.Capabilities) != 3 {
		t.Fatalf("bulk capabilities = %d, want 3（随行 catalog）", len(resp.Capabilities))
	}
}

// TestDeviceDescriptorAPI 锁定单设备端点：返回 device_id 为稳定键 "<edge>/<dev>"。
func TestDeviceDescriptorAPI(t *testing.T) {
	_, ts := setup(t)
	registerEdge(t, ts, "e1", api.DeviceMeta{ID: "d1", Adapter: "stcb"})

	var resp struct {
		Descriptor   model.Descriptor   `json:"descriptor"`
		Capabilities []model.Capability `json:"capabilities"`
	}
	getJSON(t, ts.URL+"/api/devices/e1/d1/descriptor", &resp)
	if resp.Descriptor.DeviceID != "e1/d1" || resp.Descriptor.ExternalID != "d1" {
		t.Fatalf("descriptor identity = %q/%q", resp.Descriptor.DeviceID, resp.Descriptor.ExternalID)
	}
	if len(resp.Descriptor.Entities) != 5 {
		t.Fatalf("entities = %d, want 5", len(resp.Descriptor.Entities))
	}
}

// TestDeviceDescriptorNotFound 反向测试：未知设备的 descriptor 必须 404（统一 {"error":...}）。
func TestDeviceDescriptorNotFound(t *testing.T) {
	_, ts := setup(t)
	resp := doJSON(t, http.MethodGet, ts.URL+"/api/devices/e9/nope/descriptor", "", nil, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown descriptor = %d, want 404", resp.StatusCode)
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || body.Error == "" {
		t.Fatalf("404 body 应统一为 {error:...}: %+v err=%v", body, err)
	}
}

// TestDescriptorTenantIsolation 锁定租户隔离：REST Descriptor 只返回当前租户可见的设备，
// 跨租户的单设备 descriptor 与不存在同语义（404）。
func TestDescriptorTenantIsolation(t *testing.T) {
	srv := New(Config{Version: "test"})
	clockEntities := func() []model.Entity {
		return []model.Entity{{
			EntityID: "clock", UniqueKey: "clock", Category: model.EntitySensor,
			Capabilities: []string{"cloudpath.dev/capability/clock@1"},
		}}
	}
	srv.mu.Lock()
	srv.devices["a/d1"] = &api.DeviceView{ID: "a/d1", EdgeID: "a", Adapter: "stcb", Online: true, State: map[string]any{}}
	srv.devices["b/d2"] = &api.DeviceView{ID: "b/d2", EdgeID: "b", Adapter: "stcb", Online: true, State: map[string]any{}}
	srv.deviceTenants = map[string]string{"a/d1": "tenant-a", "b/d2": "tenant-b"}
	srv.descriptors = map[string]model.Descriptor{
		"a/d1": {DeviceID: "a/d1", ExternalID: "d1", Status: model.DeviceOnline, Entities: clockEntities()},
		"b/d2": {DeviceID: "b/d2", ExternalID: "d2", Status: model.DeviceOnline, Entities: clockEntities()},
	}
	srv.mu.Unlock()

	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()
	defer srv.CloseAll()

	// tenant-a 批量只能看到 a/d1
	req := httptest.NewRequest(http.MethodGet, "/api/descriptors", nil)
	req = req.WithContext(auth.WithPrincipal(req.Context(), &auth.Principal{TenantSlug: "tenant-a"}))
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("tenant-a bulk = %d", rec.Code)
	}
	var bulk struct {
		Descriptors []model.Descriptor `json:"descriptors"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&bulk); err != nil {
		t.Fatal(err)
	}
	if len(bulk.Descriptors) != 1 || bulk.Descriptors[0].DeviceID != "a/d1" {
		t.Fatalf("tenant-a bulk leaked cross-tenant: %+v", bulk.Descriptors)
	}

	// tenant-b 访问 a/d1 → 404（跨租户与不存在同语义）
	reqB := httptest.NewRequest(http.MethodGet, "/api/devices/a/d1/descriptor", nil)
	reqB = reqB.WithContext(auth.WithPrincipal(reqB.Context(), &auth.Principal{TenantSlug: "tenant-b"}))
	recB := httptest.NewRecorder()
	srv.Routes().ServeHTTP(recB, reqB)
	if recB.Code != http.StatusNotFound {
		t.Fatalf("tenant-b access tenant-a descriptor = %d, want 404", recB.Code)
	}

	// tenant-a 访问自己的 descriptor → 200
	reqA := httptest.NewRequest(http.MethodGet, "/api/devices/a/d1/descriptor", nil)
	reqA = reqA.WithContext(auth.WithPrincipal(reqA.Context(), &auth.Principal{TenantSlug: "tenant-a"}))
	recA := httptest.NewRecorder()
	srv.Routes().ServeHTTP(recA, reqA)
	if recA.Code != http.StatusOK {
		t.Fatalf("tenant-a own descriptor = %d, want 200", recA.Code)
	}
}
