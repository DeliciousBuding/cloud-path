package audit

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestAuditMetadataRedaction 锁定敏感 key 红线：password/token/secret/cookie/authorization/session
// 一律 [REDACTED]，非敏感 key 原样保留。
func TestAuditMetadataRedaction(t *testing.T) {
	m := NewMetadata().
		String("username", "admin").
		String("password", "supersecret").
		String("access_token", "cp_plain").
		String("x_secret", "hunter2").
		String("Cookie", "cp_session=abc").
		String("Authorization", "Bearer xyz").
		String("session_id", "sess-1").
		String("scopes", "read,write")

	raw := m.JSON()
	if strings.Contains(raw, "supersecret") || strings.Contains(raw, "cp_plain") ||
		strings.Contains(raw, "hunter2") || strings.Contains(raw, "cp_session=abc") ||
		strings.Contains(raw, "Bearer xyz") || strings.Contains(raw, "sess-1") {
		t.Fatalf("metadata 泄露敏感值: %s", raw)
	}
	if strings.Count(raw, Redacted) != 6 {
		t.Fatalf("应有 6 个 [REDACTED], got %s", raw)
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["username"] != "admin" || decoded["scopes"] != "read,write" {
		t.Fatalf("非敏感 key 未保留: %+v", decoded)
	}
	for _, k := range []string{"password", "access_token", "x_secret", "Cookie", "Authorization", "session_id"} {
		if decoded[k] != Redacted {
			t.Fatalf("敏感 key %q 应为 [REDACTED], got %v", k, decoded[k])
		}
	}
}

// TestAuditRequestIDNormalize 锁定请求 ID 校验：合法放行、超长/非法字符拒绝。
func TestAuditRequestIDNormalize(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"req-abc_123.XYZ", "req-abc_123.XYZ"},
		{"  padded  ", "padded"},
		{"", ""},
		{strings.Repeat("a", MaxRequestIDLen+1), ""},
		{"bad id", ""},
		{"x\nline", ""},
		{"ok-1", "ok-1"},
	}
	for _, c := range cases {
		if got := NormalizeRequestID(c.in); got != c.want {
			t.Fatalf("NormalizeRequestID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if id := NewRequestID(); !strings.HasPrefix(id, "req-") || len(id) < 16 {
		t.Fatalf("NewRequestID = %q", id)
	}
}
