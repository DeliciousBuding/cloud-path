package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/plugincatalog"
	"github.com/DeliciousBuding/cloud-path/internal/server/storeport"
)

// invalidatePluginCache 让租户面下次访问时重新从 PluginStore 加载
// （测试直接往内存 store 里塞投影时用；生产路径由写入/上报自动维护缓存）。
func invalidatePluginCache(t *testing.T, srv *Server, tenantID int64) {
	t.Helper()
	srv.plugin.mu.Lock()
	if tp := srv.plugin.tenants[tenantID]; tp != nil {
		tp.loaded = false
	}
	srv.plugin.mu.Unlock()
}

// seedInstallations 注入 Edge 上报的安装物投影（真实上报路径的等价落库结果）。
func seedInstallations(t *testing.T, srv *Server, mem *storeport.Memory, tenantID int64, edgeID string, rows []api.PluginInstallationStatusData) {
	t.Helper()
	if err := mem.UpsertPluginInstallations(tenantID, edgeID, rows); err != nil {
		t.Fatal(err)
	}
	invalidatePluginCache(t, srv, tenantID)
}

// seedObservations 注入 Edge 上报的实例实际态投影。
func seedObservations(t *testing.T, srv *Server, mem *storeport.Memory, tenantID int64, edgeID string, rows []api.PluginObservedInstanceData, reportedAt int64) {
	t.Helper()
	if err := mem.UpsertPluginObservations(tenantID, edgeID, rows, reportedAt); err != nil {
		t.Fatal(err)
	}
	invalidatePluginCache(t, srv, tenantID)
}

// TestPluginsAPIFromProjection 锁定 /api/plugins 读的是 Edge 上报的真实安装物投影：
// 租户隔离、单个查询、未知 404、未接线空列表，且响应不含本机绝对路径。
func TestPluginsAPIFromProjection(t *testing.T) {
	srv, _, _, mem, a, b := setupPluginPlane(t)
	seedInstallations(t, srv, mem, a, "e1", []api.PluginInstallationStatusData{{
		PluginID: "io.github.acme.driver", Version: "0.1.0", Kind: "Driver", Protocol: 1,
		Digest: "sha256:abc123", TrustMode: "verified", Verified: true,
		VerifiedPublisher: "acme",
		Permissions:       api.PluginPermissionsData{Secrets: []string{"api_token"}, Hardware: []string{"serial"}},
		Contributions: api.PluginContributionsData{Drivers: []api.PluginDriverContributionData{
			{ID: "demo", Title: "STC-B Driver", Discovery: "manual"},
		}},
		Capabilities: []string{"cloudpath.dev/capability/clock@1"},
	}})
	seedInstallations(t, srv, mem, b, "e9", []api.PluginInstallationStatusData{{
		PluginID: "io.github.other.app", Version: "1.2.3", Kind: "Application", Protocol: 1,
	}})

	rec := servePlugin(t, srv, http.MethodGet, "/api/plugins", "", a, "tenant-a", string(api.RoleViewer))
	if rec.Code != http.StatusOK {
		t.Fatalf("list plugins = %d", rec.Code)
	}
	var list struct {
		Plugins []plugincatalog.PluginView `json:"plugins"`
	}
	decodeJSON(t, rec, &list)
	if len(list.Plugins) != 1 || list.Plugins[0].ID != "io.github.acme.driver" {
		t.Fatalf("tenant-a 插件列表错误/泄漏: %+v", list.Plugins)
	}
	if list.Plugins[0].Kind != "Driver" || !list.Plugins[0].Verified ||
		len(list.Plugins[0].Permissions.Secrets) != 1 || list.Plugins[0].Permissions.Secrets[0] != "api_token" {
		t.Fatalf("插件视图字段错误: %+v", list.Plugins[0])
	}
	if len(list.Plugins[0].Contributes.Drivers) != 1 || list.Plugins[0].Contributes.Drivers[0].ID != "demo" {
		t.Fatalf("贡献字段错误: %+v", list.Plugins[0].Contributes)
	}
	raw := rec.Body.String()
	for _, banned := range []string{"C:\\", "/home/", "/Users/", "/tmp/"} {
		if strings.Contains(raw, banned) {
			t.Fatalf("响应泄漏本机绝对路径 %q: %s", banned, raw)
		}
	}

	rec = servePlugin(t, srv, http.MethodGet, "/api/plugins/io.github.acme.driver", "", a, "tenant-a", string(api.RoleViewer))
	if rec.Code != http.StatusOK {
		t.Fatalf("get plugin = %d", rec.Code)
	}
	// tenant-b 看不到 tenant-a 的安装物，也拿不到单个查询。
	rec = servePlugin(t, srv, http.MethodGet, "/api/plugins", "", b, "tenant-b", string(api.RoleViewer))
	decodeJSON(t, rec, &list)
	if len(list.Plugins) != 1 || list.Plugins[0].ID != "io.github.other.app" {
		t.Fatalf("tenant-b 插件列表错误/泄漏: %+v", list.Plugins)
	}
	if rec := servePlugin(t, srv, http.MethodGet, "/api/plugins/io.github.acme.driver", "",
		b, "tenant-b", string(api.RoleViewer)); rec.Code != http.StatusNotFound {
		t.Fatalf("跨租户 get plugin = %d, want 404", rec.Code)
	}
}

// TestPluginInstancesDesiredObservedSeparation 锁定不变量 5：
// desired 与 observed 分别呈现；未上报时 Observed 必须为 null；
// 上报后 Drift/Stale 由真实 revision 与上报时间计算，绝不因 desired enabled 虚报健康。
func TestPluginInstancesDesiredObservedSeparation(t *testing.T) {
	srv, _, _, mem, a, _ := setupPluginPlane(t)
	if rec := servePlugin(t, srv, http.MethodPost, "/api/plugin-instances",
		`{"edge_id":"e1","instance_id":"box1","plugin_id":"p1","version":"1.0.0","enabled":true}`,
		a, "tenant-a", string(api.RoleOperator)); rec.Code != http.StatusOK {
		t.Fatalf("create = %d body=%s", rec.Code, rec.Body.String())
	}

	// 1) 未上报：desired enabled=true，但 observed 必须缺席（不得渲染成健康）。
	list := listInstances(t, srv, a, "tenant-a", string(api.RoleViewer))
	if len(list.Instances) != 1 {
		t.Fatalf("实例数 = %d", len(list.Instances))
	}
	got := list.Instances[0]
	if !got.Desired.Enabled {
		t.Fatalf("desired enabled 丢失: %+v", got.Desired)
	}
	if got.HasObserved || got.Observed != nil {
		t.Fatalf("未上报却给出 observed: %+v", got)
	}
	if !got.Stale || !got.Drift {
		t.Fatalf("未上报且未 ack 时 stale/drift 应为真: %+v", got)
	}
	// 冻结 DTO 用 `json:"observed,omitempty"`：nil 指针在 JSON 里表现为字段缺席
	// （与 null 同义），关键是**绝不出现对象**，UI 据 has_observed 显示「未上报」。
	raw, _ := json.Marshal(list)
	if strings.Contains(string(raw), `"observed":{`) {
		t.Fatalf("HasObserved=false 却给出 observed 对象: %s", raw)
	}
	if !strings.Contains(string(raw), `"has_observed":false`) {
		t.Fatalf("has_observed 未如实呈现: %s", raw)
	}
	if got.TenantID != a {
		t.Fatalf("tenant_id = %d, want %d", got.TenantID, a)
	}

	// 2) Edge 上报后：observed 真实呈现，state/health 来自上报而非 desired。
	seedObservations(t, srv, mem, a, "e1", []api.PluginObservedInstanceData{{
		InstanceID: "box1", PluginID: "p1", Version: "1.0.0", HostOnline: true,
		State: "CRASHED", Health: "UNKNOWN", Detail: "plugin exited", RestartCount: 3,
	}}, 1)
	list = listInstances(t, srv, a, "tenant-a", string(api.RoleViewer))
	got = list.Instances[0]
	if !got.HasObserved || got.Observed == nil {
		t.Fatalf("上报后仍无 observed: %+v", got)
	}
	if got.Observed.State != "CRASHED" || got.Observed.RestartCount != 3 {
		t.Fatalf("observed 字段错误: %+v", got.Observed)
	}
	if got.Desired.Enabled && got.Observed.Health == "HEALTHY" {
		t.Fatal("desired enabled 被渲染成 observed healthy")
	}
	// 3) Edge 上报了一个 Server 期望态里没有的实例：真实不一致必须暴露而不是隐藏。
	seedObservations(t, srv, mem, a, "e2", []api.PluginObservedInstanceData{{
		InstanceID: "ghost", PluginID: "p2", Version: "9.9.9", HostOnline: true,
		State: "HEALTHY", Health: "HEALTHY",
	}}, 1)
	list = listInstances(t, srv, a, "tenant-a", string(api.RoleViewer))
	found := false
	for _, in := range list.Instances {
		if in.ID == "ghost" {
			found = true
			if !in.Drift || !in.HasObserved || in.Desired.PluginID != "" {
				t.Fatalf("ghost 实例视图错误: %+v", in)
			}
		}
	}
	if !found {
		t.Fatalf("Edge 上报的未知实例被隐藏: %+v", list.Instances)
	}
}

// TestPluginInstanceDetailSanitized 锁定暗卷 10：Edge 上报的错误摘要里的本机绝对路径
// 与疑似凭据必须在 API 响应前被脱敏。
func TestPluginInstanceDetailSanitized(t *testing.T) {
	srv, _, _, mem, a, _ := setupPluginPlane(t)
	if rec := servePlugin(t, srv, http.MethodPost, "/api/plugin-instances",
		`{"edge_id":"e1","instance_id":"box1","plugin_id":"p1","version":"1.0.0"}`,
		a, "tenant-a", string(api.RoleOperator)); rec.Code != http.StatusOK {
		t.Fatalf("create = %d", rec.Code)
	}
	seedObservations(t, srv, mem, a, "e1", []api.PluginObservedInstanceData{{
		InstanceID: "box1", PluginID: "p1", Version: "1.0.0", HostOnline: true,
		State:  "CRASHED",
		Health: "UNKNOWN",
		Detail: "open C:" + `\Users\ding\secrets\api.txt failed; token=hunter2-secret`, // 红队字面量拆分，防 public_audit 误报
	}}, 1)
	rec := servePlugin(t, srv, http.MethodGet, "/api/plugin-instances/box1", "",
		a, "tenant-a", string(api.RoleViewer))
	if rec.Code != http.StatusOK {
		t.Fatalf("get instance = %d", rec.Code)
	}
	raw := rec.Body.String()
	for _, banned := range []string{`C:\Users`, "hunter2-secret"} {
		if strings.Contains(raw, banned) {
			t.Fatalf("响应泄漏 %q: %s", banned, raw)
		}
	}
	var view api.PluginInstanceView
	decodeJSON(t, rec, &view)
	if view.Observed == nil || !strings.Contains(view.Observed.Detail, "[path]") ||
		!strings.Contains(view.Observed.Detail, "[REDACTED]") {
		t.Fatalf("detail 未按预期脱敏: %+v", view.Observed)
	}
	if view.ID != "box1" || view.EdgeID != "e1" {
		t.Fatalf("实例视图身份错误: %+v", view)
	}
}

// TestPluginCatalogUnwiredReturnsEmpty 锁定未接线时读面真实为空而不是崩溃/样例数据。
func TestPluginCatalogUnwiredReturnsEmpty(t *testing.T) {
	srv := New(Config{Version: "test"})
	t.Cleanup(func() { srv.CloseAll() })
	rec := servePlugin(t, srv, http.MethodGet, "/api/plugins", "", 1, "tenant-a", string(api.RoleViewer))
	if rec.Code != http.StatusOK {
		t.Fatalf("nil plane list plugins = %d", rec.Code)
	}
	var list struct {
		Plugins []plugincatalog.PluginView `json:"plugins"`
	}
	decodeJSON(t, rec, &list)
	if len(list.Plugins) != 0 {
		t.Fatalf("未接线却有插件: %+v", list.Plugins)
	}
	if got := listInstances(t, srv, 1, "tenant-a", string(api.RoleViewer)); len(got.Instances) != 0 {
		t.Fatalf("未接线却有实例: %+v", got.Instances)
	}
	if rec := servePlugin(t, srv, http.MethodGet, "/api/plugin-instances/x", "",
		1, "tenant-a", string(api.RoleViewer)); rec.Code != http.StatusNotFound {
		t.Fatalf("未接线单实例 = %d, want 404", rec.Code)
	}
	_ = httptest.ResponseRecorder{}
}
