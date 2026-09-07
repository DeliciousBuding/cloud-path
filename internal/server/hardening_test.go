package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	_ "github.com/DeliciousBuding/cloud-path/examples/demo"
	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/store"
)

// registerEdge 走一次 hello 注册并等到 REST 可见（避免测试竞态）。
func registerEdge(t *testing.T, ts *httptest.Server, edgeID string, devs ...api.DeviceMeta) *websocket.Conn {
	t.Helper()
	ews := dial(t, wsURL(ts.URL, "/ws/edge"))
	writeEnv(t, ews, api.Envelope{
		V: api.Version, Type: api.MsgHello, Ts: time.Now().Unix(),
		Data: rawData(t, api.HelloData{EdgeID: edgeID, Version: "test", Devices: devs}),
	})
	deadline := time.Now().Add(30 * time.Second)
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
	if len(resp.Adapters) != 1 || resp.Adapters[0].Name != "demo" {
		t.Fatalf("adapters = %+v", resp.Adapters)
	}
	want := []string{"ping", "set", "dump", "noop"}
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
	registerEdge(t, ts, "e1", api.DeviceMeta{ID: "d1", Adapter: "demo"})
	if _, err := srv.cfg.Store.AddEvent("e1/d1", "BOOT", "{}", time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	var st api.StatsView
	getJSON(t, ts.URL+"/api/stats", &st)
	if st.Devices != 1 || st.Events != 1 || st.SchemaVersion != srv.cfg.Store.Version() {
		t.Fatalf("stats = %+v", st)
	}
	if st.RetentionDays != defaultRetentionDays || st.AuthMode != authModeOpen {
		t.Fatalf("stats 配置项 = %+v", st)
	}
	if st.OldestEvent == 0 {
		t.Fatal("oldest_event 应有值")
	}
}

// TestStatsAuthModeReportsRealEnforcement /api/stats 的 auth_mode 必须报告 server **实际执行**
// 的鉴权形态，而不是「有没有配 legacy 令牌」。
//
// 回归点：账号模式（setup 已建用户）且未配 CLOUDPATH_TOKEN 时，旧实现用 `cfg.Token != ""`
// 推断 auth_enabled，把「全部 /api/* 必须登录」报成未启用，系统页于是显示
// 「鉴权 未启用（本机模式）」——而同一页下方写着「账号模式下浏览器靠会话 cookie 鉴权」，
// 等于把一个已收紧的部署说成裸奔。形态定义见 docs/api.md §1 不变量 1-3。
func TestStatsAuthModeReportsRealEnforcement(t *testing.T) {
	cases := []struct {
		name        string
		token       string
		requireAuth bool
		runSetup    bool
		want        string
	}{
		{name: "L0 单机：无用户无令牌", want: authModeOpen},
		{name: "仅共享 legacy 令牌", token: "sekret", want: authModeToken},
		{name: "-require-auth 强制读鉴权", token: "sekret", requireAuth: true, want: authModeAccount},
		{name: "账号模式且无 legacy 令牌（回归点）", runSetup: true, want: authModeAccount},
		{name: "账号模式叠加 legacy 令牌", token: "sekret", runSetup: true, want: authModeAccount},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, err := store.Open(filepath.Join(t.TempDir(), "stats.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { st.Close() })
			srv := New(Config{Store: st, Version: "test", Token: tc.token, RequireAuth: tc.requireAuth})
			ts := httptest.NewServer(srv.Routes())
			t.Cleanup(func() { ts.Close(); srv.CloseAll(); time.Sleep(50 * time.Millisecond) })

			if tc.runSetup {
				setupAdmin(t, ts)
			}
			// 账号模式下 /api/stats 自身也要凭据：会话 cookie 与 Bearer 令牌两条路都必须读得到。
			headers := map[string]string{}
			var cookies []*http.Cookie
			switch {
			case tc.runSetup:
				cookies = loginCookie(t, ts)
			case tc.token != "":
				headers["Authorization"] = "Bearer " + tc.token
			}
			resp := doJSON(t, http.MethodGet, ts.URL+"/api/stats", "", headers, cookies)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("stats = %d, want 200", resp.StatusCode)
			}
			var got api.StatsView
			if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
				t.Fatal(err)
			}
			if got.AuthMode != tc.want {
				t.Fatalf("auth_mode = %q, want %q", got.AuthMode, tc.want)
			}
		})
	}
}

// TestUnroutedPathsDoNotFallBackToSPA 未路由的 /api/* 必须回 JSON 404、缺失的 /assets/*
// 必须回标准 404，两者都不得回落 index.html；而前端深链仍必须回落 index.html。
//
// 回归点：chi 的 `r.Handle("/*", spaHandler)` 兜底把 API 路径一起吞了。真实探针
// （账号模式演示栈）：
//
//	DELETE /api/devices/nope/nope → 200 text/html 1574B <!doctype html>…
//	POST   /api/nonexistent       → 200 text/html
//	GET    /assets/missing.js      → 200 text/html
//
// 一个不存在的删除端点回 200，等于对客户端谎报成功。
func TestUnroutedPathsDoNotFallBackToSPA(t *testing.T) {
	// 测试构建默认没有 embed_ui（webui.Dist 为零值），用 WebUIDir 指一个最小前端，
	// 这样 index.html 兜底分支是真实可达的，断言不依赖构建标签。
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"),
		[]byte(`<!doctype html><html><body><div id="root"></div></body></html>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "app.js"), []byte("// ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "spa.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv := New(Config{Store: st, Version: "test", WebUIDir: dir})
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(func() { ts.Close(); srv.CloseAll(); time.Sleep(50 * time.Millisecond) })

	apiCases := []struct{ method, path string }{
		{http.MethodGet, "/api"},
		{http.MethodGet, "/api/"},
		{http.MethodGet, "/api/nonexistent"},
		{http.MethodPost, "/api/nonexistent"},
		{http.MethodDelete, "/api/devices/nope/nope"},
		{http.MethodGet, "/api/auth/nope"},
	}
	for _, tc := range apiCases {
		t.Run("API "+tc.method+" "+tc.path, func(t *testing.T) {
			resp := doJSON(t, tc.method, ts.URL+tc.path, "", nil, nil)
			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", resp.StatusCode)
			}
			if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Fatalf("Content-Type = %q, want application/json（不得回落 index.html）", ct)
			}
			body := readBody(t, resp)
			if strings.Contains(body, "<!doctype html") {
				t.Fatalf("响应体是 SPA index.html 而非 JSON 404: %.120q", body)
			}
			var e map[string]string
			if err := json.Unmarshal([]byte(body), &e); err != nil || e["error"] == "" {
				t.Fatalf("错误体不是 {\"error\":...}: %q err=%v", body, err)
			}
		})
	}

	t.Run("缺失的 assets 回标准 404 而非 index.html", func(t *testing.T) {
		resp := doJSON(t, http.MethodGet, ts.URL+"/assets/definitely-missing.js", "", nil, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", resp.StatusCode)
		}
		if body := readBody(t, resp); strings.Contains(body, "<!doctype html") {
			t.Fatalf("缺失资源回落了 index.html: %.120q", body)
		}
	})

	// 存在的资源与前端深链照旧：前者 200 内容，后者 200 + index.html（SPA 客户端路由）。
	t.Run("已存在的 assets 照常服务", func(t *testing.T) {
		resp := doJSON(t, http.MethodGet, ts.URL+"/assets/app.js", "", nil, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		if body := readBody(t, resp); !strings.Contains(body, "ok") {
			t.Fatalf("资源内容 = %q", body)
		}
	})
	for _, p := range []string{"/", "/devices", "/settings/deep/link"} {
		t.Run("SPA 深链回落 index.html "+p, func(t *testing.T) {
			resp := doJSON(t, http.MethodGet, ts.URL+p, "", nil, nil)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
			if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
				t.Fatalf("Content-Type = %q, want text/html", ct)
			}
			if body := readBody(t, resp); !strings.Contains(body, `<div id="root">`) {
				t.Fatalf("不是 index.html: %.120q", body)
			}
		})
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
			Devices: []api.DeviceMeta{{ID: "d1", Adapter: "demo"}}}),
	})
	writeEnv(t, ews, api.Envelope{
		V: api.Version, Type: api.MsgState, Device: "e1/d1", Ts: time.Now().Unix(),
		Data: rawData(t, api.StateData{Online: true, Raw: map[string]any{}, UpdatedAt: time.Now().Unix()}),
	})
	deadline := time.Now().Add(10 * time.Second)
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
	registerEdge(t, ts, "e1", api.DeviceMeta{ID: "d1", Adapter: "demo"})

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
	registerEdge(t, ts, "e1", api.DeviceMeta{ID: "d1", Adapter: "demo"})
	url := ts.URL + "/api/devices/e1/d1/commands"

	cases := []struct {
		name string
		body string
		want int
	}{
		{"空 body", ``, http.StatusBadRequest},
		{"坏 json", `{`, http.StatusBadRequest},
		{"缺 cmd", `{"args":"x"}`, http.StatusBadRequest},
		{"args 带换行", `{"cmd":"set","args":"S\n"}`, http.StatusBadRequest},
		{"args 带 NUL", "{\"cmd\":\"raw\",\"args\":\"S\x00\"}", http.StatusBadRequest},
		{"args 过长", `{"cmd":"set","args":"` + strings.Repeat("A", 65) + `"}`, http.StatusBadRequest},
		{"未知命令", `{"cmd":"reboot"}`, http.StatusBadRequest},
		{"合法 raw", `{"cmd":"set","args":"S"}`, http.StatusOK},
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
	ews := registerEdge(t, ts, "e1", api.DeviceMeta{ID: "d1", Adapter: "demo"})
	ews.CloseNow()
	deadline := time.Now().Add(10 * time.Second)
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
		api.DeviceMeta{ID: "d1", Adapter: "demo"},
		api.DeviceMeta{ID: "d2", Adapter: "demo"})
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
	old := registerEdge(t, ts, "e1", api.DeviceMeta{ID: "d1", Adapter: "demo"})
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
			Devices: []api.DeviceMeta{{ID: "d1", Adapter: "demo"}}}),
	})
	// 旧连接应被服务端主动关闭
	if _, _, err := old.Read(ctx); err == nil {
		t.Fatal("旧连接应被挤掉")
	}
	// 先条件等待新连接注册（设备此前已在线，waitDeviceOnline 不构成注册判据；
	// 满载下固定 sleep 会被击穿），再给旧连接 defer 留窗口，复查终态未被破坏。
	deadline := time.Now().Add(30 * time.Second)
	var link *edgeLink
	for time.Now().Before(deadline) {
		srv.mu.RLock()
		link = srv.edges["e1"]
		srv.mu.RUnlock()
		if link != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)

	srv.mu.RLock()
	link = srv.edges["e1"]
	v := srv.devices["e1/d1"]
	online := v != nil && v.Online
	srv.mu.RUnlock()
	if link == nil {
		t.Fatal("新连接未注册")
	}
	if !online {
		t.Fatalf("重连不得把设备标离线: present=%v", v != nil)
	}
}

// TestSecurityHeaders 契约 §1.5：五个安全头 + CSP 在**所有**响应出现（含 4xx）。
func TestSecurityHeaders(t *testing.T) {
	_, ts := setup(t)
	for _, path := range []string{"/healthz", "/api/nope", "/"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		resp.Body.Close()
		if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Fatalf("%s: X-Content-Type-Options = %q", path, got)
		}
		if got := resp.Header.Get("X-Frame-Options"); got != "DENY" {
			t.Fatalf("%s: X-Frame-Options = %q", path, got)
		}
		if got := resp.Header.Get("Referrer-Policy"); got != "no-referrer" {
			t.Fatalf("%s: Referrer-Policy = %q", path, got)
		}
		if got := resp.Header.Get("Permissions-Policy"); got != "camera=(), microphone=(), geolocation=()" {
			t.Fatalf("%s: Permissions-Policy = %q", path, got)
		}
		csp := resp.Header.Get("Content-Security-Policy")
		for _, want := range []string{
			"default-src 'self'",
			"script-src 'self' 'sha256-jKH63gcAPxRiFu8qDqGCGYrEoEL5nCbt8h3hWkIeBB0='",
			"style-src 'self' 'unsafe-inline'",
			"img-src 'self' data:",
			"connect-src 'self' ws: wss:",
			"frame-ancestors 'none'",
			"base-uri 'self'",
			"form-action 'self'",
		} {
			if !strings.Contains(csp, want) {
				t.Fatalf("%s: CSP 缺少 %q（got %q）", path, want, csp)
			}
		}
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
