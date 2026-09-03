package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeliciousBuding/cloud-path/internal/audit"
	"github.com/DeliciousBuding/cloud-path/internal/auth"
	"github.com/DeliciousBuding/cloud-path/internal/store"
)

// D4（首装放行判据）反向测试：判据必须是 trusted-proxy 感知的真实客户端 IP，
// 而不是 TCP 对端地址。生产形态是同机 nginx 反代（proxy_pass http://127.0.0.1:<port>），
// 公网访客到达本进程时对端恒为 127.0.0.1，旧判据等于把首装开放给整个公网。

const setupBody = `{"username":"admin","password":"secret123","name":"管理员"}`

// newSetupTarget 构造只用于首装判定的 server（可注入 trusted proxies / setup token）。
func newSetupTarget(t *testing.T, proxies *auth.TrustedProxies, setupToken string) (*Server, *store.Store, http.Handler) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "setup.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv := New(Config{Store: st, Version: "test", SetupToken: setupToken, TrustedProxies: proxies})
	t.Cleanup(func() { srv.CloseAll() })
	return srv, st, srv.Routes()
}

// doSetup 以给定 TCP 对端与请求头调用 /api/auth/setup。
func doSetup(h http.Handler, remote string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", strings.NewReader(setupBody))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = remote
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// loopbackProxies 是 jp1 现网形态的 trusted proxies 配置（nginx 与 server 同机）。
func loopbackProxies(t *testing.T) *auth.TrustedProxies {
	t.Helper()
	tp, err := auth.ParseTrustedProxies([]string{"127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	if tp == nil {
		t.Fatal("trusted proxies 未解析")
	}
	return tp
}

func userCount(t *testing.T, st *store.Store) int64 {
	t.Helper()
	n, err := st.CountUsers()
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// TestSetupRejectsForgedLoopbackForwarding 锁定：非信任代理来源伪造
// X-Forwarded-For / X-Real-IP / Forwarded = 127.0.0.1 一律不得放行。
func TestSetupRejectsForgedLoopbackForwarding(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
	}{
		{"XFF", map[string]string{"X-Forwarded-For": "127.0.0.1"}},
		{"XFF-chain", map[string]string{"X-Forwarded-For": "127.0.0.1, 127.0.0.1"}},
		{"X-Real-IP", map[string]string{"X-Real-IP": "127.0.0.1"}},
		{"Forwarded", map[string]string{"Forwarded": `for=127.0.0.1`}},
		{"IPv6-loopback", map[string]string{"X-Forwarded-For": "::1"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, st, h := newSetupTarget(t, nil, "")
			rec := doSetup(h, "203.0.113.9:43210", c.headers)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("伪造 %s 回环 = %d, want 403", c.name, rec.Code)
			}
			if n := userCount(t, st); n != 0 {
				t.Fatalf("被拒的首装却建号了: users=%d", n)
			}
			if strings.Contains(rec.Body.String(), "already") {
				t.Fatalf("403 响应泄漏了初始化状态: %s", rec.Body.String())
			}
		})
	}
}

// TestSetupRejectsTrustedProxyPublicClient 锁定 jp1 现网形态
// （CLOUDPATH_TRUSTED_PROXIES=127.0.0.1 + 同机 nginx）：公网访客必须被挡。
func TestSetupRejectsTrustedProxyPublicClient(t *testing.T) {
	// 1) nginx 如实追加真实客户端 IP。
	_, st, h := newSetupTarget(t, loopbackProxies(t), "")
	if rec := doSetup(h, "127.0.0.1:5555", map[string]string{
		"X-Forwarded-For": "203.0.113.7", "X-Real-IP": "203.0.113.7",
	}); rec.Code != http.StatusForbidden {
		t.Fatalf("信任代理转发的公网来源 = %d, want 403", rec.Code)
	}
	if n := userCount(t, st); n != 0 {
		t.Fatalf("被拒的首装却建号了: users=%d", n)
	}

	// 2) 更狠的一种：代理把客户端伪造的回环 XFF 原样透传（不追加）。
	//    旧判据看 TCP 对端 = 127.0.0.1 会放行；现在带转发头即不按本机处理。
	for _, headers := range []map[string]string{
		{"X-Forwarded-For": "127.0.0.1"},
		{"X-Real-IP": "127.0.0.1"},
		{"X-Forwarded-For": "203.0.113.7, 127.0.0.1"},
	} {
		_, st2, h2 := newSetupTarget(t, loopbackProxies(t), "")
		if rec := doSetup(h2, "127.0.0.1:5555", headers); rec.Code != http.StatusForbidden {
			t.Fatalf("信任代理透传伪造回环头 %v = %d, want 403", headers, rec.Code)
		}
		if n := userCount(t, st2); n != 0 {
			t.Fatalf("被拒的首装却建号了: users=%d", n)
		}
	}
}

// TestSetupAllowsDirectLoopback 锁定正向路径：本机直连（无任何转发头）放行，
// 且在生产 trusted-proxies 配置下同样放行（不影响正常首装）。
func TestSetupAllowsDirectLoopback(t *testing.T) {
	for _, remote := range []string{"127.0.0.1:43210", "[::1]:43210"} {
		_, st, h := newSetupTarget(t, nil, "")
		if rec := doSetup(h, remote, nil); rec.Code != http.StatusOK {
			t.Fatalf("本机直连 %s = %d, want 200 body=%s", remote, rec.Code, rec.Body.String())
		}
		if n := userCount(t, st); n != 1 {
			t.Fatalf("users = %d, want 1", n)
		}
	}
	// 生产配置（trusted proxies 含 127.0.0.1）下的本机直连：无转发头 → 仍放行。
	_, st, h := newSetupTarget(t, loopbackProxies(t), "")
	if rec := doSetup(h, "127.0.0.1:43210", nil); rec.Code != http.StatusOK {
		t.Fatalf("生产配置下本机直连 = %d, want 200 body=%s", rec.Code, rec.Body.String())
	}
	if n := userCount(t, st); n != 1 {
		t.Fatalf("users = %d, want 1", n)
	}
}

// TestSetupAfterInitRejectedRegardlessOfSource 锁定要求 2：已初始化后 setup 恒定拒绝
// （幂等 409/403），与来源无关。
//
// A) 经 API 完成首装：一次性 setup token 随之失效（既有语义），此后任何来源都被拒；
// B) 已初始化实例「重启后」（token 从未消费）：授权来源也必须 409，
//
//	且 409 路径不得消耗一次性 token。
func TestSetupAfterInitRejectedRegardlessOfSource(t *testing.T) {
	// ---- A) API 首装后 ----
	srv, st, h := newSetupTarget(t, loopbackProxies(t), "one-time-secret")
	if rec := doSetup(h, "127.0.0.1:43210", nil); rec.Code != http.StatusOK {
		t.Fatalf("首装 = %d body=%s", rec.Code, rec.Body.String())
	}
	if !srv.setupTokenUsed.Load() {
		t.Fatal("首装成功后一次性 setup token 必须失效")
	}
	// 本机直连：已初始化 → 409（不得重置/抢注）。
	if rec := doSetup(h, "127.0.0.1:43210", nil); rec.Code != http.StatusConflict {
		t.Fatalf("已初始化后本机 setup = %d, want 409", rec.Code)
	}
	// 公网直连：403（且不泄漏初始化状态）。
	rec := doSetup(h, "203.0.113.9:43210", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("已初始化后公网 setup = %d, want 403", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "already") {
		t.Fatalf("未授权来源不应探测到初始化状态: %s", rec.Body.String())
	}
	// 伪造回环转发头：403。
	if rec := doSetup(h, "127.0.0.1:5555", map[string]string{"X-Forwarded-For": "127.0.0.1"}); rec.Code != http.StatusForbidden {
		t.Fatalf("已初始化后伪造回环头 = %d, want 403", rec.Code)
	}
	// 带（已随首装失效的）token 的公网来源：仍 403。
	if rec := doSetup(h, "203.0.113.9:43210", map[string]string{setupTokenHeader: "one-time-secret"}); rec.Code != http.StatusForbidden {
		t.Fatalf("已初始化后带失效 token 的 setup = %d, want 403", rec.Code)
	}
	if n := userCount(t, st); n != 1 {
		t.Fatalf("users = %d, want 1（不得被重置或抢注）", n)
	}
	row, err := st.GetUserByUsername("admin")
	if err != nil || row.Name != "管理员" {
		t.Fatalf("首个 admin 被改写: %+v err=%v", row, err)
	}

	// ---- B) 已初始化实例重启后（token 未消费）----
	st2, err := store.Open(filepath.Join(t.TempDir(), "initialized.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st2.Close() })
	hash, err := auth.HashPassword("secret123")
	if err != nil {
		t.Fatal(err)
	}
	if _, created, err := st2.CreateInitialAdmin("root", "既有管理员", hash); err != nil || !created {
		t.Fatalf("预置已初始化状态失败: created=%v err=%v", created, err)
	}
	srv2 := New(Config{Store: st2, Version: "test", SetupToken: "one-time-secret",
		TrustedProxies: loopbackProxies(t)})
	t.Cleanup(func() { srv2.CloseAll() })
	h2 := srv2.Routes()
	if !srv2.accountMode() {
		t.Fatal("已有用户的实例重启后必须处于账号模式")
	}
	// 本机直连 → 409。
	if rec := doSetup(h2, "127.0.0.1:43210", nil); rec.Code != http.StatusConflict {
		t.Fatalf("重启后本机 setup = %d, want 409", rec.Code)
	}
	// 授权来源（有效未消费 token）→ 同样 409，且 token 不被消耗（幂等）。
	for i := 0; i < 2; i++ {
		rec := doSetup(h2, "203.0.113.9:43210", map[string]string{setupTokenHeader: "one-time-secret"})
		if rec.Code != http.StatusConflict {
			t.Fatalf("重启后带有效 token 的 setup = %d, want 409", rec.Code)
		}
	}
	if srv2.setupTokenUsed.Load() {
		t.Fatal("409 路径消耗了一次性 setup token")
	}
	// 伪造回环转发头 → 403。
	if rec := doSetup(h2, "127.0.0.1:5555", map[string]string{"X-Forwarded-For": "127.0.0.1"}); rec.Code != http.StatusForbidden {
		t.Fatalf("重启后伪造回环头 = %d, want 403", rec.Code)
	}
	if n := userCount(t, st2); n != 1 {
		t.Fatalf("users = %d, want 1", n)
	}
	if row, err := st2.GetUserByUsername("root"); err != nil || row.Name != "既有管理员" {
		t.Fatalf("既有管理员被改写: %+v err=%v", row, err)
	}
}

// TestSetupRejectionsAreAudited 锁定：被拒的首装尝试必须留真实失败审计
// （不静默丢弃），且节流不会吞掉首条。
func TestSetupRejectionsAreAudited(t *testing.T) {
	_, st, h := newSetupTarget(t, loopbackProxies(t), "")
	if rec := doSetup(h, "127.0.0.1:5555", map[string]string{"X-Forwarded-For": "203.0.113.7"}); rec.Code != http.StatusForbidden {
		t.Fatalf("首装应被拒 = %d", rec.Code)
	}
	defaultID, err := st.EnsureDefaultTenant()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := st.ListAuditEvents(defaultID, 0, audit.ActionSetup, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Outcome != audit.OutcomeFailure ||
		!strings.Contains(rows[0].MetadataJSON, "not_local_client") {
		t.Fatalf("被拒首装未记失败审计: %+v err=%v", rows, err)
	}
	if rows[0].RemoteIP != "203.0.113.7" {
		t.Fatalf("审计应记录真实客户端 IP（trusted-proxy 感知）: %q", rows[0].RemoteIP)
	}
	// 节流：同一窗口内的第二次拒绝不再写审计，但请求仍被拒。
	if rec := doSetup(h, "127.0.0.1:5555", map[string]string{"X-Forwarded-For": "203.0.113.8"}); rec.Code != http.StatusForbidden {
		t.Fatalf("第二次拒绝 = %d, want 403", rec.Code)
	}
	rows, err = st.ListAuditEvents(defaultID, 0, audit.ActionSetup, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("被拒首装审计未节流: %d 条", len(rows))
	}

	// 本机直连首装成功 → 成功审计；再次首装 → 409 且记 already_initialized 失败审计。
	_, st2, h2 := newSetupTarget(t, nil, "")
	if rec := doSetup(h2, "127.0.0.1:43210", nil); rec.Code != http.StatusOK {
		t.Fatalf("首装 = %d", rec.Code)
	}
	if rec := doSetup(h2, "127.0.0.1:43210", nil); rec.Code != http.StatusConflict {
		t.Fatalf("二次首装 = %d, want 409", rec.Code)
	}
	id2, err := st2.EnsureDefaultTenant()
	if err != nil {
		t.Fatal(err)
	}
	rows2, err := st2.ListAuditEvents(id2, 0, audit.ActionSetup, 50)
	if err != nil {
		t.Fatal(err)
	}
	var ok, conflict int
	for _, row := range rows2 {
		switch {
		case row.Outcome == audit.OutcomeSuccess:
			ok++
		case strings.Contains(row.MetadataJSON, "already_initialized"):
			conflict++
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatalf("首装审计 = success %d / already_initialized %d, want 1/1 (%+v)", ok, conflict, rows2)
	}
}
