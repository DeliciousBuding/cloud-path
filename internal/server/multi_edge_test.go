package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"

	_ "github.com/DeliciousBuding/cloud-path/examples/demo"
	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/model"
	"github.com/DeliciousBuding/cloud-path/internal/store"
)

// setupMultiEdge 构造多 Edge 测试底座：真实 SQLite + 真实 WS + 账号模式（租户令牌）。
func setupMultiEdge(t *testing.T) (*store.Store, *Server, *httptest.Server, int64, int64) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "multiedge.db"))
	if err != nil {
		t.Fatal(err)
	}
	a := ensureTenantSlug(t, st, "tenant-a")
	b := ensureTenantSlug(t, st, "tenant-b")
	srv := New(Config{Store: st, Version: "test", RequireAuth: true})
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(func() { ts.Close(); srv.CloseAll(); time.Sleep(80 * time.Millisecond) })
	t.Cleanup(func() { st.Close() })
	return st, srv, ts, a, b
}

// reportOnline 让 edge 上报一台设备在线（真实 WS 状态消息）。
func reportOnline(t *testing.T, ws *websocket.Conn, key string, raw map[string]any) {
	t.Helper()
	writeEnv(t, ws, api.Envelope{V: api.Version, Type: api.MsgState, Device: key,
		Ts: time.Now().Unix(), Data: rawData(t, api.StateData{Online: true, Raw: raw, UpdatedAt: time.Now().Unix()})})
}

// reportDescriptor 让 edge 上报一台设备的 Descriptor。
func reportDescriptor(t *testing.T, ws *websocket.Conn, key string, desc model.Descriptor) {
	t.Helper()
	writeEnv(t, ws, api.Envelope{V: api.Version, Type: api.MsgDescriptor, Device: key,
		Ts: time.Now().Unix(), Data: rawData(t, desc)})
}

// waitEdgeOffline 等待某 edge 从在线集合摘除（真实断线清理已完成）。
func waitEdgeOffline(t *testing.T, srv *Server, edgeID string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		srv.mu.RLock()
		_, online := srv.edges[edgeID]
		srv.mu.RUnlock()
		if !online {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("edge %q 未在 5s 内下线", edgeID)
}

// deviceOnline 读取内存态在线标记。
func deviceOnline(srv *Server, key string) bool {
	srv.mu.RLock()
	defer srv.mu.RUnlock()
	v := srv.devices[key]
	return v != nil && v.Online
}

// listEdges 读 GET /api/edges。
func listEdges(t *testing.T, ts *httptest.Server, token string) []api.EdgeView {
	t.Helper()
	resp := doJSON(t, http.MethodGet, ts.URL+"/api/edges", "", bearerJSON(token), nil)
	raw := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/edges = %d body=%s", resp.StatusCode, raw)
	}
	var out struct {
		Edges []api.EdgeView `json:"edges"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	return out.Edges
}

// TestTwoEdgesIndependentCommandRouting 锁定暗卷 7：两个 Edge 同时在线、各自带设备
// （甚至同短名设备），命令只到目标设备所在 Edge；ack 也只能由拥有该设备的 Edge 提交。
func TestTwoEdgesIndependentCommandRouting(t *testing.T) {
	st, srv, ts, a, _ := setupMultiEdge(t)
	edgeTok := issueTenantToken(t, st, a, `["edge"]`)
	writeTok := issueTenantToken(t, st, a, `["write"]`)
	readTok := issueTenantToken(t, st, a, `["read"]`)

	// 两台 Edge 都用短名 d1：路由键必须是 "<edge_id>/<device_id>"，不得串线。
	ws1 := dialEdgeHello(t, ts, "e1", edgeTok, api.DeviceMeta{ID: "d1", Adapter: "demo"}, api.DeviceMeta{ID: "d2", Adapter: "demo"})
	defer ws1.CloseNow()
	ch1 := edgeReader(ws1)
	ws2 := dialEdgeHello(t, ts, "e2", edgeTok, api.DeviceMeta{ID: "d1", Adapter: "demo"}, api.DeviceMeta{ID: "d2", Adapter: "demo"})
	defer ws2.CloseNow()
	ch2 := edgeReader(ws2)
	waitEdgeLink(t, srv, "e1", a)
	waitEdgeLink(t, srv, "e2", a)
	reportOnline(t, ws1, "e1/d1", map[string]any{"clock": "10:00"})
	reportOnline(t, ws1, "e1/d2", map[string]any{"clock": "10:00"})
	reportOnline(t, ws2, "e2/d1", map[string]any{"clock": "11:00"})
	reportOnline(t, ws2, "e2/d2", map[string]any{"clock": "11:00"})
	waitDeviceOnline(t, srv, "e1/d1")
	waitDeviceOnline(t, srv, "e2/d2")

	// 1) 下发到 e1/d1：只有 e1 收到。
	resp := doJSON(t, http.MethodPost, ts.URL+"/api/devices/e1/d1/commands", `{"cmd":"ping"}`, bearerJSON(writeTok), nil)
	raw := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("command e1/d1 = %d body=%s", resp.StatusCode, raw)
	}
	var cv api.CommandView
	if err := json.Unmarshal([]byte(raw), &cv); err != nil || cv.Status != "sent" || cv.DeviceID != "e1/d1" {
		t.Fatalf("command view = %+v err=%v", cv, err)
	}
	env1, ok := waitEnv(t, ch1, api.MsgCommand, 30*time.Second)
	if !ok {
		t.Fatal("e1 未收到命令")
	}
	if env1.Device != "e1/d1" {
		t.Fatalf("命令路由键 = %q, want e1/d1", env1.Device)
	}
	if _, leaked := waitEnv(t, ch2, api.MsgCommand, 400*time.Millisecond); leaked {
		t.Fatal("命令串到了 e2")
	}

	// 2) 下发到 e2/d1（同短名不同 edge）：只有 e2 收到。
	resp = doJSON(t, http.MethodPost, ts.URL+"/api/devices/e2/d1/commands", `{"cmd":"dump"}`, bearerJSON(writeTok), nil)
	raw = readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("command e2/d1 = %d body=%s", resp.StatusCode, raw)
	}
	var cv2 api.CommandView
	if err := json.Unmarshal([]byte(raw), &cv2); err != nil {
		t.Fatal(err)
	}
	env2, ok := waitEnv(t, ch2, api.MsgCommand, 30*time.Second)
	if !ok || env2.Device != "e2/d1" {
		t.Fatalf("e2 未收到自己的命令: %+v ok=%v", env2, ok)
	}
	if _, leaked := waitEnv(t, ch1, api.MsgCommand, 400*time.Millisecond); leaked {
		t.Fatal("命令串到了 e1")
	}

	// 3) e2 冒名提交 e1/d1 的 ack：必须被忽略，原命令仍停在 sent。
	writeEnv(t, ws2, api.Envelope{V: api.Version, Type: api.MsgCommandAck, Device: "e1/d1",
		Ts: time.Now().Unix(), Data: rawData(t, api.AckData{CommandID: cv.ID, Status: "ok", Detail: "forged"})})
	// 4) e1 提交自己的 ack：状态推进到 ok。
	writeEnv(t, ws1, api.Envelope{V: api.Version, Type: api.MsgCommandAck, Device: "e1/d1",
		Ts: time.Now().Unix(), Data: rawData(t, api.AckData{CommandID: cv.ID, Status: "ok", Detail: "done"})})
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		r := doJSON(t, http.MethodGet, ts.URL+"/api/commands?device=e1/d1", "", bearerJSON(readTok), nil)
		body := readBody(t, r)
		var out struct {
			Commands []api.CommandView `json:"commands"`
		}
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatal(err)
		}
		if len(out.Commands) == 1 && out.Commands[0].Status == "ok" && out.Commands[0].Result == "done" {
			break
		}
		if time.Now().Add(20 * time.Millisecond).After(deadline) {
			t.Fatalf("命令状态未推进到 ok: %s", body)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// 5) 两台 Edge 都仍在线，互不影响。
	if len(srvEdges(srv)) != 2 {
		t.Fatalf("在线 edge 数 = %d, want 2", len(srvEdges(srv)))
	}
	for _, key := range []string{"e1/d1", "e1/d2", "e2/d1", "e2/d2"} {
		if !deviceOnline(srv, key) {
			t.Fatalf("设备 %q 意外离线", key)
		}
	}
}

func srvEdges(srv *Server) []string {
	srv.mu.RLock()
	defer srv.mu.RUnlock()
	out := make([]string, 0, len(srv.edges))
	for id := range srv.edges {
		out = append(out, id)
	}
	return out
}

// TestOneEdgeDisconnectDoesNotAffectOther 锁定暗卷 8：一台 Edge 断线不得踢掉另一台的
// 连接、不得改写其 online 状态、不得把其设备标离线；重连后自身状态真实恢复。
func TestOneEdgeDisconnectDoesNotAffectOther(t *testing.T) {
	st, srv, ts, a, _ := setupMultiEdge(t)
	edgeTok := issueTenantToken(t, st, a, `["edge"]`)
	writeTok := issueTenantToken(t, st, a, `["write"]`)
	readTok := issueTenantToken(t, st, a, `["read"]`)

	ws1 := dialEdgeHello(t, ts, "e1", edgeTok, api.DeviceMeta{ID: "d1", Adapter: "demo", Name: "节点1"})
	ch1 := edgeReader(ws1)
	ws2 := dialEdgeHello(t, ts, "e2", edgeTok, api.DeviceMeta{ID: "d1", Adapter: "demo", Name: "节点2"})
	defer ws2.CloseNow()
	ch2 := edgeReader(ws2)
	waitEdgeLink(t, srv, "e1", a)
	waitEdgeLink(t, srv, "e2", a)
	desc := model.Descriptor{DeviceID: "e1/d1", ExternalID: "d1", Status: model.DeviceOnline,
		Entities: []model.Entity{{EntityID: "clock", UniqueKey: "clock", Category: model.EntitySensor,
			Capabilities: []string{"cloudpath.dev/capability/clock@1"}}}}
	reportDescriptor(t, ws1, "e1/d1", desc)
	reportOnline(t, ws1, "e1/d1", map[string]any{"clock": "10:00"})
	reportOnline(t, ws2, "e2/d1", map[string]any{"clock": "11:00"})
	waitDeviceOnline(t, srv, "e1/d1")
	waitDeviceOnline(t, srv, "e2/d1")

	// e1 断线。
	ws1.CloseNow()
	waitEdgeOffline(t, srv, "e1")
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) && deviceOnline(srv, "e1/d1") {
		time.Sleep(10 * time.Millisecond)
	}
	if deviceOnline(srv, "e1/d1") {
		t.Fatal("e1 断线后其设备未标离线")
	}
	// 另一台完全不受影响：连接仍在、设备仍在线、仍能收命令。
	if len(srvEdges(srv)) != 1 || srvEdges(srv)[0] != "e2" {
		t.Fatalf("在线 edge = %v, want [e2]", srvEdges(srv))
	}
	if !deviceOnline(srv, "e2/d1") {
		t.Fatal("e1 断线把 e2 的设备标成了离线")
	}
	if resp := doJSON(t, http.MethodPost, ts.URL+"/api/devices/e2/d1/commands",
		`{"cmd":"ping"}`, bearerJSON(writeTok), nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("e1 断线后 e2 命令下发 = %d", resp.StatusCode)
	}
	if _, ok := waitEnv(t, ch2, api.MsgCommand, 30*time.Second); !ok {
		t.Fatal("e2 未收到命令（连接被误伤）")
	}
	// e1 的旧连接不会再收到任何东西。
	if _, ok := waitEnv(t, ch1, api.MsgCommand, 200*time.Millisecond); ok {
		t.Fatal("已断线的 e1 连接仍被投递")
	}
	// REST 视图：e1 离线、e2 在线。
	edges := listEdges(t, ts, readTok)
	if len(edges) != 2 {
		t.Fatalf("edges = %+v, want 2 条（含离线的 e1）", edges)
	}
	for _, e := range edges {
		switch e.EdgeID {
		case "e1":
			if e.Online {
				t.Fatalf("e1 仍显示在线: %+v", e)
			}
		case "e2":
			if !e.Online {
				t.Fatalf("e2 被误标离线: %+v", e)
			}
		}
	}

	// e1 重连（新 WS、同 edge_id）：在线态/设备列表/descriptor/状态真实恢复。
	ws1b := dialEdgeHello(t, ts, "e1", edgeTok, api.DeviceMeta{ID: "d1", Adapter: "demo", Name: "节点1"})
	defer ws1b.CloseNow()
	ch1b := edgeReader(ws1b)
	waitEdgeLink(t, srv, "e1", a)
	deadline = time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) && !deviceOnline(srv, "e1/d1") {
		time.Sleep(10 * time.Millisecond)
	}
	if !deviceOnline(srv, "e1/d1") {
		t.Fatal("重连后设备未恢复在线")
	}
	if !deviceOnline(srv, "e2/d1") {
		t.Fatal("e1 重连影响了 e2 的设备在线态")
	}
	// descriptor 未因断线丢失。
	resp := doJSON(t, http.MethodGet, ts.URL+"/api/devices/e1/d1/descriptor", "", bearerJSON(readTok), nil)
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("重连后 descriptor = %d body=%s", resp.StatusCode, body)
	}
	var dv struct {
		Descriptor model.Descriptor `json:"descriptor"`
	}
	if err := json.Unmarshal([]byte(body), &dv); err != nil {
		t.Fatal(err)
	}
	if dv.Descriptor.ExternalID != "d1" || len(dv.Descriptor.Entities) != 1 {
		t.Fatalf("descriptor 未恢复: %+v", dv.Descriptor)
	}
	// 状态在断线后仍是最后已知值，重连上报后立即刷新为真实值。
	reportOnline(t, ws1b, "e1/d1", map[string]any{"clock": "12:34"})
	deadline = time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		srv.mu.RLock()
		v := srv.devices["e1/d1"]
		clock, _ := v.State["clock"].(string)
		srv.mu.RUnlock()
		if clock == "12:34" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	srv.mu.RLock()
	v := srv.devices["e1/d1"]
	clock, _ := v.State["clock"].(string)
	srv.mu.RUnlock()
	if clock != "12:34" {
		t.Fatalf("重连后状态未刷新: %q", clock)
	}
	// 重连后命令照常下发到新连接。
	if resp := doJSON(t, http.MethodPost, ts.URL+"/api/devices/e1/d1/commands",
		`{"cmd":"dump"}`, bearerJSON(writeTok), nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("重连后命令下发 = %d", resp.StatusCode)
	}
	if _, ok := waitEnv(t, ch1b, api.MsgCommand, 30*time.Second); !ok {
		t.Fatal("重连后的新连接未收到命令")
	}
}

// TestCommandOfflineEdgeFailsExplicitly 锁定命令闭环的失败侧：
// Edge 离线时命令必须明确失败（不落库、不伪装成功），status 流转 sent→ok/timeout 真实可读。
func TestCommandOfflineEdgeFailsExplicitly(t *testing.T) {
	st, srv, ts, a, _ := setupMultiEdge(t)
	edgeTok := issueTenantToken(t, st, a, `["edge"]`)
	writeTok := issueTenantToken(t, st, a, `["write"]`)
	readTok := issueTenantToken(t, st, a, `["read"]`)

	// 从未接入过的设备 → 404（不得凭空创建命令）。
	if resp := doJSON(t, http.MethodPost, ts.URL+"/api/devices/nope/d1/commands",
		`{"cmd":"ping"}`, bearerJSON(writeTok), nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("未知设备命令 = %d, want 404", resp.StatusCode)
	}

	ws1 := dialEdgeHello(t, ts, "e1", edgeTok, api.DeviceMeta{ID: "d1", Adapter: "demo"})
	waitEdgeLink(t, srv, "e1", a)
	reportOnline(t, ws1, "e1/d1", map[string]any{"clock": "10:00"})
	waitDeviceOnline(t, srv, "e1/d1")

	resp := doJSON(t, http.MethodPost, ts.URL+"/api/devices/e1/d1/commands", `{"cmd":"ping"}`, bearerJSON(writeTok), nil)
	raw := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("命令下发 = %d body=%s", resp.StatusCode, raw)
	}
	var cv api.CommandView
	if err := json.Unmarshal([]byte(raw), &cv); err != nil || cv.Status != "sent" {
		t.Fatalf("command view = %+v err=%v", cv, err)
	}

	// 未 ack 的命令由 sweeper 的超时逻辑标为 timeout（真实服务端代码路径）。
	// created_at 是秒级，等 1.1s 让它真正落进 cutoff 之前。
	time.Sleep(1100 * time.Millisecond)
	if n, err := srv.timeoutOnce(0); err != nil || n != 1 {
		t.Fatalf("timeoutOnce = %d err=%v, want 1", n, err)
	}
	r := doJSON(t, http.MethodGet, ts.URL+"/api/commands?device=e1/d1", "", bearerJSON(readTok), nil)
	body := readBody(t, r)
	var out struct {
		Commands []api.CommandView `json:"commands"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Commands) != 1 || out.Commands[0].Status != "timeout" {
		t.Fatalf("超时状态未真实落库/可读: %s", body)
	}

	// Edge 断线后命令必须明确失败（409），且不产生新的命令行。
	ws1.CloseNow()
	waitEdgeOffline(t, srv, "e1")
	resp = doJSON(t, http.MethodPost, ts.URL+"/api/devices/e1/d1/commands", `{"cmd":"ping"}`, bearerJSON(writeTok), nil)
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("离线命令 = %d, want 409 body=%s", resp.StatusCode, body)
	}
	r = doJSON(t, http.MethodGet, ts.URL+"/api/commands?device=e1/d1", "", bearerJSON(readTok), nil)
	body = readBody(t, r)
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Commands) != 1 {
		t.Fatalf("离线命令被伪装落库: %s", body)
	}
	// 失败必须留审计。
	if !hasAudit(auditActions(t, st, a), "command.rejected:failure") {
		t.Fatalf("离线命令未记失败审计: %v", auditActions(t, st, a))
	}
}

// TestEdgeQuotaAndRateIsolation 补充：一台 Edge 的命令限流不得影响另一台 Edge 的设备。
func TestEdgeCommandRateIsolation(t *testing.T) {
	st, srv, ts, a, _ := setupMultiEdge(t)
	edgeTok := issueTenantToken(t, st, a, `["edge"]`)
	writeTok := issueTenantToken(t, st, a, `["write"]`)
	srv.cfg.CmdRatePerMin = 2

	ws1 := dialEdgeHello(t, ts, "e1", edgeTok, api.DeviceMeta{ID: "d1", Adapter: "demo"})
	defer ws1.CloseNow()
	ws2 := dialEdgeHello(t, ts, "e2", edgeTok, api.DeviceMeta{ID: "d1", Adapter: "demo"})
	defer ws2.CloseNow()
	edgeReader(ws1)
	edgeReader(ws2)
	waitEdgeLink(t, srv, "e1", a)
	waitEdgeLink(t, srv, "e2", a)
	reportOnline(t, ws1, "e1/d1", map[string]any{})
	reportOnline(t, ws2, "e2/d1", map[string]any{})
	waitDeviceOnline(t, srv, "e1/d1")
	waitDeviceOnline(t, srv, "e2/d1")

	codes := make([]int, 0, 3)
	for i := 0; i < 3; i++ {
		resp := doJSON(t, http.MethodPost, ts.URL+"/api/devices/e1/d1/commands",
			`{"cmd":"ping"}`, bearerJSON(writeTok), nil)
		readBody(t, resp)
		codes = append(codes, resp.StatusCode)
	}
	if codes[0] != http.StatusOK || codes[1] != http.StatusOK || codes[2] != http.StatusTooManyRequests {
		t.Fatalf("e1/d1 限流序列 = %v, want [200 200 429]", codes)
	}
	// 另一台 Edge 的设备不受该限流影响。
	if resp := doJSON(t, http.MethodPost, ts.URL+"/api/devices/e2/d1/commands",
		`{"cmd":"ping"}`, bearerJSON(writeTok), nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("e2/d1 被 e1/d1 的限流牵连 = %d", resp.StatusCode)
	}
}
