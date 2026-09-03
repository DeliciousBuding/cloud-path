package auth

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUntrustedXFPDoesNotSetSecureCookie(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "203.0.113.7:43210"
	r.Header.Set("X-Forwarded-Proto", "https")

	rec := httptest.NewRecorder()
	SetSessionCookie(rec, r, "sid", 60, nil)
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %d, want 1", len(cookies))
	}
	if cookies[0].Secure {
		t.Fatal("未可信反代伪造 XFP=https 不得使 cookie Secure")
	}
}

func TestTrustedProxyHTTPSetsSecureCookie(t *testing.T) {
	tp, err := ParseTrustedProxies([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.2:1234"
	r.Header.Set("X-Forwarded-Proto", "https")

	rec := httptest.NewRecorder()
	SetSessionCookie(rec, r, "sid", 60, tp)
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].Secure {
		t.Fatalf("可信反代 XFP=https 应使 cookie Secure: %+v", cookies)
	}
}

func TestRealTLSSetsSecureCookie(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.TLS = &tls.ConnectionState{}
	rec := httptest.NewRecorder()
	SetSessionCookie(rec, r, "sid", 60, nil)
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].Secure {
		t.Fatalf("真实 TLS 应使 cookie Secure: %+v", cookies)
	}
}
