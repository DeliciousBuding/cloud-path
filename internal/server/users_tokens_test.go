package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/auth"
	"github.com/DeliciousBuding/cloud-path/internal/store"
)

// setupUsersTokens 构造带 store 引用的 server（令牌/用户管理测试用）。
func setupUsersTokens(t *testing.T) (*Server, *httptest.Server, *store.Store) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	srv := New(Config{Store: st, Version: "test"})
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(func() { ts.Close(); srv.CloseAll(); time.Sleep(50 * time.Millisecond) })
	t.Cleanup(func() { st.Close() })
	return srv, ts, st
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// createToken 用 admin cookie 创建一枚令牌，返回 {id,token}。
func createToken(t *testing.T, ts *httptest.Server, cookies []*http.Cookie, body string) (int64, string) {
	t.Helper()
	resp := doJSON(t, http.MethodPost, ts.URL+"/api/tokens", body, jsonHeaders(), cookies)
	raw := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create token = %d body=%s", resp.StatusCode, raw)
	}
	var out struct {
		ID    int64  `json:"id"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("decode token: %v body=%s", err, raw)
	}
	if out.ID <= 0 || out.Token == "" {
		t.Fatalf("bad token response: %s", raw)
	}
	return out.ID, out.Token
}

func bearer(secret string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + secret}
}

func bearerJSON(secret string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + secret, "Content-Type": "application/json"}
}

// TestCreateTokenReturnsSecretOnce 明文只出现一次：创建响应含 token，列表/DB 不再出现。
func TestCreateTokenReturnsSecretOnce(t *testing.T) {
	_, ts := setup(t)
	setupAdmin(t, ts)
	cookies := loginCookie(t, ts)

	resp := doJSON(t, http.MethodPost, ts.URL+"/api/tokens",
		`{"name":"ci","scopes":["read","write"]}`, jsonHeaders(), cookies)
	raw := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create token = %d", resp.StatusCode)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.Token, auth.TenantTokenPrefix) {
		t.Fatalf("token 缺 cp_ 前缀: %q", out.Token)
	}
	if n := strings.Count(raw, out.Token); n != 1 {
		t.Fatalf("明文应只出现一次, got %d: %s", n, raw)
	}

	// 列表响应不得包含明文。
	lr := doJSON(t, http.MethodGet, ts.URL+"/api/tokens", "", nil, cookies)
	lraw := readBody(t, lr)
	if lr.StatusCode != http.StatusOK {
		t.Fatalf("list tokens = %d", lr.StatusCode)
	}
	if strings.Contains(lraw, out.Token) {
		t.Fatalf("list 泄露明文 token: %s", lraw)
	}
}

// TestTokenStoredHashed 数据库只存 SHA-256 hash 与短 prefix，不存明文。
func TestTokenStoredHashed(t *testing.T) {
	_, ts, st := setupUsersTokens(t)
	setupAdmin(t, ts)
	cookies := loginCookie(t, ts)

	_, secret := createToken(t, ts, cookies, `{"name":"hash","scopes":["read"]}`)
	row, err := st.GetTenantTokenByHash(auth.HashToken(secret))
	if err != nil {
		t.Fatalf("hash 查找失败: %v", err)
	}
	if row.Hash != auth.HashToken(secret) {
		t.Fatalf("hash 不符: got %s want %s", row.Hash, auth.HashToken(secret))
	}
	if row.Hash == secret || strings.Contains(row.Hash, secret) {
		t.Fatalf("hash 列不得含明文")
	}
	if row.Prefix == secret || strings.Contains(row.Prefix, secret) {
		t.Fatalf("prefix 列不得含明文")
	}
	if len(row.Prefix) >= len(secret) {
		t.Fatalf("prefix 应远短于完整 token")
	}
}

// TestRevokedExpiredTokenRejected 吊销/过期 token 一律 401。
func TestRevokedExpiredTokenRejected(t *testing.T) {
	_, ts := setup(t)
	setupAdmin(t, ts)
	cookies := loginCookie(t, ts)

	id, secret := createToken(t, ts, cookies, `{"name":"t","scopes":["read","write","admin"]}`)
	if resp := doJSON(t, http.MethodGet, ts.URL+"/api/devices", "", bearer(secret), nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("有效 token 读设备 = %d, want 200", resp.StatusCode)
	}

	del := doJSON(t, http.MethodDelete, ts.URL+"/api/tokens/"+itoa(id), "", nil, cookies)
	if del.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke = %d, want 204", del.StatusCode)
	}
	if resp := doJSON(t, http.MethodGet, ts.URL+"/api/devices", "", bearer(secret), nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("吊销 token = %d, want 401", resp.StatusCode)
	}

	// 已过期 token 立即 401。
	expired := time.Now().Add(-time.Hour).Unix()
	_, expSecret := createToken(t, ts, cookies,
		`{"name":"exp","scopes":["read"],"expires_at":`+itoa64(expired)+`}`)
	if resp := doJSON(t, http.MethodGet, ts.URL+"/api/devices", "", bearer(expSecret), nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("过期 token = %d, want 401", resp.StatusCode)
	}
}

// TestTokenScopeEnforcement scope 与角色模型取交集，不足即 403。
func TestTokenScopeEnforcement(t *testing.T) {
	_, ts := setup(t)
	setupAdmin(t, ts)
	cookies := loginCookie(t, ts)

	_, readOnly := createToken(t, ts, cookies, `{"name":"r","scopes":["read"]}`)
	if resp := doJSON(t, http.MethodGet, ts.URL+"/api/devices", "", bearer(readOnly), nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("read token 读 = %d, want 200", resp.StatusCode)
	}
	if resp := doJSON(t, http.MethodGet, ts.URL+"/api/users", "", bearer(readOnly), nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("read token 管用户 = %d, want 403", resp.StatusCode)
	}
	if resp := doJSON(t, http.MethodPost, ts.URL+"/api/devices/e1/d1/commands", `{"cmd":"dump"}`,
		bearerJSON(readOnly), nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("read token 写 = %d, want 403", resp.StatusCode)
	}

	_, write := createToken(t, ts, cookies, `{"name":"w","scopes":["read","write"]}`)
	if resp := doJSON(t, http.MethodGet, ts.URL+"/api/users", "", bearer(write), nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("write token 管用户 = %d, want 403", resp.StatusCode)
	}

	_, adminTok := createToken(t, ts, cookies, `{"name":"a","scopes":["read","write","admin"]}`)
	if resp := doJSON(t, http.MethodGet, ts.URL+"/api/users", "", bearer(adminTok), nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("admin token 管用户 = %d, want 200", resp.StatusCode)
	}

	_, edgeOnly := createToken(t, ts, cookies, `{"name":"e","scopes":["edge"]}`)
	if resp := doJSON(t, http.MethodGet, ts.URL+"/api/devices", "", bearer(edgeOnly), nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("edge-only token 读 = %d, want 403", resp.StatusCode)
	}
}

// TestViewerCannotCommand viewer 只读，命令下发 403，读 200。
func TestViewerCannotCommand(t *testing.T) {
	_, ts := setup(t)
	setupAdmin(t, ts)
	admin := loginCookie(t, ts)

	if resp := doJSON(t, http.MethodPost, ts.URL+"/api/users",
		`{"username":"viewer","password":"pw123456","role":"viewer"}`, jsonHeaders(), admin); resp.StatusCode != http.StatusOK {
		t.Fatalf("create viewer = %d", resp.StatusCode)
	}
	viewer := loginAs(t, ts, "viewer", "pw123456")

	if resp := doJSON(t, http.MethodGet, ts.URL+"/api/devices", "", nil, viewer); resp.StatusCode != http.StatusOK {
		t.Fatalf("viewer 读 = %d, want 200", resp.StatusCode)
	}
	if resp := doJSON(t, http.MethodPost, ts.URL+"/api/devices/e1/d1/commands", `{"cmd":"dump"}`,
		jsonHeaders(), viewer); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer 写 = %d, want 403", resp.StatusCode)
	}
}

// TestOperatorCannotManageUsers operator 可读可写，但无权管理用户/令牌。
func TestOperatorCannotManageUsers(t *testing.T) {
	_, ts := setup(t)
	setupAdmin(t, ts)
	admin := loginCookie(t, ts)

	if resp := doJSON(t, http.MethodPost, ts.URL+"/api/users",
		`{"username":"op","password":"pw123456","role":"operator"}`, jsonHeaders(), admin); resp.StatusCode != http.StatusOK {
		t.Fatalf("create operator = %d", resp.StatusCode)
	}
	op := loginAs(t, ts, "op", "pw123456")

	for _, path := range []string{"/api/users", "/api/tokens"} {
		if resp := doJSON(t, http.MethodGet, ts.URL+path, "", nil, op); resp.StatusCode != http.StatusForbidden {
			t.Fatalf("operator GET %s = %d, want 403", path, resp.StatusCode)
		}
	}
	if resp := doJSON(t, http.MethodPost, ts.URL+"/api/users",
		`{"username":"x","password":"pw123456","role":"viewer"}`, jsonHeaders(), op); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("operator 建用户 = %d, want 403", resp.StatusCode)
	}
}

// TestUserTenantIsolation 跨租户用户/令牌资源一律 404。
func TestUserTenantIsolation(t *testing.T) {
	_, ts, st := setupUsersTokens(t)
	setupAdmin(t, ts)
	admin := loginCookie(t, ts)

	bID, err := st.CreateTenant("tenant-b", "tenant-b")
	if err != nil {
		t.Fatal(err)
	}
	hashB, err := auth.HashPassword("secretB")
	if err != nil {
		t.Fatal(err)
	}
	ub, err := st.CreateUser(bID, "admin-b", "B", "admin", hashB)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := st.CreateTenantToken(bID, "bt", auth.HashToken("cp_x-tenant-b"), "cp_abcdefgh", `["read"]`, nil)
	if err != nil {
		t.Fatal(err)
	}

	// 列表只含本租户用户。
	lr := doJSON(t, http.MethodGet, ts.URL+"/api/users", "", nil, admin)
	lraw := readBody(t, lr)
	if strings.Contains(lraw, "admin-b") {
		t.Fatalf("跨租户用户泄漏: %s", lraw)
	}

	// PATCH 跨租户用户 → 404。
	if resp := doJSON(t, http.MethodPatch, ts.URL+"/api/users/"+itoa(ub.ID),
		`{"disabled":true}`, jsonHeaders(), admin); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("跨租户 PATCH user = %d, want 404", resp.StatusCode)
	}
	// DELETE 跨租户令牌 → 404。
	if resp := doJSON(t, http.MethodDelete, ts.URL+"/api/tokens/"+itoa(tok.ID), "", nil, admin); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("跨租户 DELETE token = %d, want 404", resp.StatusCode)
	}
}

// TestDisableUserRevokesSessions 禁用/密码重置撤销全部会话。
func TestDisableUserRevokesSessions(t *testing.T) {
	_, ts := setup(t)
	setupAdmin(t, ts)
	admin := loginCookie(t, ts)

	// 禁用撤销会话。
	if resp := doJSON(t, http.MethodPost, ts.URL+"/api/users",
		`{"username":"u1","password":"pw123456","role":"operator"}`, jsonHeaders(), admin); resp.StatusCode != http.StatusOK {
		t.Fatalf("create u1 = %d", resp.StatusCode)
	}
	u1 := loginAs(t, ts, "u1", "pw123456")
	if resp := doJSON(t, http.MethodGet, ts.URL+"/api/auth/me", "", nil, u1); resp.StatusCode != http.StatusOK {
		t.Fatalf("u1 me 前 = %d, want 200", resp.StatusCode)
	}
	var u1view struct {
		User api.UserView `json:"user"`
	}
	if err := json.NewDecoder(strings.NewReader(readBody(t, doJSON(t, http.MethodGet, ts.URL+"/api/auth/me", "", nil, u1)))).Decode(&u1view); err != nil {
		t.Fatal(err)
	}
	if resp := doJSON(t, http.MethodPatch, ts.URL+"/api/users/"+itoa(u1view.User.ID),
		`{"disabled":true}`, jsonHeaders(), admin); resp.StatusCode != http.StatusOK {
		t.Fatalf("disable u1 = %d", resp.StatusCode)
	}
	if resp := doJSON(t, http.MethodGet, ts.URL+"/api/auth/me", "", nil, u1); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("禁用后 u1 me = %d, want 401", resp.StatusCode)
	}

	// 密码重置撤销会话，新密码可登录。
	if resp := doJSON(t, http.MethodPost, ts.URL+"/api/users",
		`{"username":"u2","password":"pw123456","role":"operator"}`, jsonHeaders(), admin); resp.StatusCode != http.StatusOK {
		t.Fatalf("create u2 = %d", resp.StatusCode)
	}
	u2 := loginAs(t, ts, "u2", "pw123456")
	var u2view struct {
		User api.UserView `json:"user"`
	}
	if err := json.NewDecoder(strings.NewReader(readBody(t, doJSON(t, http.MethodGet, ts.URL+"/api/auth/me", "", nil, u2)))).Decode(&u2view); err != nil {
		t.Fatal(err)
	}
	if resp := doJSON(t, http.MethodPatch, ts.URL+"/api/users/"+itoa(u2view.User.ID),
		`{"password":"newpw123"}`, jsonHeaders(), admin); resp.StatusCode != http.StatusOK {
		t.Fatalf("reset u2 = %d", resp.StatusCode)
	}
	if resp := doJSON(t, http.MethodGet, ts.URL+"/api/auth/me", "", nil, u2); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("重置后旧会话 = %d, want 401", resp.StatusCode)
	}
	if resp := doJSON(t, http.MethodPost, ts.URL+"/api/auth/login",
		`{"username":"u2","password":"newpw123"}`, jsonHeaders(), nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("新密码登录 = %d, want 200", resp.StatusCode)
	}
}

// TestProtectLastAdmin 最后一个可用 admin 不允许禁用/降级（409）。
func TestProtectLastAdmin(t *testing.T) {
	_, ts := setup(t)
	setupAdmin(t, ts)
	admin := loginCookie(t, ts)

	me := readBody(t, doJSON(t, http.MethodGet, ts.URL+"/api/auth/me", "", nil, admin))
	var meView struct {
		User api.UserView `json:"user"`
	}
	if err := json.Unmarshal([]byte(me), &meView); err != nil {
		t.Fatal(err)
	}
	id := meView.User.ID

	if resp := doJSON(t, http.MethodPatch, ts.URL+"/api/users/"+itoa(id),
		`{"disabled":true}`, jsonHeaders(), admin); resp.StatusCode != http.StatusConflict {
		t.Fatalf("禁最后 admin = %d, want 409", resp.StatusCode)
	}
	if resp := doJSON(t, http.MethodPatch, ts.URL+"/api/users/"+itoa(id),
		`{"role":"operator"}`, jsonHeaders(), admin); resp.StatusCode != http.StatusConflict {
		t.Fatalf("降级最后 admin = %d, want 409", resp.StatusCode)
	}

	// 建第二个 admin 后，原 admin 可被禁用。
	if resp := doJSON(t, http.MethodPost, ts.URL+"/api/users",
		`{"username":"admin2","password":"pw123456","role":"admin"}`, jsonHeaders(), admin); resp.StatusCode != http.StatusOK {
		t.Fatalf("create admin2 = %d", resp.StatusCode)
	}
	if resp := doJSON(t, http.MethodPatch, ts.URL+"/api/users/"+itoa(id),
		`{"disabled":true}`, jsonHeaders(), admin); resp.StatusCode != http.StatusOK {
		t.Fatalf("双 admin 禁用原 admin = %d, want 200", resp.StatusCode)
	}
}

// TestLegacyTokenDefaultAdmin legacy -token 仍是 default 租户 admin（内部标记 Legacy）。
func TestLegacyTokenDefaultAdmin(t *testing.T) {
	srv, err := newTokenServer(t, "sekret")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.CloseAll()
	tokTS := httptest.NewServer(srv.Routes())
	defer tokTS.Close()

	if resp := doJSON(t, http.MethodGet, tokTS.URL+"/api/users", "", bearer("sekret"), nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("legacy token 管用户 = %d, want 200", resp.StatusCode)
	}
	me := doJSON(t, http.MethodGet, tokTS.URL+"/api/auth/me", "", bearer("sekret"), nil)
	raw := readBody(t, me)
	var out struct {
		User api.UserView `json:"user"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	if out.User.Role != string(api.RoleAdmin) || out.User.TenantSlug != "default" {
		t.Fatalf("legacy token 身份 = %+v", out.User)
	}

	// 无 token 的 /api/users 应 401（requireAdmin 始终鉴权）。
	if resp := doJSON(t, http.MethodGet, tokTS.URL+"/api/users", "", nil, nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("无凭据管用户 = %d, want 401", resp.StatusCode)
	}
}

func newTokenServer(t *testing.T, token string) (*Server, error) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { st.Close() })
	return New(Config{Store: st, Version: "test", Token: token}), nil
}

func loginAs(t *testing.T, ts *httptest.Server, username, password string) []*http.Cookie {
	t.Helper()
	resp := doJSON(t, http.MethodPost, ts.URL+"/api/auth/login",
		`{"username":"`+username+`","password":"`+password+`"}`, jsonHeaders(), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login %s = %d", username, resp.StatusCode)
	}
	return resp.Cookies()
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }

// TestAccountModeRejectsUnauthenticatedEdge 账号模式下（已有 user）即使无 legacy token，
// edge hello 无凭据必须 policy violation 断开；租户令牌(scope=edge)可接入。
func TestAccountModeRejectsUnauthenticatedEdge(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "account.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hash, err := auth.HashPassword("pw123456")
	if err != nil {
		t.Fatal(err)
	}
	if _, created, err := st.CreateInitialAdmin("admin", "管理员", hash); err != nil || !created {
		t.Fatalf("setup admin: created=%v err=%v", created, err)
	}
	srv := New(Config{Store: st, Version: "test"}) // 无 legacy Token
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(func() { ts.Close(); srv.CloseAll(); time.Sleep(50 * time.Millisecond) })

	// 无凭据 edge → 断开（policy violation）。
	ws := dial(t, wsURL(ts.URL, "/ws/edge"))
	writeEnv(t, ws, api.Envelope{
		V: api.Version, Type: api.MsgHello,
		Data: rawData(t, api.HelloData{EdgeID: "anon",
			Devices: []api.DeviceMeta{{ID: "d1", Adapter: "stcb"}}}),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, _, err := ws.Read(ctx); err == nil {
		t.Fatal("账号模式无凭据 edge 应被断开")
	} else if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatalf("断开状态 = %v, want policy violation", websocket.CloseStatus(err))
	}
	ws.CloseNow()

	// 租户令牌(scope=edge)可接入。
	defID, err := st.EnsureDefaultTenant()
	if err != nil {
		t.Fatal(err)
	}
	plain, tokenHash, prefix, err := auth.NewTenantToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateTenantToken(defID, "edge", tokenHash, prefix, `["edge"]`, nil); err != nil {
		t.Fatal(err)
	}
	ws2 := dial(t, wsURL(ts.URL, "/ws/edge"))
	writeEnv(t, ws2, api.Envelope{
		V: api.Version, Type: api.MsgHello,
		Data: rawData(t, api.HelloData{EdgeID: "tok", Token: plain,
			Devices: []api.DeviceMeta{{ID: "d1", Adapter: "stcb"}}}),
	})
	time.Sleep(200 * time.Millisecond)
	srv.mu.RLock()
	_, ok := srv.edges["tok"]
	srv.mu.RUnlock()
	if !ok {
		t.Fatal("tenant token(edge) 未接入")
	}
	ws2.CloseNow()
}
