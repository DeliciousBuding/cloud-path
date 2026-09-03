package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/DeliciousBuding/cloud-path/internal/api"
)

// TestPluginWriteOverRealHTTPAuth 锁定真实 HTTP 鉴权链路（账号模式 + 租户令牌）：
// 未认证 401、viewer 只读 403、operator 可写 200、admin 可 purge。
func TestPluginWriteOverRealHTTPAuth(t *testing.T) {
	st, _, ts, _, a, _ := setupPluginSync(t)
	readTok := issueTenantToken(t, st, a, `["read"]`)
	writeTok := issueTenantToken(t, st, a, `["write"]`)
	adminTok := issueTenantToken(t, st, a, `["admin"]`)
	create := `{"edge_id":"e1","instance_id":"box1","plugin_id":"io.github.acme.driver","version":"0.1.0"}`

	// 未认证：账号模式必须 401（不因为回环来源放行）。
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/plugin-instances", strings.NewReader(create))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "127.0.0.1:1234"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	readBody(t, resp)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("未认证写 = %d, want 401", resp.StatusCode)
	}

	if resp := pluginREST(t, ts, readTok, http.MethodPost, "/api/plugin-instances", create); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer 写 = %d, want 403", resp.StatusCode)
	}
	resp = pluginREST(t, ts, writeTok, http.MethodPost, "/api/plugin-instances", create)
	raw := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("operator 写 = %d body=%s", resp.StatusCode, raw)
	}
	var out api.PluginInstanceWriteResponse
	if err := json.Unmarshal([]byte(raw), &out); err != nil || out.Revision != 1 {
		t.Fatalf("write response = %+v err=%v", out, err)
	}
	if resp := pluginREST(t, ts, writeTok, http.MethodDelete, "/api/plugin-instances/box1",
		`{"purge":true}`); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("operator purge = %d, want 403", resp.StatusCode)
	}
	if resp := pluginREST(t, ts, adminTok, http.MethodDelete, "/api/plugin-instances/box1",
		`{"purge":true}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("admin purge = %d", resp.StatusCode)
	}
}

// TestPluginWriteRequestIDRoundtrip 锁定 X-Request-ID：调用方提供的合法 id 必须
// 同时出现在响应头、响应体与审计行里（写操作可追溯到单次请求）。
func TestPluginWriteRequestIDRoundtrip(t *testing.T) {
	st, _, ts, _, a, _ := setupPluginSync(t)
	adminTok := issueTenantToken(t, st, a, `["admin"]`)
	const reqID = "req-lane-test-0001"

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/plugin-instances",
		strings.NewReader(`{"edge_id":"e1","instance_id":"box1","plugin_id":"p1","version":"1.0.0"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminTok)
	req.Header.Set("X-Request-ID", reqID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create = %d body=%s", resp.StatusCode, raw)
	}
	if got := resp.Header.Get("X-Request-ID"); got != reqID {
		t.Fatalf("响应头 X-Request-ID = %q, want %q", got, reqID)
	}
	var out api.PluginInstanceWriteResponse
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	if out.RequestID != reqID {
		t.Fatalf("响应体 request_id = %q, want %q", out.RequestID, reqID)
	}
	rows, err := st.ListAuditEvents(a, 0, actionPluginCreate, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("审计行 = %d err=%v, want 1", len(rows), err)
	}
	if rows[0].RequestID != reqID {
		t.Fatalf("审计 request_id = %q, want %q", rows[0].RequestID, reqID)
	}
	if rows[0].Outcome != "success" || !strings.Contains(rows[0].MetadataJSON, `"edge_id":"e1"`) {
		t.Fatalf("审计内容错误: %+v", rows[0])
	}
	// 非法 X-Request-ID 必须被服务端自生成 id 替换（不回显调用方的脏值）。
	req2, err := http.NewRequest(http.MethodPatch, ts.URL+"/api/plugin-instances/box1",
		strings.NewReader(`{"enabled":false}`))
	if err != nil {
		t.Fatal(err)
	}
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Authorization", "Bearer "+adminTok)
	req2.Header.Set("X-Request-ID", strings.Repeat("x", 400))
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	readBody(t, resp2)
	if got := resp2.Header.Get("X-Request-ID"); got == "" || strings.HasPrefix(got, "xxxx") {
		t.Fatalf("非法 request id 未被替换: %q", got)
	}
}
