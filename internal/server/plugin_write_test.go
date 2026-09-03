package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/auth"
	"github.com/DeliciousBuding/cloud-path/internal/server/storeport"
	"github.com/DeliciousBuding/cloud-path/internal/store"
)

// setupPluginPlane 构造插件控制面测试底座：真实 SQLite（租户/审计/事件/命令）
// + storeport 内存实现（插件期望态/投影，Store v7 接线前的契约替身）。
// 返回 tenant-a / tenant-b 的真实租户 id。
func setupPluginPlane(t *testing.T) (*Server, *httptest.Server, *store.Store, *storeport.Memory, int64, int64) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "plugin.db"))
	if err != nil {
		t.Fatal(err)
	}
	ensure := func(slug string) int64 {
		t.Helper()
		if row, err := st.GetTenantBySlug(slug); err == nil {
			return row.ID
		}
		id, err := st.CreateTenant(slug, slug)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	a, b := ensure("tenant-a"), ensure("tenant-b")
	mem := storeport.NewMemory()
	srv := New(Config{Store: st, Version: "test", PluginStore: mem})
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(func() { ts.Close(); srv.CloseAll(); time.Sleep(50 * time.Millisecond) })
	t.Cleanup(func() { st.Close() })
	return srv, ts, st, mem, a, b
}

// servePlugin 以指定租户/角色身份调用插件端点（注入 principal，走完整 chi 路由与 RBAC）。
func servePlugin(t *testing.T, srv *Server, method, target, body string, tenantID int64, tenantSlug, role string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *strings.Reader
	if body == "" {
		rdr = strings.NewReader("")
	} else {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, rdr)
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "127.0.0.1:1234"
	req = req.WithContext(auth.WithPrincipal(req.Context(), &auth.Principal{
		UserID: 7, Username: "u-" + role, Name: role, Role: role,
		TenantID: tenantID, TenantSlug: tenantSlug,
	}))
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	return rec
}

// pluginBody 是写响应的通用解码形状。
type pluginBody struct {
	ID        string                 `json:"id"`
	Revision  uint64                 `json:"revision"`
	RequestID string                 `json:"request_id"`
	Instance  api.PluginInstanceView `json:"instance"`
	Code      string                 `json:"code"`
	Error     string                 `json:"error"`
	Message   string                 `json:"message"`
}

func decodePluginBody(t *testing.T, rec *httptest.ResponseRecorder) pluginBody {
	t.Helper()
	var out pluginBody
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return out
}

// listInstances 读 GET /api/plugin-instances 的契约载荷。
func listInstances(t *testing.T, srv *Server, tenantID int64, slug, role string) api.PluginInstanceListResponse {
	t.Helper()
	rec := servePlugin(t, srv, http.MethodGet, "/api/plugin-instances", "", tenantID, slug, role)
	if rec.Code != http.StatusOK {
		t.Fatalf("list instances = %d body=%s", rec.Code, rec.Body.String())
	}
	var out api.PluginInstanceListResponse
	decodeJSON(t, rec, &out)
	return out
}

// listInstancesHTTP 用租户令牌走真实 HTTP 读 GET /api/plugin-instances
// （账号模式服务器不能用注入 principal 的 servePlugin）。
func listInstancesHTTP(t *testing.T, ts *httptest.Server, token string) api.PluginInstanceListResponse {
	t.Helper()
	resp := doJSON(t, http.MethodGet, ts.URL+"/api/plugin-instances", "", bearerJSON(token), nil)
	raw := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list instances = %d body=%s", resp.StatusCode, raw)
	}
	var out api.PluginInstanceListResponse
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("decode instances: %v (%s)", err, raw)
	}
	return out
}

// auditActions 返回某租户已落库的审计动作+结果（断言「失败必须被真实记录」用）。
func auditActions(t *testing.T, st *store.Store, tenantID int64) []string {
	t.Helper()
	rows, err := st.ListAuditEvents(tenantID, 0, "", 200)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Action+":"+r.Outcome)
	}
	return out
}

func hasAudit(actions []string, want string) bool {
	for _, a := range actions {
		if a == want {
			return true
		}
	}
	return false
}

// TestPluginInstanceCreateAndRead 锁定创建 → 读面：desired 真实落库、revision 从 1 起、
// 未上报时 HasObserved=false 且 Observed 必须为 null（绝不把期望渲染成观测）。
func TestPluginInstanceCreateAndRead(t *testing.T) {
	srv, _, _, mem, a, _ := setupPluginPlane(t)
	rec := servePlugin(t, srv, http.MethodPost, "/api/plugin-instances",
		`{"edge_id":"e1","instance_id":"box1","plugin_id":"io.github.acme.driver","version":"0.1.0","config":{"interval":"30"}}`,
		a, "tenant-a", string(api.RoleOperator))
	if rec.Code != http.StatusOK {
		t.Fatalf("create = %d body=%s", rec.Code, rec.Body.String())
	}
	body := decodePluginBody(t, rec)
	if body.Revision != 1 || body.ID != "box1" || body.RequestID == "" {
		t.Fatalf("write response = %+v", body)
	}
	if body.Instance.HasObserved || body.Instance.Observed != nil {
		t.Fatalf("未上报却给出 observed: %+v", body.Instance)
	}
	if !body.Instance.Drift || body.Instance.DesiredRevision != 1 || body.Instance.AppliedRevision != 0 {
		t.Fatalf("drift/revision 计算错误: %+v", body.Instance)
	}
	if body.Instance.EdgeOnline || !body.Instance.Stale {
		t.Fatalf("edge 离线时 stale 必须为真: %+v", body.Instance)
	}
	if !body.Instance.Desired.Enabled || body.Instance.Desired.Isolation != "shared" {
		t.Fatalf("desired 字段错误: %+v", body.Instance.Desired)
	}
	if rev, err := mem.PluginDesiredRevision(a, "e1"); err != nil || rev != 1 {
		t.Fatalf("desired revision = %d err=%v, want 1", rev, err)
	}
	list := listInstances(t, srv, a, "tenant-a", string(api.RoleViewer))
	if len(list.Instances) != 1 || list.Instances[0].ID != "box1" {
		t.Fatalf("读面实例 = %+v", list.Instances)
	}
	if list.Instances[0].Desired.Config == nil {
		t.Fatalf("config 未回读（只应缺 secret 明文，不缺普通配置）: %+v", list.Instances[0].Desired)
	}
}

// TestPluginInstanceRBAC 锁定角色差异：viewer 只读；operator 可写；
// purge 删数据要求 admin（契约的 DeleteRequest 没有 confirm 字段）。
func TestPluginInstanceRBAC(t *testing.T) {
	srv, _, _, _, a, _ := setupPluginPlane(t)
	create := `{"edge_id":"e1","instance_id":"box1","plugin_id":"p1","version":"1.0.0"}`
	if rec := servePlugin(t, srv, http.MethodPost, "/api/plugin-instances", create,
		a, "tenant-a", string(api.RoleViewer)); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer 创建 = %d, want 403", rec.Code)
	}
	if rec := servePlugin(t, srv, http.MethodPost, "/api/plugin-instances", create,
		a, "tenant-a", string(api.RoleOperator)); rec.Code != http.StatusOK {
		t.Fatalf("operator 创建 = %d body=%s", rec.Code, rec.Body.String())
	}
	if rec := servePlugin(t, srv, http.MethodPatch, "/api/plugin-instances/box1",
		`{"enabled":false}`, a, "tenant-a", string(api.RoleViewer)); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer 修改 = %d, want 403", rec.Code)
	}
	if rec := servePlugin(t, srv, http.MethodPatch, "/api/plugin-instances/box1",
		`{"enabled":false}`, a, "tenant-a", string(api.RoleOperator)); rec.Code != http.StatusOK {
		t.Fatalf("operator 修改 = %d body=%s", rec.Code, rec.Body.String())
	}
	// purge 需要 admin：operator 显式 purge 被拒且期望态仍在。
	rec := servePlugin(t, srv, http.MethodDelete, "/api/plugin-instances/box1",
		`{"purge":true}`, a, "tenant-a", string(api.RoleOperator))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("operator purge = %d, want 403", rec.Code)
	}
	if got := decodePluginBody(t, rec); got.Code != api.PluginErrPermissionConfirm {
		t.Fatalf("purge 拒绝码 = %q, want %q", got.Code, api.PluginErrPermissionConfirm)
	}
	if rec := servePlugin(t, srv, http.MethodDelete, "/api/plugin-instances/box1",
		`{"purge":true}`, a, "tenant-a", string(api.RoleAdmin)); rec.Code != http.StatusOK {
		t.Fatalf("admin purge = %d body=%s", rec.Code, rec.Body.String())
	}
	if list := listInstances(t, srv, a, "tenant-a", string(api.RoleViewer)); len(list.Instances) != 0 {
		t.Fatalf("删除后仍有实例: %+v", list.Instances)
	}
}

// TestPluginInstanceQuotaDoesNotAdvanceRevision 锁定暗卷 6：配额超限不得增加 revision、
// 不得留「成功」审计，必须记失败审计，并返回稳定错误码。
func TestPluginInstanceQuotaDoesNotAdvanceRevision(t *testing.T) {
	srv, _, st, mem, a, _ := setupPluginPlane(t)
	if err := mem.SetTenantPolicy(a, storeport.TenantPolicyRow{TenantID: a, QuotaPluginInstances: 1}); err != nil {
		t.Fatal(err)
	}
	first := servePlugin(t, srv, http.MethodPost, "/api/plugin-instances",
		`{"edge_id":"e1","instance_id":"box1","plugin_id":"p1","version":"1.0.0"}`,
		a, "tenant-a", string(api.RoleOperator))
	if first.Code != http.StatusOK {
		t.Fatalf("首个实例创建 = %d", first.Code)
	}
	revBefore, err := mem.PluginDesiredRevision(a, "e1")
	if err != nil || revBefore != 1 {
		t.Fatalf("revision = %d err=%v, want 1", revBefore, err)
	}
	rec := servePlugin(t, srv, http.MethodPost, "/api/plugin-instances",
		`{"edge_id":"e1","instance_id":"box2","plugin_id":"p1","version":"1.0.0"}`,
		a, "tenant-a", string(api.RoleOperator))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("超配额 = %d, want 429 body=%s", rec.Code, rec.Body.String())
	}
	if got := decodePluginBody(t, rec); got.Code != api.PluginErrQuota {
		t.Fatalf("错误码 = %q, want %q", got.Code, api.PluginErrQuota)
	}
	revAfter, err := mem.PluginDesiredRevision(a, "e1")
	if err != nil || revAfter != revBefore {
		t.Fatalf("配额拒绝后 revision = %d, want %d（不得增加）", revAfter, revBefore)
	}
	if rows, err := mem.ListPluginInstancesTenant(a); err != nil || len(rows) != 1 {
		t.Fatalf("配额拒绝后实例数 = %d err=%v, want 1", len(rows), err)
	}
	actions := auditActions(t, st, a)
	if !hasAudit(actions, actionPluginQuota+":failure") {
		t.Fatalf("配额拒绝未记失败审计: %v", actions)
	}
	if hasAudit(actions, actionPluginCreate+":failure") {
		// 失败审计必须有（业务动作），但绝不允许出现第二次成功审计。
		t.Logf("create failure audit recorded as well: %v", actions)
	}
	n := 0
	for _, act := range actions {
		if act == actionPluginCreate+":success" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("create success 审计条数 = %d, want 1（配额拒绝不得留成功审计）: %v", n, actions)
	}
}

// TestPluginInstancePermissionEscalationRequiresConfirm 锁定：绑定 secret / 削弱隔离
// 属于权限扩大，未显式确认不得生成新 desired revision。
func TestPluginInstancePermissionEscalationRequiresConfirm(t *testing.T) {
	srv, _, _, mem, a, _ := setupPluginPlane(t)
	// 1) operator 绑定 secret 但未确认 → 403，且不产生任何 revision。
	rec := servePlugin(t, srv, http.MethodPost, "/api/plugin-instances",
		`{"edge_id":"e1","instance_id":"box1","plugin_id":"p1","version":"1.0.0","config":{"api_token":"secret://api_token"}}`,
		a, "tenant-a", string(api.RoleOperator))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("未确认的 secret 绑定 = %d, want 403 body=%s", rec.Code, rec.Body.String())
	}
	if got := decodePluginBody(t, rec); got.Code != api.PluginErrPermissionConfirm {
		t.Fatalf("错误码 = %q, want %q", got.Code, api.PluginErrPermissionConfirm)
	}
	if rev, err := mem.PluginDesiredRevision(a, "e1"); err != nil || rev != 0 {
		t.Fatalf("未确认却产生 revision = %d err=%v, want 0", rev, err)
	}
	if rows, err := mem.ListPluginInstancesTenant(a); err != nil || len(rows) != 0 {
		t.Fatalf("未确认却写入期望态: %+v err=%v", rows, err)
	}
	// 2) 显式确认后成功，且期望态只含 handle。
	rec = servePlugin(t, srv, http.MethodPost, "/api/plugin-instances",
		`{"edge_id":"e1","instance_id":"box1","plugin_id":"p1","version":"1.0.0","config":{"api_token":"secret://api_token"},"confirm_permissions":true}`,
		a, "tenant-a", string(api.RoleOperator))
	if rec.Code != http.StatusOK {
		t.Fatalf("确认后创建 = %d body=%s", rec.Code, rec.Body.String())
	}
	raw := rec.Body.String()
	body := decodePluginBody(t, rec)
	if len(body.Instance.Desired.SecretRefs) != 1 || body.Instance.Desired.SecretRefs[0] != "api_token" {
		t.Fatalf("secret_refs = %+v, want [api_token]", body.Instance.Desired.SecretRefs)
	}
	if !strings.Contains(raw, "secret://api_token") {
		t.Fatalf("期望态应以 handle 形式出现: %s", raw)
	}
	if strings.Contains(raw, "hunter2") || strings.Contains(raw, "sk-") {
		t.Fatalf("响应泄漏疑似明文 secret: %s", raw)
	}
	// 3) admin 无需 confirm 即可绑定 secret。
	if rec := servePlugin(t, srv, http.MethodPost, "/api/plugin-instances",
		`{"edge_id":"e2","instance_id":"box2","plugin_id":"p1","version":"1.0.0","secret_refs":["api_token"]}`,
		a, "tenant-a", string(api.RoleAdmin)); rec.Code != http.StatusOK {
		t.Fatalf("admin 绑定 secret = %d body=%s", rec.Code, rec.Body.String())
	}
	// 4) 隔离降级（per-instance → shared）需确认。
	if rec := servePlugin(t, srv, http.MethodPatch, "/api/plugin-instances/box1",
		`{"isolation":"per-instance"}`, a, "tenant-a", string(api.RoleAdmin)); rec.Code != http.StatusOK {
		t.Fatalf("升级到 per-instance = %d body=%s", rec.Code, rec.Body.String())
	}
	rec = servePlugin(t, srv, http.MethodPatch, "/api/plugin-instances/box1",
		`{"isolation":"shared"}`, a, "tenant-a", string(api.RoleOperator))
	if rec.Code != http.StatusForbidden || decodePluginBody(t, rec).Code != api.PluginErrPermissionConfirm {
		t.Fatalf("隔离降级未确认 = %d body=%s", rec.Code, rec.Body.String())
	}
	if rec := servePlugin(t, srv, http.MethodPatch, "/api/plugin-instances/box1",
		`{"isolation":"shared","confirm_permissions":true}`, a, "tenant-a", string(api.RoleOperator)); rec.Code != http.StatusOK {
		t.Fatalf("隔离降级已确认 = %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestPluginInstanceRejectsPlaintextSecret 锁定 secret 边界：明文一律拒绝，
// 且拒绝响应、审计、存储里都不得出现该明文。
func TestPluginInstanceRejectsPlaintextSecret(t *testing.T) {
	srv, _, st, mem, a, _ := setupPluginPlane(t)
	const plaintext = "sk-live-SUPERSECRETVALUE"
	rec := servePlugin(t, srv, http.MethodPost, "/api/plugin-instances",
		`{"edge_id":"e1","instance_id":"box1","plugin_id":"p1","version":"1.0.0","config":{"api_token":"`+plaintext+`"}}`,
		a, "tenant-a", string(api.RoleAdmin))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("明文 secret = %d, want 403 body=%s", rec.Code, rec.Body.String())
	}
	if got := decodePluginBody(t, rec); got.Code != api.PluginErrSecretForbidden {
		t.Fatalf("错误码 = %q, want %q", got.Code, api.PluginErrSecretForbidden)
	}
	if strings.Contains(rec.Body.String(), plaintext) {
		t.Fatalf("响应回显了明文 secret: %s", rec.Body.String())
	}
	rows, err := mem.ListPluginInstancesTenant(a)
	if err != nil || len(rows) != 0 {
		t.Fatalf("明文 secret 被写入存储: %+v err=%v", rows, err)
	}
	for _, ev := range auditActions(t, st, a) {
		if strings.Contains(ev, plaintext) {
			t.Fatalf("审计泄漏明文: %v", ev)
		}
	}
	auditRows, err := st.ListAuditEvents(a, 0, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range auditRows {
		if strings.Contains(r.MetadataJSON, plaintext) {
			t.Fatalf("审计 metadata 泄漏明文: %s", r.MetadataJSON)
		}
	}
	// 内联 URL 凭据同样拒绝。
	if rec := servePlugin(t, srv, http.MethodPost, "/api/plugin-instances",
		`{"edge_id":"e1","instance_id":"box2","plugin_id":"p1","version":"1.0.0","config":{"endpoint":"https://user:`+plaintext+`@example.com"}}`,
		a, "tenant-a", string(api.RoleAdmin)); rec.Code != http.StatusForbidden {
		t.Fatalf("内联 URL 凭据 = %d, want 403", rec.Code)
	}
}

// TestPluginInstanceSecretMustBeDeclared 锁定双重授权的服务端一半：
// 已有安装物投影时，handle 名必须在 manifest permissions.secrets 中声明。
func TestPluginInstanceSecretMustBeDeclared(t *testing.T) {
	srv, _, _, mem, a, _ := setupPluginPlane(t)
	if err := mem.UpsertPluginInstallations(a, "e1", []api.PluginInstallationStatusData{{
		PluginID: "p1", Version: "1.0.0", Kind: "Driver", Protocol: 1,
		Permissions: api.PluginPermissionsData{Secrets: []string{"other_name"}},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := mem.CreatePluginInstance(storeport.PluginInstanceRow{
		TenantID: a, EdgeID: "e1", InstanceID: "seed", PluginID: "p0", Version: "1.0.0",
		Enabled: true, Isolation: "shared", ConfigJSON: "{}", SecretRefs: "[]",
	}); err != nil {
		t.Fatal(err)
	}
	rec := servePlugin(t, srv, http.MethodPost, "/api/plugin-instances",
		`{"edge_id":"e1","instance_id":"box1","plugin_id":"p1","version":"1.0.0","secret_refs":["api_token"],"confirm_permissions":true}`,
		a, "tenant-a", string(api.RoleAdmin))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("未声明的 secret = %d, want 403 body=%s", rec.Code, rec.Body.String())
	}
	if got := decodePluginBody(t, rec); got.Code != api.PluginErrSecretForbidden {
		t.Fatalf("错误码 = %q, want %q", got.Code, api.PluginErrSecretForbidden)
	}
	// 声明过的名字放行。
	if err := mem.UpsertPluginInstallations(a, "e1", []api.PluginInstallationStatusData{{
		PluginID: "p1", Version: "1.0.0", Kind: "Driver", Protocol: 1,
		Permissions: api.PluginPermissionsData{Secrets: []string{"api_token"}},
	}}); err != nil {
		t.Fatal(err)
	}
	srv.plugin.mu.Lock()
	if t2 := srv.plugin.tenants[a]; t2 != nil {
		t2.loaded = false
	}
	srv.plugin.mu.Unlock()
	if rec := servePlugin(t, srv, http.MethodPost, "/api/plugin-instances",
		`{"edge_id":"e1","instance_id":"box1","plugin_id":"p1","version":"1.0.0","secret_refs":["api_token"],"confirm_permissions":true}`,
		a, "tenant-a", string(api.RoleAdmin)); rec.Code != http.StatusOK {
		t.Fatalf("已声明的 secret = %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestPluginInstanceDeleteKeepsObservedByDefault 锁定 purge 语义：
// 默认删除期望态但保留 observed 投影（标 stale），purge 才删投影。
func TestPluginInstanceDeleteKeepsObservedByDefault(t *testing.T) {
	srv, _, _, mem, a, _ := setupPluginPlane(t)
	mustCreate := func(edge, id string) {
		t.Helper()
		rec := servePlugin(t, srv, http.MethodPost, "/api/plugin-instances",
			`{"edge_id":"`+edge+`","instance_id":"`+id+`","plugin_id":"p1","version":"1.0.0"}`,
			a, "tenant-a", string(api.RoleOperator))
		if rec.Code != http.StatusOK {
			t.Fatalf("create %s = %d body=%s", id, rec.Code, rec.Body.String())
		}
	}
	mustCreate("e1", "keep")
	mustCreate("e2", "purge")
	reported := time.Now().Unix()
	for _, pair := range []struct{ edge, id string }{{"e1", "keep"}, {"e2", "purge"}} {
		if err := mem.UpsertPluginObservations(a, pair.edge, []api.PluginObservedInstanceData{{
			InstanceID: pair.id, PluginID: "p1", Version: "1.0.0", HostOnline: true,
			State: "HEALTHY", Health: "HEALTHY", RestartCount: 0,
		}}, reported); err != nil {
			t.Fatal(err)
		}
	}
	srv.plugin.mu.Lock()
	if t2 := srv.plugin.tenants[a]; t2 != nil {
		t2.loaded = false
	}
	srv.plugin.mu.Unlock()

	if rec := servePlugin(t, srv, http.MethodDelete, "/api/plugin-instances/keep", ``,
		a, "tenant-a", string(api.RoleOperator)); rec.Code != http.StatusOK {
		t.Fatalf("delete keep = %d body=%s", rec.Code, rec.Body.String())
	}
	obs, err := mem.ListPluginObservationsTenant(a)
	if err != nil {
		t.Fatal(err)
	}
	kept := 0
	for _, o := range obs {
		if o.InstanceID == "keep" {
			kept++
		}
	}
	if kept != 1 {
		t.Fatalf("默认删除把 observed 投影也删了: %+v", obs)
	}
	if rec := servePlugin(t, srv, http.MethodDelete, "/api/plugin-instances/purge", `{"purge":true}`,
		a, "tenant-a", string(api.RoleAdmin)); rec.Code != http.StatusOK {
		t.Fatalf("purge = %d body=%s", rec.Code, rec.Body.String())
	}
	obs, err = mem.ListPluginObservationsTenant(a)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range obs {
		if o.InstanceID == "purge" {
			t.Fatalf("purge 未删除 observed 投影: %+v", o)
		}
	}
	if rows, err := mem.ListPluginInstancesTenant(a); err != nil || len(rows) != 0 {
		t.Fatalf("删除后仍有期望态: %+v err=%v", rows, err)
	}
}

// TestPluginInstanceReconcileRequiresOnlineEdge 锁定：reconcile 不增 revision；
// Edge 离线必须明确失败（稳定码 plugin_edge_offline），绝不伪装成功。
func TestPluginInstanceReconcileRequiresOnlineEdge(t *testing.T) {
	srv, _, st, mem, a, _ := setupPluginPlane(t)
	if rec := servePlugin(t, srv, http.MethodPost, "/api/plugin-instances",
		`{"edge_id":"e1","instance_id":"box1","plugin_id":"p1","version":"1.0.0"}`,
		a, "tenant-a", string(api.RoleOperator)); rec.Code != http.StatusOK {
		t.Fatalf("create = %d", rec.Code)
	}
	rec := servePlugin(t, srv, http.MethodPost, "/api/plugin-instances/box1/reconcile", `{}`,
		a, "tenant-a", string(api.RoleOperator))
	if rec.Code != http.StatusConflict {
		t.Fatalf("离线 reconcile = %d, want 409 body=%s", rec.Code, rec.Body.String())
	}
	if got := decodePluginBody(t, rec); got.Code != api.PluginErrEdgeOffline {
		t.Fatalf("错误码 = %q, want %q", got.Code, api.PluginErrEdgeOffline)
	}
	if rev, err := mem.PluginDesiredRevision(a, "e1"); err != nil || rev != 1 {
		t.Fatalf("reconcile 改变了 revision = %d err=%v, want 1", rev, err)
	}
	if !hasAudit(auditActions(t, st, a), actionPluginReconcile+":failure") {
		t.Fatalf("离线 reconcile 未记失败审计: %v", auditActions(t, st, a))
	}
}

// TestPluginInstanceNotFoundConflictCrossTenant 锁定稳定错误码：未知实例 404、
// 同租户 instance_id 冲突 409、跨租户一律 404（不泄漏存在性）。
func TestPluginInstanceNotFoundConflictCrossTenant(t *testing.T) {
	srv, _, _, _, a, b := setupPluginPlane(t)
	if rec := servePlugin(t, srv, http.MethodPatch, "/api/plugin-instances/nope", `{"enabled":true}`,
		a, "tenant-a", string(api.RoleOperator)); rec.Code != http.StatusNotFound {
		t.Fatalf("未知实例 patch = %d, want 404", rec.Code)
	} else if got := decodePluginBody(t, rec); got.Code != api.PluginErrNotFound {
		t.Fatalf("错误码 = %q, want %q", got.Code, api.PluginErrNotFound)
	}
	if rec := servePlugin(t, srv, http.MethodPost, "/api/plugin-instances",
		`{"edge_id":"e1","instance_id":"box1","plugin_id":"p1","version":"1.0.0"}`,
		a, "tenant-a", string(api.RoleOperator)); rec.Code != http.StatusOK {
		t.Fatalf("create = %d", rec.Code)
	}
	// 同租户不同 edge 复用 instance_id → 冲突（instance id 在租户内唯一，URL 才可寻址）。
	rec := servePlugin(t, srv, http.MethodPost, "/api/plugin-instances",
		`{"edge_id":"e2","instance_id":"box1","plugin_id":"p1","version":"1.0.0"}`,
		a, "tenant-a", string(api.RoleOperator))
	if rec.Code != http.StatusConflict || decodePluginBody(t, rec).Code != api.PluginErrConflict {
		t.Fatalf("重复 instance_id = %d body=%s", rec.Code, rec.Body.String())
	}
	// tenant-b 既读不到也改不到 tenant-a 的实例。
	if rec := servePlugin(t, srv, http.MethodGet, "/api/plugin-instances/box1", "",
		b, "tenant-b", string(api.RoleAdmin)); rec.Code != http.StatusNotFound {
		t.Fatalf("跨租户读 = %d, want 404", rec.Code)
	}
	if rec := servePlugin(t, srv, http.MethodPatch, "/api/plugin-instances/box1", `{"enabled":false}`,
		b, "tenant-b", string(api.RoleAdmin)); rec.Code != http.StatusNotFound {
		t.Fatalf("跨租户改 = %d, want 404", rec.Code)
	}
	if rec := servePlugin(t, srv, http.MethodDelete, "/api/plugin-instances/box1", "",
		b, "tenant-b", string(api.RoleAdmin)); rec.Code != http.StatusNotFound {
		t.Fatalf("跨租户删 = %d, want 404", rec.Code)
	}
	if list := listInstances(t, srv, b, "tenant-b", string(api.RoleViewer)); len(list.Instances) != 0 {
		t.Fatalf("tenant-b 看到 tenant-a 实例: %+v", list.Instances)
	}
	// tenant-a 的期望态未被跨租户请求改动。
	if list := listInstances(t, srv, a, "tenant-a", string(api.RoleViewer)); len(list.Instances) != 1 ||
		!list.Instances[0].Desired.Enabled {
		t.Fatalf("tenant-a 实例被改动: %+v", list.Instances)
	}
}

// TestPluginInstanceInvalidInput 锁定输入校验：非法 edge/instance/plugin id、缺 version、
// 未知 isolation 都返回稳定码 plugin_invalid_config，且不产生 revision。
func TestPluginInstanceInvalidInput(t *testing.T) {
	srv, _, _, mem, a, _ := setupPluginPlane(t)
	bad := []string{
		`{"edge_id":"","instance_id":"b1","plugin_id":"p1","version":"1.0.0"}`,
		`{"edge_id":"e1","instance_id":"","plugin_id":"p1","version":"1.0.0"}`,
		`{"edge_id":"e1","instance_id":"../escape","plugin_id":"p1","version":"1.0.0"}`,
		`{"edge_id":"e1","instance_id":"b1","plugin_id":"","version":"1.0.0"}`,
		`{"edge_id":"e1","instance_id":"b1","plugin_id":"p1","version":""}`,
		`{"edge_id":"e1","instance_id":"b1","plugin_id":"p1","version":"1.0.0","isolation":"docker"}`,
		`{"edge_id":"e1","instance_id":"b1","plugin_id":"p1","version":"1.0.0","config":{"":"v"}}`,
	}
	for i, body := range bad {
		rec := servePlugin(t, srv, http.MethodPost, "/api/plugin-instances", body,
			a, "tenant-a", string(api.RoleAdmin))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("case %d = %d, want 400 body=%s", i, rec.Code, rec.Body.String())
		}
		if got := decodePluginBody(t, rec); got.Code != api.PluginErrInvalidConfig {
			t.Fatalf("case %d 错误码 = %q, want %q", i, got.Code, api.PluginErrInvalidConfig)
		}
	}
	if rev, err := mem.PluginDesiredRevision(a, "e1"); err != nil || rev != 0 {
		t.Fatalf("非法输入产生了 revision = %d err=%v", rev, err)
	}
}

// TestPluginWriteUnwiredStoreFailsClosed 锁定 PluginStore 未接线时的降级：
// 写 API 503、读面真实为空，绝不伪造期望态。
func TestPluginWriteUnwiredStoreFailsClosed(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "unwired.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv := New(Config{Store: st, Version: "test"})
	t.Cleanup(func() { srv.CloseAll() })
	if srv.PluginControlPlaneWired() {
		t.Fatal("未注入 PluginStore 却报告已接线")
	}
	rec := servePlugin(t, srv, http.MethodPost, "/api/plugin-instances",
		`{"edge_id":"e1","instance_id":"b1","plugin_id":"p1","version":"1.0.0"}`,
		1, "tenant-a", string(api.RoleAdmin))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("未接线写 = %d, want 503", rec.Code)
	}
	if list := listInstances(t, srv, 1, "tenant-a", string(api.RoleViewer)); len(list.Instances) != 0 {
		t.Fatalf("未接线读面应为空: %+v", list.Instances)
	}
}
