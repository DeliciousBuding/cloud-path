package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/model"
)

const (
	capTenantA      = "io.example.tenant/capability/a@1"
	capTenantAOther = "io.example.tenant/capability/a-other@1"
	capTenantB      = "io.example.tenant/capability/b@1"
	capTenantShared = "io.example.tenant/capability/shared@1"
)

func tenantCapability(id, title string) model.Capability {
	return model.Capability{
		APIVersion: model.CapabilityAPIVersion,
		Kind:       model.CapabilityKind,
		Metadata: model.CapabilityMetadata{
			ID: id, Version: 1, Title: title,
		},
		Spec: model.CapabilitySpec{
			Properties: map[string]model.Property{
				"value": {Type: "number", Unit: "Cel", Access: model.PropertyRead},
			},
		},
	}
}

func reportTenantCapabilities(t *testing.T, ws *websocket.Conn, caps ...model.Capability) {
	t.Helper()
	writeEnv(t, ws, api.Envelope{
		V: api.Version, Type: api.MsgCapabilities, Ts: time.Now().Unix(),
		Data: rawData(t, api.CapabilitiesData{Sources: []api.CapabilitySource{{
			Source: "tenant-driver", Capabilities: caps,
		}}}),
	})
}

func fetchTenantCatalogList(t *testing.T, ts *httptest.Server, token string) []model.Capability {
	t.Helper()
	resp := doJSON(t, http.MethodGet, ts.URL+"/api/capabilities", "", bearerJSON(token), nil)
	defer resp.Body.Close()
	raw := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/capabilities = %d body=%s", resp.StatusCode, raw)
	}
	var out struct {
		Capabilities []model.Capability `json:"capabilities"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("decode capabilities: %v body=%s", err, raw)
	}
	return out.Capabilities
}

func fetchTenantCatalog(t *testing.T, ts *httptest.Server, token string) map[string]model.Capability {
	t.Helper()
	list := fetchTenantCatalogList(t, ts, token)
	byID := make(map[string]model.Capability, len(list))
	for _, c := range list {
		byID[c.Metadata.ID] = c
	}
	return byID
}

func waitTenantCatalog(t *testing.T, ts *httptest.Server, token string,
	cond func(map[string]model.Capability) bool, why string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if cond(fetchTenantCatalog(t, ts, token)) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s（超时 20s）", why)
}

type tenantDescriptorResponse struct {
	Descriptors  []model.Descriptor `json:"descriptors"`
	Capabilities []model.Capability `json:"capabilities"`
}

func fetchTenantDescriptors(t *testing.T, ts *httptest.Server, token string) tenantDescriptorResponse {
	t.Helper()
	resp := doJSON(t, http.MethodGet, ts.URL+"/api/descriptors", "", bearerJSON(token), nil)
	defer resp.Body.Close()
	raw := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/descriptors = %d body=%s", resp.StatusCode, raw)
	}
	var out tenantDescriptorResponse
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("decode descriptors: %v body=%s", err, raw)
	}
	return out
}

func waitTenantDescriptors(t *testing.T, ts *httptest.Server, token, deviceID string) tenantDescriptorResponse {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		out := fetchTenantDescriptors(t, ts, token)
		if len(out.Descriptors) == 1 && out.Descriptors[0].DeviceID == deviceID {
			return out
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("租户未在 20s 内看到自己的 descriptor %q", deviceID)
	return tenantDescriptorResponse{}
}

func tenantDescriptor(deviceID, externalID string) model.Descriptor {
	return model.Descriptor{
		DeviceID: deviceID, ExternalID: externalID, Status: model.DeviceOnline,
		Entities: []model.Entity{{
			EntityID: "sensor", UniqueKey: "sensor", Category: model.EntitySensor,
			Capabilities: []string{"cloudpath.dev/capability/temperature@1"},
		}},
	}
}

func assertCapabilityAbsent(t *testing.T, catalog map[string]model.Capability, id, tenant string) {
	t.Helper()
	if _, ok := catalog[id]; ok {
		t.Fatalf("tenant %s 看到了其他租户的 capability %q", tenant, id)
	}
}

func assertCapabilityPresent(t *testing.T, catalog map[string]model.Capability, id, tenant string) {
	t.Helper()
	if _, ok := catalog[id]; !ok {
		t.Fatalf("tenant %s 缺少自己的 capability %q", tenant, id)
	}
}

func TestCapabilityAndDescriptorTenantIsolation(t *testing.T) {
	st, srv, ts, tenantA, tenantB := setupMultiEdge(t)
	edgeA := issueTenantToken(t, st, tenantA, `["edge"]`)
	edgeB := issueTenantToken(t, st, tenantB, `["edge"]`)
	readA := issueTenantToken(t, st, tenantA, `["read"]`)
	readB := issueTenantToken(t, st, tenantB, `["read"]`)

	wsA := dialEdgeHello(t, ts, "ea", edgeA,
		api.DeviceMeta{ID: "d1", Adapter: "demo"})
	wsB := dialEdgeHello(t, ts, "eb", edgeB,
		api.DeviceMeta{ID: "d1", Adapter: "demo"})
	defer wsA.CloseNow()
	defer wsB.CloseNow()
	waitEdgeLink(t, srv, "ea", tenantA)
	waitEdgeLink(t, srv, "eb", tenantB)

	// A 故意逆序上报，锁定同租户内仍按 capability ID 排序、去重。
	reportTenantCapabilities(t, wsA,
		tenantCapability(capTenantShared, "shared-by-A"),
		tenantCapability(capTenantAOther, "A-other"),
		tenantCapability(capTenantA, "A"),
		tenantCapability(capTenantA, "A-duplicate"),
	)
	reportTenantCapabilities(t, wsB,
		tenantCapability(capTenantShared, "shared-by-B"),
		tenantCapability(capTenantB, "B"),
	)
	reportDescriptor(t, wsA, "ea/d1", tenantDescriptor("ea/d1", "d1"))
	reportDescriptor(t, wsB, "eb/d1", tenantDescriptor("eb/d1", "d1"))

	waitTenantCatalog(t, ts, readA, func(catalog map[string]model.Capability) bool {
		return catalog[capTenantA].Metadata.Title == "A" &&
			catalog[capTenantAOther].Metadata.Title == "A-other" &&
			catalog[capTenantShared].Metadata.Title == "shared-by-A"
	}, "tenant-a capability 未生效")
	waitTenantCatalog(t, ts, readB, func(catalog map[string]model.Capability) bool {
		return catalog[capTenantB].Metadata.Title == "B" &&
			catalog[capTenantShared].Metadata.Title == "shared-by-B"
	}, "tenant-b capability 未生效")

	listA := fetchTenantCatalogList(t, ts, readA)
	var gotA []string
	knownA := map[string]bool{capTenantA: true, capTenantAOther: true, capTenantShared: true}
	for _, c := range listA {
		if knownA[c.Metadata.ID] {
			gotA = append(gotA, c.Metadata.ID)
		}
	}
	wantA := []string{capTenantAOther, capTenantA, capTenantShared}
	if len(gotA) != len(wantA) {
		t.Fatalf("tenant-a capability 去重/排序 = %v, want %v", gotA, wantA)
	}
	for i := range wantA {
		if gotA[i] != wantA[i] {
			t.Fatalf("tenant-a capability 去重/排序 = %v, want %v", gotA, wantA)
		}
	}

	catalogA := fetchTenantCatalog(t, ts, readA)
	assertCapabilityPresent(t, catalogA, capTenantA, "tenant-a")
	assertCapabilityPresent(t, catalogA, capTenantAOther, "tenant-a")
	assertCapabilityPresent(t, catalogA, capTenantShared, "tenant-a")
	assertCapabilityAbsent(t, catalogA, capTenantB, "tenant-a")
	if got := catalogA[capTenantShared].Metadata.Title; got != "shared-by-A" {
		t.Fatalf("tenant-a shared capability title = %q, want shared-by-A", got)
	}

	catalogB := fetchTenantCatalog(t, ts, readB)
	assertCapabilityPresent(t, catalogB, capTenantB, "tenant-b")
	assertCapabilityPresent(t, catalogB, capTenantShared, "tenant-b")
	assertCapabilityAbsent(t, catalogB, capTenantA, "tenant-b")
	assertCapabilityAbsent(t, catalogB, capTenantAOther, "tenant-b")
	if got := catalogB[capTenantShared].Metadata.Title; got != "shared-by-B" {
		t.Fatalf("tenant-b shared capability title = %q, want shared-by-B", got)
	}

	bulkA := waitTenantDescriptors(t, ts, readA, "ea/d1")
	if len(bulkA.Descriptors) != 1 || bulkA.Descriptors[0].DeviceID != "ea/d1" {
		t.Fatalf("tenant-a descriptors leaked: %+v", bulkA.Descriptors)
	}
	bulkCapA := make(map[string]model.Capability, len(bulkA.Capabilities))
	for _, c := range bulkA.Capabilities {
		bulkCapA[c.Metadata.ID] = c
	}
	assertCapabilityAbsent(t, bulkCapA, capTenantB, "tenant-a")

	bulkB := waitTenantDescriptors(t, ts, readB, "eb/d1")
	if len(bulkB.Descriptors) != 1 || bulkB.Descriptors[0].DeviceID != "eb/d1" {
		t.Fatalf("tenant-b descriptors leaked: %+v", bulkB.Descriptors)
	}
	bulkCapB := make(map[string]model.Capability, len(bulkB.Capabilities))
	for _, c := range bulkB.Capabilities {
		bulkCapB[c.Metadata.ID] = c
	}
	assertCapabilityAbsent(t, bulkCapB, capTenantA, "tenant-b")
	assertCapabilityAbsent(t, bulkCapB, capTenantAOther, "tenant-b")

	// 账号模式下未认证请求必须继续被拒绝，不能因 capability 过滤而放宽。
	for _, path := range []string{"/api/capabilities", "/api/descriptors"} {
		resp := doJSON(t, http.MethodGet, ts.URL+path, "", nil, nil)
		if resp.StatusCode != http.StatusUnauthorized {
			raw := readBody(t, resp)
			t.Fatalf("unauthenticated GET %s = %d body=%s, want 401", path, resp.StatusCode, raw)
		}
		resp.Body.Close()
	}
}
