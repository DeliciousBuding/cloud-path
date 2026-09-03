package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	_ "github.com/DeliciousBuding/cloudpath/examples/stcb"
	"github.com/DeliciousBuding/cloudpath/internal/api"
	"github.com/DeliciousBuding/cloudpath/internal/store"
)

// registerEdge 走一次 hello 注册并等到 REST 可见（避免测试竞态）。
func registerEdge(t *testing.T, ts *httptest.Server, edgeID string, devs ...api.DeviceMeta) *websocket.Conn {
	t.Helper()
	ews := dial(t, wsURL(ts.URL, "/ws/edge"))
	writeEnv(t, ews, api.Envelope{
		V: api.Version, Type: api.MsgHello, Ts: time.Now().Unix(),
		Data: rawData(t, api.HelloData{EdgeID: edgeID, Version: "test", Devices: devs}),
	})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var resp struct {
			Devices []api.DeviceView `json:"devices"`
		}
		getJSON(t, ts.URL+"/api/devices", &resp)
		if len(resp.Devices) >= len(devs) {
			return ews
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("edge %s 未在 5s 内注册可见", edgeID)
	return nil
}

func postCommand(t *testing.T, url, body string) int {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func TestAdaptersEndpoint(t *testing.T) {
	_, ts := setup(t)
	var resp struct {
		Adapters []api.AdapterView `json:"adapters"`
	}
	getJSON(t, ts.URL+"/api/adapters", &resp)
	if len(resp.Adapters) != 1 || resp.Adapters[0].Name != "stcb" {
		t.Fatalf("adapters = %+v", resp.Adapters)
	}
	want := []string{"sync", "dump", "trigger", "open", "isp", "raw"}
	got := resp.Adapters[0].Commands
	if len(got) != len(want) {
		t.Fatalf("commands = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("commands = %v, want %v", got, want)
		}
	}
}

func TestStatsEndpoint(t *testing.T) {
	srv, ts := setup(t)
	registerEdge(t, ts, "e1", api.DeviceMeta{ID: "d1", Adapter: "stcb"})
	if _, err := srv.cfg.Store.AddEvent("e1/d1", "BOOT", "{}", time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	var st api.StatsView
	getJSON(t, ts.URL+"/api/stats", &st)
	if st.Devices != 1 || st.Events != 1 || st.SchemaVersion != 2 {
		t.Fatalf("stats = %+v", st)
	}
	if st.RetentionDays != defaultRetentionDays || st.AuthEnabled {
		t.Fatalf("stats 配置项 = %+v", st)
	}
	if st.OldestEvent == 0 {
		t.Fatal("oldest_event 应有值")
	}
}

// Store 为 nil 时（API-only）任何端点都不得 panic，必须优雅降级。
func TestNilStoreModeDoesNotPanic(t *testing.T) {
	srv := New(Config{Version: "test"})
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()
	defer srv.CloseAll()

	var evs struct {
		Events []api.EventView `json:"events"`
	}
	getJSON(t, ts.URL+"/api/events", &evs)
	if len(evs.Events) != 0 {
		t.Fatalf("events = %+v", evs.Events)
	}
	var cmds struct {
		Commands []api.CommandView `json:"commands"`
	}
	getJSON(t, ts.URL+"/api/commands", &cmds)
	if len(cmds.Commands) != 0 {
		t.Fatalf("commands = %+v", cmds.Commands)
	}
	var st api.StatsView
	getJSON(t, ts.URL+"/api/stats", &st)
	if st.Devices != 0 || st.Events != 0 {
		t.Fatalf("stats = %+v", st)
	}
	var h api.HealthView
	getJSON(t, ts.URL+"/healthz", &h)
	if !h.OK {
		t.Fatal("healthz 应正常")
	}

	// 设备已注册但无 store → 命令下发 503 而非 panic
	ews := dial(t, wsURL(ts.URL, "/ws/edge"))
	writeEnv(t, ews, api.Envelope{
		V: api.Version, Type: api.MsgHello, Ts: time.Now().Unix(),
		Data: rawData(t, api.HelloData{EdgeID: "e1", Version: "test",
			Devices: []api.DeviceMeta{{ID: "d1", Adapter: "stcb"}}}),
	})
	writeEnv(t, ews, api.Envelope{
		V: api.Version, Type: api.MsgState, Device: "e1/d1", Ts: time.Now().Unix(),
		Data: rawData(t, api.StateData{Online: true, Raw: map[string]any{}, UpdatedAt: time.Now().Unix()}),
	})
	deadline := time.Now().Add(3 * time.Second)
	code := 0
	for time.Now().Before(deadline) {
		code = postCommand(t, ts.URL+"/api/devices/e1/d1/commands", `{"cmd":"dump"}`)
		if code == http.StatusServiceUnavailable {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("无 store 时命令下发应 503，got %d", code)
}

func TestCommandRateLimit(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv := New(Config{Store: st, Version: "test", CmdRatePerMin: 3})
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()
	defer srv.CloseAll()
	registerEdge(t, ts, "e1", api.DeviceMeta{ID: "d1", Adapter: "stcb"})

	url := ts.URL + "/api/devices/e1/d1/commands"
	for i := 0; i < 3; i++ {
		if code := postCommand(t, url, `{"cmd":"dump"}`); code != http.StatusOK {
			t.Fatalf("第 %d 次命令 = %d, want 200", i+1, code)
		}
	}
	if code := postCommand(t, url, `{"cmd":"dump"}`); code != http.StatusTooManyRequests {
		t.Fatalf("超限额应 429，got %d", code)
	}
	// 窗口滑过（手工把命中时刻拨老）后应恢复
	srv.mu.Lock()
	for k := range srv.cmdHits {
		hits := srv.cmdHits[k]
		for i := range hits {
			hits[i] = hits[i].Add(-2 * time.Minute)
		}
	}
	srv.mu.Unlock()
	if code := postCommand(t, url, `{"cmd":"dump"}`); code != http.StatusOK {
		t.Fatalf("窗口滑过后应恢复 200，got %d", code)
	}
}

func TestCommandArgsValidation(t *testing.T) {
	_, ts := setup(t)
	registerEdge(t, ts, "e1", api.DeviceMeta{ID: "d1", Adapter: "stcb"})
	url := ts.URL + "/api/devices/e1/d1/commands"

	cases := []struct {
		name string
		body string
		want int
	}{
		{"空 body", ``, http.StatusBadRequest},
		{"坏 json", `{`, http.StatusBadRequest},
		{"缺 cmd", `{"args":"x"}`, http.StatusBadRequest},
		{"args 带换行", `{"cmd":"raw","args":"S\n"}`, http.StatusBadRequest},
		{"args 带 NUL", "{\"cmd\":\"raw\",\"args\":\"S\x00\"}", http.StatusBadRequest},
		{"args 过长", `{"cmd":"raw","args":"` + strings.Repeat("A", 65) + `"}`, http.StatusBadRequest},
		{"未知命令", `{"cmd":"reboot"}`, http.StatusBadRequest},
		{"合法 raw", `{"cmd":"raw","args":"S"}`, http.StatusOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if code := postCommand(t, url, c.body); code != c.want {
				t.Fatalf("%s: got %d, want %d", c.name, code, c.want)
			}
		})
	}
}

func TestCommandUnknownDeviceAndOfflineEdge(t *testing.T) {
	_, ts := setup(t)
	// 未注册设备 → 404（不得 nil 解引用）
	if code := postCommand(t, ts.URL+"/api/devices/nope/none/commands", `{"cmd":"dump"}`); code != http.StatusNotFound {
		t.Fatalf("未注册设备 = %d, want 404", code)
	}
	// 注册后 edge 断开 → 409
	ews := registerEdge(t, ts, "e1", api.DeviceMeta{ID: "d1", Adapter: "stcb"})
	ews.CloseNow()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if code := postCommand(t, ts.URL+"/api/devices/e1/d1/commands", `{"cmd":"dump"}`); code == http.StatusConflict {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatal("edge 断开后命令下发应 409")
}

func TestCommandsDeviceFilter(t *testing.T) {
	srv, ts := setup(t)
	registerEdge(t, ts, "e1",
		api.DeviceMeta{ID: "d1", Adapter: "stcb"},
		api.DeviceMeta{ID: "d2", Adapter: "stcb"})
	for _, k := range []string{"e1/d1", "e1/d2"} {
		if _, err := srv.cfg.Store.CreateCommand(k, "dump", ""); err != nil {
			t.Fatal(err)
		}
	}
	var resp struct {
		Commands []api.CommandView `json:"commands"`
	}
	getJSON(t, ts.URL+"/api/commands?device=e1/d1", &resp)
	if len(resp.Commands) != 1 || resp.Commands[0].DeviceID != "e1/d1" {
		t.Fatalf("device 过滤失败: %+v", resp.Commands)
	}
	getJSON(t, ts.URL+"/api/commands", &resp)
	if len(resp.Commands) != 2 {
		t.Fatalf("无过滤应返回 2 条，got %d", len(resp.Commands))
	}
}

// 非法查询参数必须被夹到安全范围，而不是报错或无界查询。
func TestQueryParamsClamped(t *testing.T) {
	srv, ts := setup(t)
	base := time.Now().Unix()
	for i := 0; i < 5; i++ {
		if _, err := srv.cfg.Store.AddEvent("e1/d1", "BOOT", "{}", base+int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	var resp struct {
		Events []api.EventView `json:"events"`
	}
	for _, q := range []string{"?limit=abc", "?limit=-3", "?limit=999999", "?since=notanumber", "?limit=2"} {
		getJSON(t, ts.URL+"/api/events"+q, &resp)
		if q == "?limit=2" && len(resp.Events) != 2 {
			t.Fatalf("%s: got %d events, want 2", q, len(resp.Events))
		}
		if q == "?limit=-3" && len(resp.Events) != 5 {
			t.Fatalf("%s: 非法 limit 应回退默认值（5 条全返回），got %d", q, len(resp.Events))
		}
	}
}

// 保留期清理：超期事件被删，未超期的事件与命令保留。
func TestPruneOnceRetention(t *testing.T) {
	srv, _ := setup(t)
	st := srv.cfg.Store
	old := time.Now().AddDate(0, 0, -(defaultRetentionDays + 5)).Unix()
	if _, err := st.AddEvent("e1/d1", "BOOT", "{}", old); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddEvent("e1/d1", "REMIND", "{}", time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateCommand("e1/d1", "dump", ""); err != nil {
		t.Fatal(err)
	}

	srv.pruneOnce()

	evs, err := st.ListEvents("", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || evs[0].Type != "REMIND" {
		t.Fatalf("超期事件应被清理: %+v", evs)
	}
	cmds, err := st.ListCommands("", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 1 {
		t.Fatalf("未超期命令不应被清理: %+v", cmds)
	}
}

func TestValidEdgeID(t *testing.T) {
	ok := []string{"e1", "desk-1", "lab_2", "A1", strings.Repeat("a", 64)}
	bad := []string{"", "a/b", "bad id", "desk.1", "中文", strings.Repeat("a", 65), "e1\n"}
	for _, s := range ok {
		if !validEdgeID(s) {
			t.Errorf("validEdgeID(%q) = false, want true", s)
		}
	}
	for _, s := range bad {
		if validEdgeID(s) {
			t.Errorf("validEdgeID(%q) = true, want false", s)
		}
	}
}

// 同 edge_id 重连：新连接挤掉旧连接，且旧连接断开不得把设备标离线。
func TestEdgeReconnectEvictionKeepsState(t *testing.T) {
	srv, ts := setup(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	bws := dial(t, wsURL(ts.URL, "/ws"))
	old := registerEdge(t, ts, "e1", api.DeviceMeta{ID: "d1", Adapter: "stcb"})
	writeEnv(t, old, api.Envelope{
		V: api.Version, Type: api.MsgState, Device: "e1/d1", Ts: time.Now().Unix(),
		Data: rawData(t, api.StateData{Online: true, Raw: map[string]any{"clock": "10:00"}, UpdatedAt: time.Now().Unix()}),
	})
	if _, err := readEnvUntil(ctx, bws, api.MsgState); err != nil {
		t.Fatalf("state fanout: %v", err)
	}

	// 新连接同 edge_id 上线
	neu := dial(t, wsURL(ts.URL, "/ws/edge"))
	writeEnv(t, neu, api.Envelope{
		V: api.Version, Type: api.MsgHello, Ts: time.Now().Unix(),
		Data: rawData(t, api.HelloData{EdgeID: "e1", Version: "test",
			Devices: []api.DeviceMeta{{ID: "d1", Adapter: "stcb"}}}),
	})
	// 旧连接应被服务端主动关闭
	if _, _, err := old.Read(ctx); err == nil {
		t.Fatal("旧连接应被挤掉")
	}
	time.Sleep(200 * time.Millisecond)

	srv.mu.RLock()
	link := srv.edges["e1"]
	v := srv.devices["e1/d1"]
	srv.mu.RUnlock()
	if link == nil {
		t.Fatal("新连接未注册")
	}
	if v == nil || !v.Online {
		t.Fatalf("重连不得把设备标离线: %+v", v)
	}
}

func TestSecurityHeaders(t *testing.T) {
	_, ts := setup(t)
	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
	if got := resp.Header.Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("X-Frame-Options = %q", got)
	}
}

// SPA 静态服务：未命中路径回落 index.html，但路径穿越不得逃出前端根目录。
func TestSPAFallbackAndTraversal(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>app</html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(filepath.Dir(dir), "secret.txt")
	if err := os.WriteFile(secret, []byte("TOP-SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := New(Config{Version: "test", WebUIDir: dir})
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()
	defer srv.CloseAll()

	// SPA 回落
	resp, err := http.Get(ts.URL + "/devices/e1/d1")
	if err != nil {
		t.Fatal(err)
	}
	body := new(bytes.Buffer)
	body.ReadFrom(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(body.String(), "app") {
		t.Fatalf("SPA 回落失败: %d %q", resp.StatusCode, body.String())
	}

	// 路径穿越（明文与百分号编码两种）
	for _, p := range []string{"/../secret.txt", "/%2e%2e/secret.txt", "/..%2fsecret.txt"} {
		resp, err := http.Get(ts.URL + p)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		b := new(bytes.Buffer)
		b.ReadFrom(resp.Body)
		resp.Body.Close()
		if strings.Contains(b.String(), "TOP-SECRET") {
			t.Fatalf("路径穿越泄露文件: %s", p)
		}
	}
}

// 未构建前端时 API-only 模式返回可读提示而非 404/panic。
func TestSPAWithoutBuild(t *testing.T) {
	srv := New(Config{Version: "test"})
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()
	defer srv.CloseAll()
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body := new(bytes.Buffer)
	body.ReadFrom(resp.Body)
	if !strings.Contains(body.String(), "API-only") {
		t.Fatalf("应返回 API-only 提示，got %q", body.String())
	}
}
