package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/store"
)

// doJSON 发起一次可带 header/cookie 的 JSON 请求。
func doJSON(t *testing.T, method, url, body string, headers map[string]string, cookies []*http.Cookie) *http.Response {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func jsonHeaders() map[string]string {
	return map[string]string{"Content-Type": "application/json"}
}

func setupAdmin(t *testing.T, ts *httptest.Server) {
	t.Helper()
	resp := doJSON(t, http.MethodPost, ts.URL+"/api/auth/setup",
		`{"username":"admin","password":"secret123","name":"管理员"}`, jsonHeaders(), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("setup = %d", resp.StatusCode)
	}
}

func loginCookie(t *testing.T, ts *httptest.Server) []*http.Cookie {
	t.Helper()
	resp := doJSON(t, http.MethodPost, ts.URL+"/api/auth/login",
		`{"username":"admin","password":"secret123"}`, jsonHeaders(), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login = %d", resp.StatusCode)
	}
	var body struct {
		User api.UserView `json:"user"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || body.User.Username != "admin" {
		t.Fatalf("login body = %+v err=%v", body, err)
	}
	cookies := resp.Cookies()
	if len(cookies) == 0 {
		t.Fatal("login 应 set-cookie")
	}
	return cookies
}

// TestAuthSetup 首装：0 用户创建 default 租户 + 首个 admin；已有用户 409；
// 建号后立即进入账号模式（/api/* 全鉴权，/healthz 豁免）。
func TestAuthSetup(t *testing.T) {
	srv, ts := setup(t)

	if resp := doJSON(t, http.MethodGet, ts.URL+"/api/auth/me", "", nil, nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("无凭据 me = %d, want 401", resp.StatusCode)
	}

	resp := doJSON(t, http.MethodPost, ts.URL+"/api/auth/setup",
		`{"username":"admin","password":"secret123","name":"管理员"}`, jsonHeaders(), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("setup = %d, want 200", resp.StatusCode)
	}
	var body struct {
		User api.UserView `json:"user"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.User.Role != "admin" || body.User.Username != "admin" ||
		body.User.TenantSlug != "default" || body.User.ID <= 0 || body.User.TenantID <= 0 {
		t.Fatalf("bad setup user: %+v", body.User)
	}

	// 已有用户 → 409
	if resp := doJSON(t, http.MethodPost, ts.URL+"/api/auth/setup",
		`{"username":"admin2","password":"secret456"}`, jsonHeaders(), nil); resp.StatusCode != http.StatusConflict {
		t.Fatalf("二次 setup = %d, want 409", resp.StatusCode)
	}
	if n, err := srv.cfg.Store.CountUsers(); err != nil || n != 1 {
		t.Fatalf("users = %d err=%v, want 1", n, err)
	}

	// 账号模式全鉴权：/api/devices 401、/healthz 200
	if resp := doJSON(t, http.MethodGet, ts.URL+"/api/devices", "", nil, nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("账号模式 /api/devices = %d, want 401", resp.StatusCode)
	}
	if resp := doJSON(t, http.MethodGet, ts.URL+"/healthz", "", nil, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("/healthz = %d, want 200（豁免）", resp.StatusCode)
	}
}

// TestAuthLogin 登录：错 401 / 对 200 + set-cookie；每次登录会话 ID 轮换（防固定）。
func TestAuthLogin(t *testing.T) {
	_, ts := setup(t)
	setupAdmin(t, ts)

	if resp := doJSON(t, http.MethodPost, ts.URL+"/api/auth/login",
		`{"username":"admin","password":"wrong"}`, jsonHeaders(), nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("错密码 = %d, want 401", resp.StatusCode)
	}
	if resp := doJSON(t, http.MethodPost, ts.URL+"/api/auth/login",
		`{"username":"nope","password":"secret123"}`, jsonHeaders(), nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("错用户 = %d, want 401", resp.StatusCode)
	}

	cookies := loginCookie(t, ts)
	c := cookies[0]
	if c.Name != "cp_session" || !c.HttpOnly || c.Path != "/" || c.SameSite != http.SameSiteLaxMode || c.MaxAge <= 0 {
		t.Fatalf("cookie 属性不符: %+v", c)
	}

	// 再次登录 → 会话 ID 轮换
	cookies2 := loginCookie(t, ts)
	if cookies2[0].Value == c.Value {
		t.Fatal("登录后会话 ID 应轮换（防会话固定）")
	}

	// 会话可用：带 cookie 读受保护 API
	if resp := doJSON(t, http.MethodGet, ts.URL+"/api/adapters", "", nil, cookies2); resp.StatusCode != http.StatusOK {
		t.Fatalf("带会话 /api/adapters = %d, want 200", resp.StatusCode)
	}
}

// TestAuthLogout 登出：删服务端会话 + 清 cookie；旧会话立即失效。
func TestAuthLogout(t *testing.T) {
	_, ts := setup(t)
	setupAdmin(t, ts)
	cookies := loginCookie(t, ts)

	if resp := doJSON(t, http.MethodGet, ts.URL+"/api/auth/me", "", nil, cookies); resp.StatusCode != http.StatusOK {
		t.Fatalf("me = %d, want 200", resp.StatusCode)
	}

	resp := doJSON(t, http.MethodPost, ts.URL+"/api/auth/logout", "", nil, cookies)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout = %d, want 204", resp.StatusCode)
	}
	cleared := false
	for _, c := range resp.Cookies() {
		if c.Name == "cp_session" && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("logout 应清 cookie（MaxAge<0）")
	}

	// 服务端会话已删：旧 cookie 不可再用
	if resp := doJSON(t, http.MethodGet, ts.URL+"/api/auth/me", "", nil, cookies); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("登出后 me = %d, want 401", resp.StatusCode)
	}
	// 无会话再次 logout → 401
	if resp := doJSON(t, http.MethodPost, ts.URL+"/api/auth/logout", "", nil, nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("无会话 logout = %d, want 401", resp.StatusCode)
	}
}

// TestAuthMe 身份查询：服务令牌等价 admin；错令牌/无凭据 401。
func TestAuthMe(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv := New(Config{Store: st, Version: "test", Token: "sekret"})
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()
	defer srv.CloseAll()

	resp := doJSON(t, http.MethodGet, ts.URL+"/api/auth/me", "", map[string]string{"Authorization": "Bearer sekret"}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("token me = %d, want 200", resp.StatusCode)
	}
	var body struct {
		User api.UserView `json:"user"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.User.Role != "admin" || body.User.TenantSlug != "default" || body.User.TenantID <= 0 {
		t.Fatalf("token 身份 = %+v（应等价 default 租户 admin）", body.User)
	}

	if resp := doJSON(t, http.MethodGet, ts.URL+"/api/auth/me", "", map[string]string{"Authorization": "Bearer wrong"}, nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("错 token me = %d, want 401", resp.StatusCode)
	}
}

// TestRequireAuthForcesReadAuth -require-auth 在无用户时也强制读鉴权（配合服务令牌）。
func TestRequireAuthForcesReadAuth(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv := New(Config{Store: st, Version: "test", Token: "sekret", RequireAuth: true})
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()
	defer srv.CloseAll()

	if resp := doJSON(t, http.MethodGet, ts.URL+"/api/devices", "", nil, nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("require-auth 下无凭据读 = %d, want 401", resp.StatusCode)
	}
	if resp := doJSON(t, http.MethodGet, ts.URL+"/api/devices", "",
		map[string]string{"Authorization": "Bearer sekret"}, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("require-auth 下服务令牌读 = %d, want 200", resp.StatusCode)
	}
	if resp := doJSON(t, http.MethodGet, ts.URL+"/healthz", "", nil, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("require-auth 下 /healthz = %d, want 200（豁免）", resp.StatusCode)
	}
}

// TestLoginRateLimit 登录连错达 -login-rate 上限 → 429 + Retry-After。
func TestLoginRateLimit(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv := New(Config{Store: st, Version: "test", LoginRatePerMin: 3})
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()
	defer srv.CloseAll()

	for i := 0; i < 3; i++ {
		if resp := doJSON(t, http.MethodPost, ts.URL+"/api/auth/login",
			`{"username":"x","password":"y"}`, jsonHeaders(), nil); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("第 %d 次错登录 = %d, want 401", i+1, resp.StatusCode)
		}
	}
	resp := doJSON(t, http.MethodPost, ts.URL+"/api/auth/login",
		`{"username":"x","password":"y"}`, jsonHeaders(), nil)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("超限登录 = %d, want 429", resp.StatusCode)
	}
	if got := resp.Header.Get("Retry-After"); got == "" {
		t.Fatal("429 应带 Retry-After")
	}
}

// TestCommandWriteAuth 契约 §1 不变量「无凭据不写」：
// 无凭据回环 200、非回环 403；服务令牌非回环 200；-token 下回环仍放行。
func TestCommandWriteAuth(t *testing.T) {
	srv, ts := setup(t)
	registerEdge(t, ts, "e1", api.DeviceMeta{ID: "d1", Adapter: "stcb"})

	// 回环（真实 httptest 客户端）+ 无凭据 → 200
	if code := postCommand(t, ts.URL+"/api/devices/e1/d1/commands", `{"cmd":"dump"}`); code != http.StatusOK {
		t.Fatalf("回环无凭据 = %d, want 200", code)
	}

	// 非回环 + 无凭据 → 403（直接调 handler 伪造来源）
	req := httptest.NewRequest(http.MethodPost, "/api/devices/e1/d1/commands", strings.NewReader(`{"cmd":"dump"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "203.0.113.9:43210"
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("非回环无凭据 = %d, want 403", rec.Code)
	}

	// 服务令牌等价 admin：-token 下非回环 + Bearer → 200
	st2, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	srv2 := New(Config{Store: st2, Version: "test", Token: "sekret"})
	ts2 := httptest.NewServer(srv2.Routes())
	defer ts2.Close()
	defer srv2.CloseAll()

	ews := dial(t, wsURL(ts2.URL, "/ws/edge"))
	writeEnv(t, ews, api.Envelope{
		V: api.Version, Type: api.MsgHello, Ts: time.Now().Unix(),
		Data: rawData(t, api.HelloData{EdgeID: "e1", Token: "sekret",
			Devices: []api.DeviceMeta{{ID: "d1", Adapter: "stcb"}}}),
	})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var devs struct {
			Devices []api.DeviceView `json:"devices"`
		}
		getJSON(t, ts2.URL+"/api/devices", &devs)
		if len(devs.Devices) == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/api/devices/e1/d1/commands", strings.NewReader(`{"cmd":"dump"}`))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Authorization", "Bearer sekret")
	req2.RemoteAddr = "203.0.113.9:43210"
	rec2 := httptest.NewRecorder()
	srv2.Routes().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("非回环 + 服务令牌 = %d, want 200", rec2.Code)
	}

	req3 := httptest.NewRequest(http.MethodPost, "/api/devices/e1/d1/commands", strings.NewReader(`{"cmd":"dump"}`))
	req3.Header.Set("Content-Type", "application/json")
	req3.RemoteAddr = "203.0.113.9:43210"
	rec3 := httptest.NewRecorder()
	srv2.Routes().ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusForbidden {
		t.Fatalf("-token 下非回环无凭据 = %d, want 403", rec3.Code)
	}

	// -token 下回环仍放行（本机控制台语义，契约不变量 1）
	if code := postCommand(t, ts2.URL+"/api/devices/e1/d1/commands", `{"cmd":"dump"}`); code != http.StatusOK {
		t.Fatalf("-token 下回环无凭据 = %d, want 200", code)
	}
}

// TestStaticCacheHeaders 契约 §1.6：/（index）no-cache；/assets/* immutable。
func TestStaticCacheHeaders(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>app</html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "app.js"), []byte("console.log(1)"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := New(Config{Version: "test", WebUIDir: dir})
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()
	defer srv.CloseAll()

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := resp.Header.Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("/ Cache-Control = %q, want no-cache", got)
	}

	resp, err = http.Get(ts.URL + "/assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := resp.Header.Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("/assets/* Cache-Control = %q", got)
	}
}
