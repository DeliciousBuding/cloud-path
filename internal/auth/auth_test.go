package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPasswordHashAndVerify(t *testing.T) {
	const pass = "correct horse battery staple"
	h1, err := HashPassword(pass)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := HashPassword(pass)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h1, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Fatalf("hash 前缀/参数不符: %q", h1)
	}
	if h1 == h2 {
		t.Fatal("两次哈希应因随机盐不同")
	}
	if strings.Contains(h1, pass) {
		t.Fatal("哈希不得泄露明文")
	}
	if !VerifyPassword(h1, pass) {
		t.Fatal("正确密码应通过")
	}
	if VerifyPassword(h1, "wrong") {
		t.Fatal("错误密码不得通过")
	}
	if VerifyPassword(h1, "") {
		t.Fatal("空密码不得通过")
	}
	if VerifyPassword("not-a-hash", pass) {
		t.Fatal("坏格式不得通过")
	}
	// 篡改参数（m 改成攻击者可控值）必须拒绝，防内存 DoS
	tampered := strings.Replace(h1, "$m=65536,", "$m=8,", 1)
	if VerifyPassword(tampered, pass) {
		t.Fatal("参数被篡改的哈希不得通过")
	}
}

func TestSessionID(t *testing.T) {
	id1, err := NewSessionID()
	if err != nil {
		t.Fatal(err)
	}
	id2, err := NewSessionID()
	if err != nil {
		t.Fatal(err)
	}
	if id1 == id2 {
		t.Fatal("会话 ID 不得重复")
	}
	if len(id1) != 43 {
		t.Fatalf("32 字节 base64url 应为 43 字符，got %d", len(id1))
	}
	if strings.ContainsAny(id1, "+/=") {
		t.Fatalf("会话 ID 应 base64url 无填充: %q", id1)
	}
}

func TestTokenOK(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/x?token=sekret", nil)
	if TokenOK(r, "") {
		t.Fatal("空令牌永不通过")
	}
	if !TokenOK(r, "sekret") {
		t.Fatal("query token 应通过")
	}
	if TokenOK(r, "other") {
		t.Fatal("错 token 不得通过")
	}
	r2 := httptest.NewRequest(http.MethodGet, "/x", nil)
	r2.Header.Set("Authorization", "Bearer  sekret ")
	if BearerToken(r2) != "sekret" {
		t.Fatalf("BearerToken = %q", BearerToken(r2))
	}
	if !TokenOK(r2, "sekret") {
		t.Fatal("Bearer token 应通过")
	}
}

func TestLoopbackAndClientIP(t *testing.T) {
	cases := []struct {
		remote string
		want   bool
	}{
		{"127.0.0.1:8080", true},
		{"[::1]:8080", true},
		{"192.168.1.5:8080", false},
		{"203.0.113.9:43210", false},
		{"no-port", false},
		{"", false},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = c.remote
		if got := IsLoopbackRemote(r); got != c.want {
			t.Errorf("IsLoopbackRemote(%q) = %v, want %v", c.remote, got, c.want)
		}
	}

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	if got := ClientIP(r); got != "127.0.0.1" {
		t.Fatalf("ClientIP = %q", got)
	}
	r.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.1")
	if got := ClientIP(r); got != "203.0.113.7" {
		t.Fatalf("ClientIP 应取 XFF 首项: %q", got)
	}
}

func TestRateLimiter(t *testing.T) {
	l := NewRateLimiter(2)
	if ok, _ := l.Allow("ip1"); !ok {
		t.Fatal("第 1 次应放行")
	}
	if ok, _ := l.Allow("ip1"); !ok {
		t.Fatal("第 2 次应放行")
	}
	ok, retry := l.Allow("ip1")
	if ok || retry <= 0 {
		t.Fatalf("第 3 次应超限: ok=%v retry=%v", ok, retry)
	}
	if ok, _ := l.Allow("ip2"); !ok {
		t.Fatal("其他 IP 不受影响")
	}
	// 窗口滑过（拨老最早命中）后恢复
	l.mu.Lock()
	for i := range l.hits["ip1"] {
		l.hits["ip1"][i] = l.hits["ip1"][i].Add(-2 * time.Minute)
	}
	l.mu.Unlock()
	if ok, _ := l.Allow("ip1"); !ok {
		t.Fatal("窗口滑过后应放行")
	}
}
