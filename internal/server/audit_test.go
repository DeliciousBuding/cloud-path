package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	_ "github.com/DeliciousBuding/cloud-path/examples/stcb"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/audit"
	"github.com/DeliciousBuding/cloud-path/internal/store"
)

func listAudit(t *testing.T, st *store.Store, tenantID int64, action string) []store.AuditEvent {
	t.Helper()
	rows, err := st.ListAuditEvents(tenantID, 0, action, 1000)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// auditContains 扫描本租户全部审计事件（含 metadata_json）是否出现目标明文。
func auditContains(t *testing.T, st *store.Store, tenantID int64, needle string) bool {
	t.Helper()
	rows, err := st.ListAuditEvents(tenantID, 0, "", 1000)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Contains(string(b), needle)
}

// TestLoginSuccessFailureAudited 锁定登录成功/失败审计：成功落 success，
// 错密码（已知用户）落该租户 failure，未知用户落 default failure，且不改变 401 响应语义。
func TestLoginSuccessFailureAudited(t *testing.T) {
	_, ts, st := setupUsersTokens(t)
	setupAdmin(t, ts)

	_ = loginCookie(t, ts) // 成功 → auth.login success

	if resp := doJSON(t, http.MethodPost, ts.URL+"/api/auth/login",
		`{"username":"admin","password":"wrong"}`, jsonHeaders(), nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("错密码 = %d, want 401", resp.StatusCode)
	}
	if resp := doJSON(t, http.MethodPost, ts.URL+"/api/auth/login",
		`{"username":"ghost","password":"whatever"}`, jsonHeaders(), nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("未知用户 = %d, want 401", resp.StatusCode)
	}

	defaultID, err := st.EnsureDefaultTenant()
	if err != nil {
		t.Fatal(err)
	}
	rows := listAudit(t, st, defaultID, audit.ActionLogin)
	if len(rows) != 3 {
		t.Fatalf("auth.login 事件 = %d, want 3: %+v", len(rows), rows)
	}
	var success, knownFail, unknownFail int
	for _, r := range rows {
		if r.TenantID != defaultID {
			t.Fatalf("登录审计跨租户泄漏: %+v", r)
		}
		switch {
		case r.Outcome == audit.OutcomeSuccess:
			success++
			if r.ActorName != "admin" || r.ActorID <= 0 {
				t.Fatalf("成功事件 actor 异常: %+v", r)
			}
		case r.ActorName == "admin":
			knownFail++
		case r.ActorName == "ghost":
			unknownFail++
			if r.ActorID != 0 {
				t.Fatalf("未知用户不应有 actor id: %+v", r)
			}
		}
	}
	if success != 1 || knownFail != 1 || unknownFail != 1 {
		t.Fatalf("登录审计分布异常 success=%d knownFail=%d unknownFail=%d", success, knownFail, unknownFail)
	}
}

// TestUserAndTokenActionsAudited 锁定用户/令牌管理动作审计，并反向验证 DB 不含明文密码/令牌。
func TestUserAndTokenActionsAudited(t *testing.T) {
	_, ts, st := setupUsersTokens(t)
	setupAdmin(t, ts)
	cookies := loginCookie(t, ts)
	defaultID, err := st.EnsureDefaultTenant()
	if err != nil {
		t.Fatal(err)
	}

	// 创建用户
	userPass := "user-secret-777"
	resp := doJSON(t, http.MethodPost, ts.URL+"/api/users",
		`{"username":"op1","name":"操作员","role":"operator","password":"`+userPass+`"}`,
		jsonHeaders(), cookies)
	raw := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create user = %d %s", resp.StatusCode, raw)
	}
	var created struct {
		User api.UserView `json:"user"`
	}
	if err := json.Unmarshal([]byte(raw), &created); err != nil || created.User.ID <= 0 {
		t.Fatalf("create user body = %s err=%v", raw, err)
	}
	uid := created.User.ID

	// 禁用用户
	if resp := doJSON(t, http.MethodPatch, ts.URL+"/api/users/"+itoa(uid),
		`{"disabled":true}`, jsonHeaders(), cookies); resp.StatusCode != http.StatusOK {
		t.Fatalf("disable user = %d", resp.StatusCode)
	}
	// 重置密码
	newPass := "user-new-888"
	if resp := doJSON(t, http.MethodPatch, ts.URL+"/api/users/"+itoa(uid),
		`{"password":"`+newPass+`"}`, jsonHeaders(), cookies); resp.StatusCode != http.StatusOK {
		t.Fatalf("reset password = %d", resp.StatusCode)
	}
	// 创建令牌
	tokenID, tokenSecret := createToken(t, ts, cookies, `{"name":"ci","scopes":["read","edge"]}`)
	// 吊销令牌
	if resp := doJSON(t, http.MethodDelete, ts.URL+"/api/tokens/"+itoa(tokenID),
		"", nil, cookies); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke token = %d", resp.StatusCode)
	}

	want := map[string]bool{
		audit.ActionUserCreate:        false,
		audit.ActionUserDisable:       false,
		audit.ActionUserPasswordReset: false,
		audit.ActionTokenCreate:       false,
		audit.ActionTokenRevoke:       false,
	}
	for _, r := range listAudit(t, st, defaultID, "") {
		if r.TenantID != defaultID {
			t.Fatalf("审计跨租户泄漏: %+v", r)
		}
		if _, ok := want[r.Action]; ok {
			want[r.Action] = true
		}
	}
	for action, seen := range want {
		if !seen {
			t.Fatalf("缺少审计动作 %s", action)
		}
	}

	// 反向验证：审计 DB/JSON 不得出现明文密码或明文令牌。
	for _, secret := range []string{userPass, newPass, tokenSecret} {
		if auditContains(t, st, defaultID, secret) {
			t.Fatalf("审计 DB 泄露明文: %q", secret)
		}
	}

	// 审计 API 响应也不得出现明文。
	ar := doJSON(t, http.MethodGet, ts.URL+"/api/audit", "", nil, cookies)
	araw := readBody(t, ar)
	if ar.StatusCode != http.StatusOK {
		t.Fatalf("list audit = %d", ar.StatusCode)
	}
	for _, secret := range []string{userPass, newPass, tokenSecret} {
		if strings.Contains(araw, secret) {
			t.Fatalf("审计 API 泄露明文: %q", secret)
		}
	}
	// 非敏感元数据仍可读（token.create 的 scopes）。
	if !strings.Contains(araw, "token.create") || !strings.Contains(araw, "user.disable") {
		t.Fatalf("审计 API 缺少事件: %s", araw)
	}
}

// TestCommandAuditDoesNotStoreArgs 锁定命令审计：accepted/rejected 都只记 cmd+reason，不记 args。
func TestCommandAuditDoesNotStoreArgs(t *testing.T) {
	srv, ts, st := setupUsersTokens(t)
	key := "e/d1"
	srv.mu.Lock()
	srv.devices[key] = &api.DeviceView{ID: key, EdgeID: "e", Adapter: "stcb", Online: true, State: map[string]any{}}
	srv.deviceTenants[key] = "default"
	srv.edges["e"] = &edgeLink{edgeID: "e", tenant: "default", devices: []string{key}, send: make(chan []byte, 8), cancel: func() {}}
	srv.mu.Unlock()

	secret := "args-super-secret-42"
	if resp := doJSON(t, http.MethodPost, ts.URL+"/api/devices/e/d1/commands",
		`{"cmd":"sync","args":"`+secret+`"}`, jsonHeaders(), nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("下发命令 = %d", resp.StatusCode)
	}
	// 被拒路径同样不落 args。
	if resp := doJSON(t, http.MethodPost, ts.URL+"/api/devices/e/ghost/commands",
		`{"cmd":"sync","args":"`+secret+`"}`, jsonHeaders(), nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("未知设备命令 = %d, want 404", resp.StatusCode)
	}

	defaultID, err := st.EnsureDefaultTenant()
	if err != nil {
		t.Fatal(err)
	}
	if got := listAudit(t, st, defaultID, audit.ActionCommandAccepted); len(got) != 1 {
		t.Fatalf("command.accepted = %d, want 1", len(got))
	}
	if got := listAudit(t, st, defaultID, audit.ActionCommandRejected); len(got) != 1 {
		t.Fatalf("command.rejected = %d, want 1", len(got))
	}
	if auditContains(t, st, defaultID, secret) {
		t.Fatalf("命令审计泄露 args: %q", secret)
	}
}

// TestEdgeAuthAudited 锁定 edge 鉴权成功/失败审计（坏 token 失败、edge scope token 成功）。
func TestEdgeAuthAudited(t *testing.T) {
	srv, ts, st := setupUsersTokens(t)
	setupAdmin(t, ts)
	cookies := loginCookie(t, ts)

	// 失败：非法租户令牌。
	bad := dial(t, wsURL(ts.URL, "/ws/edge"))
	writeEnv(t, bad, api.Envelope{V: api.Version, Type: api.MsgHello,
		Data: rawData(t, api.HelloData{EdgeID: "edge-bad", Token: "cp_badtoken", Version: "v1"})})
	rctx, rcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer rcancel()
	if _, _, rerr := bad.Read(rctx); rerr == nil {
		t.Fatal("invalid edge token 应被拒绝")
	}

	// 成功：edge scope 租户令牌。
	_, tokenSecret := createToken(t, ts, cookies, `{"name":"edge","scopes":["edge"]}`)
	good := dial(t, wsURL(ts.URL, "/ws/edge"))
	writeEnv(t, good, api.Envelope{V: api.Version, Type: api.MsgHello,
		Data: rawData(t, api.HelloData{EdgeID: "edge-good", Token: tokenSecret, Version: "v1",
			Devices: []api.DeviceMeta{{ID: "d1", Adapter: "stcb"}}})})

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		srv.mu.RLock()
		_, ok := srv.edges["edge-good"]
		srv.mu.RUnlock()
		if ok {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	srv.mu.RLock()
	_, ok := srv.edges["edge-good"]
	srv.mu.RUnlock()
	if !ok {
		t.Fatal("edge-good 未注册成功")
	}

	defaultID, err := st.EnsureDefaultTenant()
	if err != nil {
		t.Fatal(err)
	}
	fails := listAudit(t, st, defaultID, audit.ActionEdgeAuthFailure)
	if len(fails) == 0 {
		t.Fatal("缺少 edge.auth.failure")
	}
	found := false
	for _, r := range fails {
		if r.ActorName == "edge-bad" && r.Outcome == audit.OutcomeFailure {
			found = true
		}
	}
	if !found {
		t.Fatalf("未找到 edge-bad 失败审计: %+v", fails)
	}
	okays := listAudit(t, st, defaultID, audit.ActionEdgeAuthSuccess)
	if len(okays) != 1 || okays[0].ActorName != "edge-good" || okays[0].Outcome != audit.OutcomeSuccess {
		t.Fatalf("edge.auth.success 异常: %+v", okays)
	}
}

// TestAuditWriteFailureDoesNotChangeBusinessResult 锁定审计写失败不改变业务结果。
func TestAuditWriteFailureDoesNotChangeBusinessResult(t *testing.T) {
	srv, ts, st := setupUsersTokens(t)
	setupAdmin(t, ts)
	srv.auditWrite = func(audit.Event) error { return errors.New("audit down") }

	if resp := doJSON(t, http.MethodPost, ts.URL+"/api/auth/login",
		`{"username":"admin","password":"secret123"}`, jsonHeaders(), nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("审计写失败时登录 = %d, want 200", resp.StatusCode)
	}

	defaultID, err := st.EnsureDefaultTenant()
	if err != nil {
		t.Fatal(err)
	}
	if got := listAudit(t, st, defaultID, audit.ActionLogin); len(got) != 0 {
		t.Fatalf("审计写失败后不应有 auth.login 落库: %+v", got)
	}
}

// TestRequestIDRoundtrip 锁定请求 ID：接受合法 X-Request-ID 并回传；非法/缺失回退自生成。
func TestRequestIDRoundtrip(t *testing.T) {
	_, ts := setup(t)

	resp := doJSON(t, http.MethodGet, ts.URL+"/healthz", "",
		map[string]string{"X-Request-ID": "trace-abc.123"}, nil)
	if got := resp.Header.Get("X-Request-ID"); got != "trace-abc.123" {
		t.Fatalf("X-Request-ID = %q, want trace-abc.123", got)
	}

	resp = doJSON(t, http.MethodGet, ts.URL+"/healthz", "",
		map[string]string{"X-Request-ID": "bad id!"}, nil)
	got := resp.Header.Get("X-Request-ID")
	if got == "" || got == "bad id!" || !strings.HasPrefix(got, "req-") {
		t.Fatalf("非法 ID 应回退自生成, got %q", got)
	}

	resp = doJSON(t, http.MethodGet, ts.URL+"/healthz", "", nil, nil)
	if got := resp.Header.Get("X-Request-ID"); got == "" || !strings.HasPrefix(got, "req-") {
		t.Fatalf("缺失 ID 应自生成, got %q", got)
	}
}
