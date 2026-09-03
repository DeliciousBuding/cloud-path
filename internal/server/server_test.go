package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	_ "github.com/DeliciousBuding/cloud-path/examples/stcb" // 注册 stcb 适配器（命令白名单）
	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/store"
)

func setup(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	srv := New(Config{Store: st, Version: "test"})
	ts := httptest.NewServer(srv.Routes())
	// Cleanup 为 LIFO：先断 WS/关 server（触发离线落库），最后关 store
	t.Cleanup(func() { st.Close() })
	t.Cleanup(func() { ts.Close(); srv.CloseAll(); time.Sleep(50 * time.Millisecond) })
	return srv, ts
}

func wsURL(httpURL, path string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http") + path
}

func dial(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial %s: %v", url, err)
	}
	t.Cleanup(func() { ws.CloseNow() })
	return ws
}

func readEnvUntil(ctx context.Context, ws *websocket.Conn, typ api.MsgType) (api.Envelope, error) {
	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			return api.Envelope{}, err
		}
		var env api.Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			return api.Envelope{}, err
		}
		if env.Type == typ {
			return env, nil
		}
	}
}

func writeEnv(t *testing.T, ws *websocket.Conn, env api.Envelope) {
	t.Helper()
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := ws.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatalf("write %s: %v", env.Type, err)
	}
}

func rawData(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func getJSON(t *testing.T, url string, out any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("GET %s → %d", url, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatal(err)
	}
}

// TestWSFullLoop 覆盖核心链路：浏览器快照 → edge hello 注册 → 状态上报 fan-out + 落库
// → REST 命令下发 → edge 收到 command → ack 回执落库 → 浏览器收到 ack。
func TestWSFullLoop(t *testing.T) {
	_, ts := setup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 130*time.Second)
	defer cancel()

	// 1) 浏览器连接：首帧必须是 snapshot
	bws := dial(t, wsURL(ts.URL, "/ws"))
	snap, err := readEnvUntil(ctx, bws, api.MsgSnapshot)
	if err != nil {
		t.Fatalf("browser snapshot: %v", err)
	}
	var sd api.SnapshotData
	if err := json.Unmarshal(snap.Data, &sd); err != nil || len(sd.Devices) != 0 {
		t.Fatalf("bad snapshot: %v", sd)
	}

	// 2) edge 连接 + hello 注册
	ews := dial(t, wsURL(ts.URL, "/ws/edge"))
	writeEnv(t, ews, api.Envelope{
		V: api.Version, Type: api.MsgHello, Ts: time.Now().Unix(),
		Data: rawData(t, api.HelloData{
			EdgeID: "e1", Version: "test",
			Devices: []api.DeviceMeta{{ID: "d1", Adapter: "stcb", Name: "节点1", Port: "COM9"}},
		}),
	})

	// 浏览器应收到 edge_up
	if _, err := readEnvUntil(ctx, bws, api.MsgEdgeUp); err != nil {
		t.Fatalf("browser edge_up: %v", err)
	}

	// 3) edge 上报状态 → 浏览器 fan-out + REST 可见 + SQLite 落库
	writeEnv(t, ews, api.Envelope{
		V: api.Version, Type: api.MsgState, Device: "e1/d1", Ts: time.Now().Unix(),
		Data: rawData(t, api.StateData{Online: true,
			Raw: map[string]any{"hour": 12, "min": 34, "clock": "12:34"}, UpdatedAt: time.Now().Unix()}),
	})
	stEnv, err := readEnvUntil(ctx, bws, api.MsgState)
	if err != nil {
		t.Fatalf("browser state fanout: %v", err)
	}
	if stEnv.Device != "e1/d1" {
		t.Fatalf("state device = %q", stEnv.Device)
	}

	var devResp struct {
		Devices []api.DeviceView `json:"devices"`
	}
	getJSON(t, ts.URL+"/api/devices", &devResp)
	if len(devResp.Devices) != 1 {
		t.Fatalf("REST devices = %d", len(devResp.Devices))
	}
	dv := devResp.Devices[0]
	if dv.ID != "e1/d1" || !dv.Online || dv.Adapter != "stcb" || dv.State["clock"] != "12:34" {
		t.Fatalf("bad device view: %+v", dv)
	}

	// 4) 事件上报 → 落库 + fan-out
	writeEnv(t, ews, api.Envelope{
		V: api.Version, Type: api.MsgEvent, Device: "e1/d1", Ts: time.Now().Unix(),
		Data: rawData(t, api.EventData{Type: "REMIND"}),
	})
	if _, err := readEnvUntil(ctx, bws, api.MsgEvent); err != nil {
		t.Fatalf("browser event fanout: %v", err)
	}
	var evResp struct {
		Events []api.EventView `json:"events"`
	}
	getJSON(t, ts.URL+"/api/events?device=e1/d1", &evResp)
	if len(evResp.Events) != 1 || evResp.Events[0].Type != "REMIND" {
		t.Fatalf("events not persisted: %+v", evResp.Events)
	}

	// 5) REST 下发命令 → edge 收到 MsgCommand
	resp, err := http.Post(ts.URL+"/api/devices/e1/d1/commands",
		"application/json", bytes.NewReader([]byte(`{"cmd":"dump"}`)))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("POST command: %v %v", err, resp)
	}
	var cv api.CommandView
	json.NewDecoder(resp.Body).Decode(&cv)
	resp.Body.Close()
	if cv.ID == 0 || cv.Status != "sent" {
		t.Fatalf("bad command view: %+v", cv)
	}
	cmdEnv, err := readEnvUntil(ctx, ews, api.MsgCommand)
	if err != nil {
		t.Fatalf("edge command: %v", err)
	}
	var cd api.CommandData
	json.Unmarshal(cmdEnv.Data, &cd)
	if cd.CommandID != cv.ID || cd.Cmd != "dump" {
		t.Fatalf("bad command data: %+v", cd)
	}

	// 6) edge 回 ack → 落库 + 浏览器 fan-out
	writeEnv(t, ews, api.Envelope{
		V: api.Version, Type: api.MsgCommandAck, Device: "e1/d1", Ts: time.Now().Unix(),
		Data: rawData(t, api.AckData{CommandID: cv.ID, Status: "ok", Detail: ""}),
	})
	if _, err := readEnvUntil(ctx, bws, api.MsgCommandAck); err != nil {
		t.Fatalf("browser ack fanout: %v", err)
	}
	var cmdResp struct {
		Commands []api.CommandView `json:"commands"`
	}
	getJSON(t, ts.URL+"/api/commands?status=ok", &cmdResp)
	if len(cmdResp.Commands) != 1 || cmdResp.Commands[0].ID != cv.ID {
		t.Fatalf("ack not persisted: %+v", cmdResp.Commands)
	}

	// 7) 未知命令被白名单拒绝
	resp, err = http.Post(ts.URL+"/api/devices/e1/d1/commands",
		"application/json", bytes.NewReader([]byte(`{"cmd":"rm -rf /"}`)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("unknown command should be rejected, got %d", resp.StatusCode)
	}
}

// TestEdgeAuth 覆盖 token 鉴权：错误 token 拒连，正确 token 放行。
func TestEdgeAuth(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv := New(Config{Store: st, Version: "test", Token: "sekret"})
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 错误 token → hello 后被断开
	ws := dial(t, wsURL(ts.URL, "/ws/edge"))
	writeEnv(t, ws, api.Envelope{
		V: api.Version, Type: api.MsgHello,
		Data: rawData(t, api.HelloData{EdgeID: "bad", Token: "wrong",
			Devices: []api.DeviceMeta{{ID: "d1", Adapter: "stcb"}}}),
	})
	_, _, err = ws.Read(ctx)
	if err == nil {
		t.Fatal("wrong token should disconnect")
	}
	ws.CloseNow()

	// 正确 token → 正常
	ws2 := dial(t, wsURL(ts.URL, "/ws/edge"))
	writeEnv(t, ws2, api.Envelope{
		V: api.Version, Type: api.MsgHello,
		Data: rawData(t, api.HelloData{EdgeID: "good", Token: "sekret",
			Devices: []api.DeviceMeta{{ID: "d1", Adapter: "stcb"}}}),
	})
	// 存活证明：发一条 state 不会被打断（能继续读写即通过）
	writeEnv(t, ws2, api.Envelope{
		V: api.Version, Type: api.MsgState, Device: "good/d1",
		Data: rawData(t, api.StateData{Online: true, Raw: map[string]any{}, UpdatedAt: time.Now().Unix()}),
	})
	time.Sleep(200 * time.Millisecond)
	srv.mu.RLock()
	v, ok := srv.devices["good/d1"]
	online := ok && v.Online
	srv.mu.RUnlock()
	if !ok || !online {
		t.Fatalf("valid token edge not registered: ok=%v online=%v", ok, online)
	}
	ws2.CloseNow()
}

// TestHydrate 覆盖重启恢复：server 重建后从 SQLite 水合上次已知设备（离线态）。
func TestHydrate(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	srv1 := New(Config{Store: st, Version: "test"})
	if err := st.UpsertDevice("e1/d1", "e1", "stcb", "节点1", "COM9"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetState("e1/d1", `{"clock":"08:00"}`, true, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	srv1.mu.RLock()
	n := len(srv1.devices)
	srv1.mu.RUnlock()
	_ = n
	st.Close()

	// 模拟重启：同库新开 server
	st2, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	srv2 := New(Config{Store: st2, Version: "test"})
	srv2.mu.RLock()
	v, ok := srv2.devices["e1/d1"]
	var online bool
	var clock any
	var name string
	if ok {
		online = v.Online
		clock = v.State["clock"]
		name = v.Name
	}
	srv2.mu.RUnlock()
	if !ok {
		t.Fatal("device not hydrated after restart")
	}
	if online {
		t.Fatal("hydrated device must be offline until edge reconnects")
	}
	if clock != "08:00" || name != "节点1" {
		t.Fatalf("hydrated state lost: clock=%v name=%q", clock, name)
	}
}

func TestHealthz(t *testing.T) {
	_, ts := setup(t)
	var h api.HealthView
	getJSON(t, ts.URL+"/healthz", &h)
	if !h.OK || h.Version != "test" {
		t.Fatalf("bad health: %+v", h)
	}
}
