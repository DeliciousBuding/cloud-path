package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/appruntime"
	"github.com/DeliciousBuding/cloud-path/internal/auth"
	"github.com/DeliciousBuding/cloud-path/internal/store"
)

// ---- D1 Application Data Plane 测试 ----
//
// 覆盖：records 分页/过滤/畸形输入、租户隔离（跨租户同形空列表）、
// bindings/jobs 运行态投影（含 appHost 未启用）、领域记录 WS 投影
// （created/updated 双态 + 按租户过滤）、重启持久化。

// setupAppPlane 构造带真实 store 的 server（账号模式）并给 default 租户建 admin。
func setupAppPlane(t *testing.T) (*Server, *httptest.Server, *store.Store, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "appplane.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	srv := New(Config{Store: st, Version: "test", RequireAuth: true})
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(func() { st.Close() })
	t.Cleanup(func() { ts.Close(); srv.CloseAll(); time.Sleep(50 * time.Millisecond) })

	hash, err := auth.HashPassword("secret123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUser(1, "admin", "管理员", "admin", hash); err != nil {
		t.Fatal(err)
	}
	return srv, ts, st, dbPath
}

// appPlaneCookie 登录拿会话 cookie。
func appPlaneCookie(t *testing.T, ts *httptest.Server, username string) []*http.Cookie {
	t.Helper()
	resp := doJSON(t, http.MethodPost, ts.URL+"/api/auth/login",
		`{"username":"`+username+`","password":"secret123"}`, jsonHeaders(), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login %s = %d", username, resp.StatusCode)
	}
	return resp.Cookies()
}

func decodeAppPlane[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	var out T
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAppRecordsReadPlane(t *testing.T) {
	srv, ts, st, dbPath := setupAppPlane(t)
	_ = srv
	cookie := appPlaneCookie(t, ts, "admin")

	// 5 条 window + 2 条 intake 记录（updated_at 递增保证倒序稳定）
	for i := 0; i < 5; i++ {
		if err := st.UpsertAppDomainRecord(1, "box-1", "window", "w-"+string(rune('a'+i)),
			`{"n":`+string(rune('0'+i))+`}`, "1", int64(1700000000+i)); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := st.UpsertAppDomainRecord(1, "box-1", "intake", "i-"+string(rune('a'+i)),
			`{}`, "1", int64(1700000100+i)); err != nil {
			t.Fatal(err)
		}
	}

	base := ts.URL + "/api/plugin-instances/box-1/records"

	// 默认参数：全量（updated_at 倒序，intake 在前）
	resp := doJSON(t, http.MethodGet, base, "", nil, cookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("default = %d", resp.StatusCode)
	}
	view := decodeAppPlane[api.AppDomainRecordsView](t, resp)
	if len(view.Records) != 7 || view.Limit != 100 || view.Offset != 0 {
		t.Fatalf("default view = %+v", view)
	}
	if view.Records[0].RecordType != "intake" || view.Records[0].RecordID != "i-b" {
		t.Fatalf("ordering = %+v", view.Records[0])
	}

	// record_type 过滤
	resp = doJSON(t, http.MethodGet, base+"?record_type=window", "", nil, cookie)
	view = decodeAppPlane[api.AppDomainRecordsView](t, resp)
	if len(view.Records) != 5 || view.RecordType != "window" {
		t.Fatalf("filter = %+v", view)
	}
	for _, r := range view.Records {
		if r.RecordType != "window" {
			t.Fatalf("filter leaked: %+v", r)
		}
	}

	// 分页：limit=3 offset=0 / offset=3 窗口不相交且拼接为全集
	resp = doJSON(t, http.MethodGet, base+"?record_type=window&limit=3", "", nil, cookie)
	page1 := decodeAppPlane[api.AppDomainRecordsView](t, resp)
	resp = doJSON(t, http.MethodGet, base+"?record_type=window&limit=3&offset=3", "", nil, cookie)
	page2 := decodeAppPlane[api.AppDomainRecordsView](t, resp)
	if len(page1.Records) != 3 || len(page2.Records) != 2 || page1.Offset != 0 || page2.Offset != 3 {
		t.Fatalf("pages = %d/%d", len(page1.Records), len(page2.Records))
	}
	for _, a := range page1.Records {
		for _, b := range page2.Records {
			if a.RecordID == b.RecordID {
				t.Fatalf("pages overlap at %s", a.RecordID)
			}
		}
	}

	// 超出末尾：空列表不是错误
	resp = doJSON(t, http.MethodGet, base+"?offset=999", "", nil, cookie)
	view = decodeAppPlane[api.AppDomainRecordsView](t, resp)
	if resp.StatusCode != http.StatusOK || len(view.Records) != 0 {
		t.Fatalf("past-end = %d %+v", resp.StatusCode, view.Records)
	}

	// 畸形输入显式拒绝
	for _, q := range []string{"?limit=abc", "?limit=-1", "?limit=1001", "?offset=-5", "?offset=x", "?record_type=bad/slash"} {
		resp := doJSON(t, http.MethodGet, base+q, "", nil, cookie)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s = %d, want 400", q, resp.StatusCode)
		}
	}

	// 未认证 401
	resp = doJSON(t, http.MethodGet, base, "", nil, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauth = %d", resp.StatusCode)
	}

	// 重启持久化：同一路径重开 store，记录仍在
	st.Close()
	st2, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st2.Close() })
	rows, err := st2.ListAppDomainRecordsFiltered(1, "box-1", "window", 100, 0)
	if err != nil || len(rows) != 5 {
		t.Fatalf("reopen: rows=%d err=%v", len(rows), err)
	}
}

func TestAppRecordsTenantIsolation(t *testing.T) {
	_, ts, st, _ := setupAppPlane(t)

	// tenant-b 的用户
	bID, err := st.CreateTenant("tenant-b", "tenant-b")
	if err != nil {
		t.Fatal(err)
	}
	hash, err := auth.HashPassword("secret123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUser(bID, "buser", "B", "viewer", hash); err != nil {
		t.Fatal(err)
	}

	// default 租户的实例写满记录
	for i := 0; i < 3; i++ {
		if err := st.UpsertAppDomainRecord(1, "box-1", "window", "w-"+string(rune('a'+i)),
			`{"secret":"tenant-a-data"}`, "1", int64(1700000000+i)); err != nil {
			t.Fatal(err)
		}
	}

	adminCookie := appPlaneCookie(t, ts, "admin")
	resp := doJSON(t, http.MethodGet, ts.URL+"/api/plugin-instances/box-1/records", "", nil, adminCookie)
	view := decodeAppPlane[api.AppDomainRecordsView](t, resp)
	if len(view.Records) != 3 {
		t.Fatalf("owner sees = %d", len(view.Records))
	}

	// tenant-b 查同一实例：200 + 空列表，与「实例不存在」同形（探测无信息）
	bCookie := appPlaneCookie(t, ts, "buser")
	resp = doJSON(t, http.MethodGet, ts.URL+"/api/plugin-instances/box-1/records", "", nil, bCookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("foreign tenant = %d", resp.StatusCode)
	}
	view = decodeAppPlane[api.AppDomainRecordsView](t, resp)
	if len(view.Records) != 0 {
		t.Fatalf("tenant isolation leak: %+v", view.Records)
	}
}

func TestAppBindingsJobsRuntimeProjection(t *testing.T) {
	srv, ts, _, _ := setupAppPlane(t)
	cookie := appPlaneCookie(t, ts, "admin")

	// 场景 0：appHost 未启用（nil）→ running=false 空数组，不 500
	resp := doJSON(t, http.MethodGet, ts.URL+"/api/plugin-instances/box-1/bindings", "", nil, cookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("nil apphost bindings = %d", resp.StatusCode)
	}
	bv := decodeAppPlane[api.AppBindingsView](t, resp)
	if bv.Running || len(bv.Bindings) != 0 {
		t.Fatalf("nil apphost = %+v", bv)
	}
	resp = doJSON(t, http.MethodGet, ts.URL+"/api/plugin-instances/box-1/jobs", "", nil, cookie)
	jv := decodeAppPlane[api.AppJobsView](t, resp)
	if resp.StatusCode != http.StatusOK || jv.Running || len(jv.Jobs) != 0 {
		t.Fatalf("nil apphost jobs = %d %+v", resp.StatusCode, jv)
	}

	// 场景 1：注入运行态（包内构造，绕过进程握手）
	row := store.PluginInstanceRow{TenantID: 1, InstanceID: "box-1", PluginID: "app-x", Enabled: true}
	srv.SetAppHost(&AppHost{
		logger: slog.Default(),
		running: map[string]*appInstanceRun{
			"box-1": {
				row:         row,
				tenantStr:   "1",
				reqByEntity: map[string]string{"alarm-1": "reminder-output"},
				bindings: []api.AppBindingView{
					{RequirementID: "reminder-output", Capability: "cloudpath.dev/capability/alarm@1", EntityID: "alarm-1"},
					{RequirementID: "compartments", Capability: "cloudpath.dev/capability/contact@1", EntityID: "comp-1"},
				},
				jobIDs: []string{"window-check"},
			},
		},
	})

	resp = doJSON(t, http.MethodGet, ts.URL+"/api/plugin-instances/box-1/bindings", "", nil, cookie)
	bv = decodeAppPlane[api.AppBindingsView](t, resp)
	if !bv.Running || len(bv.Bindings) != 2 || bv.Bindings[0].EntityID != "alarm-1" ||
		bv.Bindings[0].Capability != "cloudpath.dev/capability/alarm@1" {
		t.Fatalf("bindings = %+v", bv)
	}
	resp = doJSON(t, http.MethodGet, ts.URL+"/api/plugin-instances/box-1/jobs", "", nil, cookie)
	jv = decodeAppPlane[api.AppJobsView](t, resp)
	if !jv.Running || len(jv.Jobs) != 1 || jv.Jobs[0] != "window-check" {
		t.Fatalf("jobs = %+v", jv)
	}

	// 场景 2：跨租户（tenant-b 用户）→ running=false 空数组，且拿不到绑定内容
	hash, _ := auth.HashPassword("secret123")
	if _, err := srv.cfg.Store.CreateTenant("tenant-b", "tenant-b"); err != nil {
		t.Fatal(err)
	}
	bID, err := srv.cfg.Store.GetTenantBySlug("tenant-b")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.cfg.Store.CreateUser(bID.ID, "buser", "B", "viewer", hash); err != nil {
		t.Fatal(err)
	}
	bCookie := appPlaneCookie(t, ts, "buser")
	resp = doJSON(t, http.MethodGet, ts.URL+"/api/plugin-instances/box-1/bindings", "", nil, bCookie)
	bv = decodeAppPlane[api.AppBindingsView](t, resp)
	if resp.StatusCode != http.StatusOK || bv.Running || len(bv.Bindings) != 0 {
		t.Fatalf("cross-tenant bindings = %d %+v", resp.StatusCode, bv)
	}
}

func TestDomainRecordWSProjection(t *testing.T) {
	srv, ts, st, _ := setupAppPlane(t)
	_ = ts

	// default 租户浏览器连接（包内注入 browserConn，读 send 通道收投影）
	owner := &browserConn{send: make(chan []byte, 4), cancel: func() {}, tenant: "default"}
	foreign := &browserConn{send: make(chan []byte, 4), cancel: func() {}, tenant: "tenant-b"}
	srv.mu.Lock()
	srv.browsers[owner] = struct{}{}
	srv.browsers[foreign] = struct{}{}
	srv.mu.Unlock()

	h := &AppHost{srv: srv, logger: slog.Default()}
	exec := &appEffectExecutor{host: h}
	trow, err := st.GetTenantBySlug("default")
	if err != nil {
		t.Fatal(err)
	}
	effect := appruntime.Effect{
		ID:               "e-1",
		IdempotencyKey:   "rec-1",
		TenantID:         trow.Slug, // 由执行器解析成 id
		PluginInstanceID: "box-1",
		Kind:             appruntime.EffectCreateDomainRecord,
		CreateDomainRecord: &appruntime.CreateDomainRecord{
			RecordType: "window", RecordID: "w-1", DataJSON: `{"state":"opened"}`, Version: "1",
		},
	}
	// TenantID 需要是数字字符串（执行器 ParseInt）
	effect.TenantID = "1"

	// 第一次写入 → created=true
	if err := exec.Execute(context.Background(), effect); err != nil {
		t.Fatalf("execute create: %v", err)
	}
	select {
	case raw := <-owner.send:
		var env api.Envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatal(err)
		}
		if env.Type != api.MsgDomainRecord {
			t.Fatalf("type = %s", env.Type)
		}
		var data api.DomainRecordData
		if err := json.Unmarshal(env.Data, &data); err != nil {
			t.Fatal(err)
		}
		if !data.Created || data.InstanceID != "box-1" || data.RecordType != "window" ||
			data.RecordID != "w-1" || data.DataJSON != `{"state":"opened"}` {
			t.Fatalf("projection = %+v", data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("owner 浏览器未收到投影")
	}

	// 同键再写 → created=false（updated）
	effect.CreateDomainRecord.DataJSON = `{"state":"completed"}`
	effect.ID, effect.IdempotencyKey = "e-2", "rec-2"
	if err := exec.Execute(context.Background(), effect); err != nil {
		t.Fatalf("execute update: %v", err)
	}
	select {
	case raw := <-owner.send:
		var env api.Envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatal(err)
		}
		var data api.DomainRecordData
		if err := json.Unmarshal(env.Data, &data); err != nil {
			t.Fatal(err)
		}
		if data.Created {
			t.Fatalf("second write should be updated, got %+v", data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("owner 浏览器未收到第二次投影")
	}

	// 跨租户浏览器收不到任何东西
	select {
	case raw := <-foreign.send:
		t.Fatalf("tenant-b 浏览器收到了 default 租户投影: %s", raw)
	default:
	}

	// 记录本体两次落库（最终态）
	row, err := st.GetAppDomainRecord(1, "box-1", "window", "w-1")
	if err != nil || row.DataJSON != `{"state":"completed"}` {
		t.Fatalf("store = %+v err=%v", row, err)
	}
}
