package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeliciousBuding/cloud-path/internal/auth"
	"github.com/DeliciousBuding/cloud-path/internal/store"
)

// TestRemoteSetupRejectedWithoutToken 非回环且未配置 setup token → 403，且不落库。
func TestRemoteSetupRejectedWithoutToken(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv := New(Config{Store: st, Version: "test"})
	h := srv.Routes()

	req := httptest.NewRequest(http.MethodPost, "/api/auth/setup",
		strings.NewReader(`{"username":"admin","password":"secret123"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "203.0.113.9:43210"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("非回环无 token setup = %d, want 403", rec.Code)
	}
	if n, err := st.CountUsers(); err != nil || n != 0 {
		t.Fatalf("users = %d err=%v, want 0", n, err)
	}
}

// TestRemoteSetupOneTimeToken 非回环 + 一次性 token：错 token 403、对 token 200、
// 复用 403；回环不带 token 仍放行（保持本机首装语义）。
func TestRemoteSetupOneTimeToken(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv := New(Config{Store: st, Version: "test", SetupToken: "one-time-secret"})
	h := srv.Routes()

	post := func(remote, tokenHeader string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/setup",
			strings.NewReader(`{"username":"admin","password":"secret123","name":"管理员"}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = remote
		if tokenHeader != "" {
			req.Header.Set(setupTokenHeader, tokenHeader)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	if code := post("203.0.113.9:43210", "wrong"); code != http.StatusForbidden {
		t.Fatalf("错 setup token = %d, want 403", code)
	}
	if code := post("203.0.113.9:43210", "one-time-secret"); code != http.StatusOK {
		t.Fatalf("正确 setup token = %d, want 200", code)
	}
	if n, err := st.CountUsers(); err != nil || n != 1 {
		t.Fatalf("users = %d err=%v, want 1", n, err)
	}
	// 成功消费后复用必须失败（非回环仍带同 token）。
	if code := post("203.0.113.9:43210", "one-time-secret"); code != http.StatusForbidden {
		t.Fatalf("复用 setup token = %d, want 403", code)
	}

	// 回环仍放行（带不带 token 都行），但已 setup → 409。
	st2, err := store.Open(filepath.Join(t.TempDir(), "test2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	srv2 := New(Config{Store: st2, Version: "test", SetupToken: "one-time-secret"})
	h2 := srv2.Routes()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/setup",
		strings.NewReader(`{"username":"admin","password":"secret123"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "127.0.0.1:43210"
	rec := httptest.NewRecorder()
	h2.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("回环无 token setup = %d, want 200", rec.Code)
	}
}

// TestUntrustedXFFCannotBypassLoginRateLimit 直连（未可信反代）每次换 XFF，
// 仍按同一 socket IP 计桶，第 N 次必须 429。
func TestUntrustedXFFCannotBypassLoginRateLimit(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv := New(Config{Store: st, Version: "test", LoginRatePerMin: 5})
	h := srv.Routes()

	body := `{"username":"ghost","password":"whatever"}`
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "198.51.100.7:12345"
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i+1))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("第 %d 次登录 = %d, want 401", i+1, rec.Code)
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "198.51.100.7:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.99")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("换 XFF 后仍应命中同 IP 限流 = %d, want 429", rec.Code)
	}
}

// TestUnknownUserRunsDummyPasswordVerify 未知用户登录必须走固定 dummy Argon2
// 派生（与已知用户同等耗时量级），并返回与错误密码一致的 401。
func TestUnknownUserRunsDummyPasswordVerify(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv := New(Config{Store: st, Version: "test"})

	var dummyCalls, verifyCalls int
	srv.dummyVerify = func(password string) {
		dummyCalls++
		auth.DummyVerify(password)
	}
	srv.verifyPassword = func(hash, password string) bool {
		verifyCalls++
		return auth.VerifyPassword(hash, password)
	}

	h := srv.Routes()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login",
		strings.NewReader(`{"username":"ghost","password":"whatever"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "127.0.0.1:43210"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("未知用户登录 = %d, want 401", rec.Code)
	}
	if dummyCalls != 1 {
		t.Fatalf("未知用户应执行 1 次 dummy verify，got %d", dummyCalls)
	}
	if verifyCalls != 0 {
		t.Fatalf("未知用户不应调用真实 verify，got %d", verifyCalls)
	}
}
