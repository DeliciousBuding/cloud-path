package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	_ "github.com/DeliciousBuding/cloud-path/examples/stcb"
	"github.com/DeliciousBuding/cloud-path/internal/api"
)

// TestPluginReconcileRepushesToOnlineEdge 锁定 reconcile 语义：
// 不改期望态因此**不增加 revision**，把当前完整快照重新下发给在线 Edge 并记成功审计；
// Edge 随后离线时同一路径必须明确失败（稳定码 plugin_edge_offline）而不是静默成功。
func TestPluginReconcileRepushesToOnlineEdge(t *testing.T) {
	st, srv, ts, mem, a, _ := setupPluginSync(t)
	admin := issueTenantToken(t, st, a, `["admin"]`)
	edgeTok := issueTenantToken(t, st, a, `["edge"]`)
	if rev := createInstance(t, ts, admin, "e1", "box1"); rev != 1 {
		t.Fatalf("revision = %d, want 1", rev)
	}

	ews := dialEdgeHello(t, ts, "e1", edgeTok, api.DeviceMeta{ID: "d1", Adapter: "stcb"})
	defer ews.CloseNow()
	ch := edgeReader(ews)
	waitEdgeLink(t, srv, "e1", a)
	first, ok := waitEnv(t, ch, api.MsgPluginDesired, 30*time.Second)
	if !ok {
		t.Fatal("hello 后未收到 desired")
	}

	resp := pluginREST(t, ts, admin, http.MethodPost, "/api/plugin-instances/box1/reconcile", `{"force":true}`)
	raw := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reconcile = %d body=%s", resp.StatusCode, raw)
	}
	var out api.PluginInstanceWriteResponse
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("decode reconcile: %v (%s)", err, raw)
	}
	if out.Revision != 1 || out.ID != "box1" || out.RequestID == "" {
		t.Fatalf("reconcile 响应错误（revision 不得变化）: %+v", out)
	}
	if rev, err := mem.PluginDesiredRevision(a, "e1"); err != nil || rev != 1 {
		t.Fatalf("reconcile 改变了 revision = %d err=%v, want 1", rev, err)
	}
	again, ok := waitEnv(t, ch, api.MsgPluginDesired, 30*time.Second)
	if !ok {
		t.Fatal("reconcile 未重新下发期望态")
	}
	if string(again.Data) != string(first.Data) {
		t.Fatalf("reconcile 下发的不是同一份完整快照:\n%s\n%s", first.Data, again.Data)
	}
	if !hasAudit(auditActions(t, st, a), actionPluginReconcile+":success") {
		t.Fatalf("reconcile 未记成功审计: %v", auditActions(t, st, a))
	}

	// Edge 断线后同一路径必须明确失败。
	ews.CloseNow()
	waitEdgeOffline(t, srv, "e1")
	resp = pluginREST(t, ts, admin, http.MethodPost, "/api/plugin-instances/box1/reconcile", `{}`)
	raw = readBody(t, resp)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("离线 reconcile = %d, want 409 body=%s", resp.StatusCode, raw)
	}
	var e struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal([]byte(raw), &e); err != nil || e.Code != api.PluginErrEdgeOffline {
		t.Fatalf("离线 reconcile 错误码 = %q (%s)", e.Code, raw)
	}
	if !hasAudit(auditActions(t, st, a), actionPluginReconcile+":failure") {
		t.Fatalf("离线 reconcile 未记失败审计: %v", auditActions(t, st, a))
	}
	if rev, err := mem.PluginDesiredRevision(a, "e1"); err != nil || rev != 1 {
		t.Fatalf("离线 reconcile 改变了 revision = %d err=%v", rev, err)
	}
}
