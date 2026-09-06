package server

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/DeliciousBuding/cloud-path/examples/demo"
	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/auth"
	"github.com/DeliciousBuding/cloud-path/internal/server/storeport"
	"github.com/DeliciousBuding/cloud-path/internal/store"
)

// setupOverview 构造 overview 测试底座（真实 SQLite + 真实 WS + 插件投影）。
func setupOverview(t *testing.T) (*store.Store, *Server, *httptest.Server, *storeport.Memory, int64, int64) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "overview.db"))
	if err != nil {
		t.Fatal(err)
	}
	a := ensureTenantSlug(t, st, "tenant-a")
	b := ensureTenantSlug(t, st, "tenant-b")
	mem := storeport.NewMemory()
	srv := New(Config{Store: st, Version: "test", RequireAuth: true, PluginStore: mem})
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(func() { ts.Close(); srv.CloseAll(); time.Sleep(80 * time.Millisecond) })
	t.Cleanup(func() { st.Close() })
	return st, srv, ts, mem, a, b
}

func getOverview(t *testing.T, ts *httptest.Server, token string) (api.OverviewView, string) {
	t.Helper()
	var headers map[string]string
	if token != "" {
		headers = bearerJSON(token)
	}
	resp := doJSON(t, http.MethodGet, ts.URL+"/api/overview", "", headers, nil)
	raw := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/overview = %d body=%s", resp.StatusCode, raw)
	}
	var view api.OverviewView
	if err := json.Unmarshal([]byte(raw), &view); err != nil {
		t.Fatalf("decode overview: %v (%s)", err, raw)
	}
	return view, raw
}

// TestOverviewEmptyIsRealZeros 锁定：空系统返回真实零值与空列表，
// 不得用占位/样例数据填充（server_time 必须是真实当前时间）。
func TestOverviewEmptyIsRealZeros(t *testing.T) {
	st, _, ts, _, a, _ := setupOverview(t)
	token := issueTenantToken(t, st, a, `["read"]`)
	before := time.Now().Unix()
	view, raw := getOverview(t, ts, token)
	if view.DevicesTotal != 0 || view.DevicesOnline != 0 || view.EdgesTotal != 0 || view.EdgesOnline != 0 {
		t.Fatalf("空系统计数非零: %+v", view)
	}
	if view.PluginsActive != 0 || view.PluginsDesired != 0 || view.CommandsFailed != 0 {
		t.Fatalf("空系统插件/命令计数非零: %+v", view)
	}
	if view.RecentEvents == nil || view.OfflineDevices == nil || view.FailedCommands == nil {
		t.Fatalf("列表必须是空数组而不是 null: %s", raw)
	}
	if len(view.RecentEvents)+len(view.OfflineDevices)+len(view.FailedCommands) != 0 {
		t.Fatalf("空系统却有列表内容: %s", raw)
	}
	if view.ServerTime < before || view.ServerTime > time.Now().Unix()+1 {
		t.Fatalf("server_time 不是真实当前时间: %d", view.ServerTime)
	}
}

// TestOverviewTenantIsolation 锁定聚合读面的租户隔离与真实性：
// 计数来自真实在线态与 DB，跨租户数据一条都不出现。
func TestOverviewTenantIsolation(t *testing.T) {
	st, srv, ts, _, a, b := setupOverview(t)
	edgeA := issueTenantToken(t, st, a, `["edge"]`)
	edgeB := issueTenantToken(t, st, b, `["edge"]`)
	readA := issueTenantToken(t, st, a, `["read"]`)
	readB := issueTenantToken(t, st, b, `["read"]`)
	writeA := issueTenantToken(t, st, a, `["write"]`)

	// tenant-a：e1(d1 在线, d2 在线) + e2(d3 离线)
	wsA1 := dialEdgeHello(t, ts, "ea1", edgeA,
		api.DeviceMeta{ID: "d1", Adapter: "demo"}, api.DeviceMeta{ID: "d2", Adapter: "demo"})
	defer wsA1.CloseNow()
	chA1 := edgeReader(wsA1)
	wsA2 := dialEdgeHello(t, ts, "ea2", edgeA, api.DeviceMeta{ID: "d3", Adapter: "demo"})
	defer wsA2.CloseNow()
	waitEdgeLink(t, srv, "ea1", a)
	waitEdgeLink(t, srv, "ea2", a)
	reportOnline(t, wsA1, "ea1/d1", map[string]any{"clock": "10:00"})
	reportOnline(t, wsA1, "ea1/d2", map[string]any{"clock": "10:00"})
	writeEnv(t, wsA2, api.Envelope{V: api.Version, Type: api.MsgState, Device: "ea2/d3",
		Ts: time.Now().Unix(), Data: rawData(t, api.StateData{Online: false, Raw: map[string]any{}, UpdatedAt: time.Now().Unix()})})
	waitDeviceOnline(t, srv, "ea1/d1")
	waitDeviceOnline(t, srv, "ea1/d2")

	// tenant-b：一台在线设备 + 一个事件
	wsB := dialEdgeHello(t, ts, "eb1", edgeB, api.DeviceMeta{ID: "d9", Adapter: "demo"})
	defer wsB.CloseNow()
	waitEdgeLink(t, srv, "eb1", b)
	reportOnline(t, wsB, "eb1/d9", map[string]any{"clock": "12:00"})
	waitDeviceOnline(t, srv, "eb1/d9")
	writeEnv(t, wsB, api.Envelope{V: api.Version, Type: api.MsgEvent, Device: "eb1/d9",
		Ts: time.Now().Unix(), Data: rawData(t, api.EventData{Type: "BOOT"})})
	writeEnv(t, wsA1, api.Envelope{V: api.Version, Type: api.MsgEvent, Device: "ea1/d1",
		Ts: time.Now().Unix(), Data: rawData(t, api.EventData{Type: "REMIND"})})

	// tenant-a 的一条失败命令（真实 ack 回执）。
	resp := doJSON(t, http.MethodPost, ts.URL+"/api/devices/ea1/d1/commands",
		`{"cmd":"ping"}`, bearerJSON(writeA), nil)
	raw := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("command = %d body=%s", resp.StatusCode, raw)
	}
	var cv api.CommandView
	if err := json.Unmarshal([]byte(raw), &cv); err != nil {
		t.Fatal(err)
	}
	if _, ok := waitEnv(t, chA1, api.MsgCommand, 30*time.Second); !ok {
		t.Fatal("edge 未收到命令")
	}
	writeEnv(t, wsA1, api.Envelope{V: api.Version, Type: api.MsgCommandAck, Device: "ea1/d1",
		Ts: time.Now().Unix(), Data: rawData(t, api.AckData{CommandID: cv.ID, Status: "failed", Detail: "port busy"})})
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		view, _ := getOverview(t, ts, readA)
		if view.CommandsFailed == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	viewA, rawA := getOverview(t, ts, readA)
	if viewA.DevicesTotal != 3 || viewA.DevicesOnline != 2 {
		t.Fatalf("tenant-a 设备计数 = %d/%d, want 2/3（在线/总）", viewA.DevicesOnline, viewA.DevicesTotal)
	}
	if viewA.EdgesTotal != 2 || viewA.EdgesOnline != 2 {
		t.Fatalf("tenant-a edge 计数 = %d/%d, want 2/2", viewA.EdgesOnline, viewA.EdgesTotal)
	}
	if len(viewA.OfflineDevices) != 1 || viewA.OfflineDevices[0].ID != "ea2/d3" {
		t.Fatalf("离线设备列表错误: %+v", viewA.OfflineDevices)
	}
	if viewA.CommandsFailed != 1 || len(viewA.FailedCommands) != 1 || viewA.FailedCommands[0].ID != cv.ID {
		t.Fatalf("失败命令聚合错误: %+v", viewA)
	}
	if len(viewA.RecentEvents) != 1 || viewA.RecentEvents[0].DeviceID != "ea1/d1" {
		t.Fatalf("最近事件错误/泄漏: %+v", viewA.RecentEvents)
	}
	for _, banned := range []string{"eb1", "d9", "tenant-b", "BOOT"} {
		if strings.Contains(rawA, banned) {
			t.Fatalf("tenant-a overview 泄漏 tenant-b 数据 %q: %s", banned, rawA)
		}
	}

	viewB, rawB := getOverview(t, ts, readB)
	if viewB.DevicesTotal != 1 || viewB.DevicesOnline != 1 || viewB.EdgesTotal != 1 || viewB.EdgesOnline != 1 {
		t.Fatalf("tenant-b 计数错误: %+v", viewB)
	}
	if viewB.CommandsFailed != 0 || len(viewB.FailedCommands) != 0 || len(viewB.OfflineDevices) != 0 {
		t.Fatalf("tenant-b 聚合被 tenant-a 污染: %+v", viewB)
	}
	if len(viewB.RecentEvents) != 1 || viewB.RecentEvents[0].Type != "BOOT" {
		t.Fatalf("tenant-b 事件错误: %+v", viewB.RecentEvents)
	}
	for _, banned := range []string{"ea1", "ea2", "REMIND", "port busy"} {
		if strings.Contains(rawB, banned) {
			t.Fatalf("tenant-b overview 泄漏 tenant-a 数据 %q: %s", banned, rawB)
		}
	}
}

// TestOverviewPluginsDesiredNeverCountsAsActive 锁定不变量 5 在聚合读面上的表现：
// 期望启用绝不计入 active；只有 Edge 真实上报健康且未过期才算 active；
// Edge 断线后 active 立刻回落，desired 不受影响。
func TestOverviewPluginsDesiredNeverCountsAsActive(t *testing.T) {
	st, srv, ts, _, a, _ := setupOverview(t)
	admin := issueTenantToken(t, st, a, `["admin"]`)
	edgeTok := issueTenantToken(t, st, a, `["edge"]`)
	readTok := issueTenantToken(t, st, a, `["read"]`)

	if rev := createInstance(t, ts, admin, "e1", "box1"); rev != 1 {
		t.Fatalf("revision = %d", rev)
	}
	view, _ := getOverview(t, ts, readTok)
	if view.PluginsDesired != 1 || view.PluginsActive != 0 {
		t.Fatalf("离线时 desired/active = %d/%d, want 1/0", view.PluginsDesired, view.PluginsActive)
	}

	ws := dialEdgeHello(t, ts, "e1", edgeTok, api.DeviceMeta{ID: "d1", Adapter: "demo"})
	ch := edgeReader(ws)
	waitEdgeLink(t, srv, "e1", a)
	if _, ok := waitEnv(t, ch, api.MsgPluginDesired, 30*time.Second); !ok {
		t.Fatal("未收到 desired")
	}
	// 上报 CRASHED：desired 仍是 1，active 必须是 0（不得把期望当健康）。
	writeEnv(t, ws, api.Envelope{V: api.Version, Type: api.MsgPluginStatus, Ts: time.Now().Unix(),
		Data: rawData(t, api.PluginStatusData{BootID: "b1", Sequence: 1,
			ObservedInstances: []api.PluginObservedInstanceData{{
				InstanceID: "box1", PluginID: "io.github.acme.driver", Version: "0.1.0",
				HostOnline: true, State: "CRASHED", Health: "UNKNOWN", RestartCount: 2}}})})
	waitPluginActive(t, ts, readTok, 0)
	view, _ = getOverview(t, ts, readTok)
	if view.PluginsDesired != 1 {
		t.Fatalf("observed 崩溃却改写了 desired 计数: %+v", view)
	}
	// 上报 HEALTHY：active 变 1。
	writeEnv(t, ws, api.Envelope{V: api.Version, Type: api.MsgPluginStatus, Ts: time.Now().Unix(),
		Data: rawData(t, api.PluginStatusData{BootID: "b1", Sequence: 2,
			ObservedInstances: []api.PluginObservedInstanceData{{
				InstanceID: "box1", PluginID: "io.github.acme.driver", Version: "0.1.0",
				HostOnline: true, State: "HEALTHY", Health: "HEALTHY"}}})})
	waitPluginActive(t, ts, readTok, 1)

	// Edge 断线：active 回落 0（投影过期只标记，desired 不动）。
	ws.CloseNow()
	waitEdgeOffline(t, srv, "e1")
	waitPluginActive(t, ts, readTok, 0)
	view, _ = getOverview(t, ts, readTok)
	if view.PluginsDesired != 1 {
		t.Fatalf("断线后 desired 被改写: %+v", view)
	}
	list := listInstancesHTTP(t, ts, readTok)
	if len(list.Instances) != 1 || !list.Instances[0].Stale || !list.Instances[0].Desired.Enabled {
		t.Fatalf("断线后实例视图错误: %+v", list.Instances)
	}
	if list.Instances[0].Observed == nil {
		t.Fatalf("断线不应抹掉既有 observed 投影（只标 stale）: %+v", list.Instances[0])
	}
}

// waitPluginActive 轮询 overview 直到 plugins_active 达到期望值。
func waitPluginActive(t *testing.T, ts *httptest.Server, token string, want int) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var last api.OverviewView
	for time.Now().Before(deadline) {
		last, _ = getOverview(t, ts, token)
		if last.PluginsActive == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("plugins_active = %d, want %d（overview=%+v）", last.PluginsActive, want, last)
}

// TestOverviewRequiresAuth 锁定账号模式下 overview 不对外裸奔。
func TestOverviewRequiresAuth(t *testing.T) {
	_, _, ts, _, _, _ := setupOverview(t)
	resp, err := http.Get(ts.URL + "/api/overview")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("未认证 overview = %d, want 401", resp.StatusCode)
	}
}

func TestPluginObservedActiveAppHost(t *testing.T) {
	tests := []struct {
		name     string
		edge     string
		observed *api.PluginInstanceObservedView
		want     bool
	}{
		{name: "server running", edge: "server", observed: &api.PluginInstanceObservedView{State: "running", Health: "UNKNOWN"}, want: true},
		{name: "server stopped", edge: "server", observed: &api.PluginInstanceObservedView{State: "stopped", Health: "UNKNOWN"}},
		{name: "server without observed", edge: "server"},
		{name: "edge unknown running state", edge: "edge-a", observed: &api.PluginInstanceObservedView{State: "running", Health: "UNKNOWN"}},
		{name: "edge healthy", edge: "edge-a", observed: &api.PluginInstanceObservedView{State: "HEALTHY", Health: "HEALTHY"}, want: true},
		{name: "edge degraded", edge: "edge-a", observed: &api.PluginInstanceObservedView{State: "DEGRADED", Health: "DEGRADED"}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := api.PluginInstanceView{EdgeID: tt.edge, Observed: tt.observed}
			if got := pluginObservedActive(in); got != tt.want {
				t.Fatalf("pluginObservedActive = %t, want %t", got, tt.want)
			}
		})
	}
}

// openOverviewCommandStore also exposes a fixture-only SQL connection so exact historical
// timestamps can be seeded without adding a production clock or mutation API.
func openOverviewCommandStore(t *testing.T) (*store.Store, *sql.DB) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "command-window.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath)+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return st, db
}

func seedOverviewCommand(t *testing.T, db *sql.DB, tenantID int64, name, status string, createdAt int64, ackedAt any) int64 {
	t.Helper()
	res, err := db.Exec(`INSERT INTO commands(tenant_id, device_id, cmd, args, status, created_at, acked_at, result)
		VALUES(?, 'edge/device', 'ping', '', ?, ?, ?, ?)`, tenantID, status, createdAt, ackedAt, name)
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestOverviewFailedCommandsWindow(t *testing.T) {
	st, db := openOverviewCommandStore(t)
	a := ensureTenantSlug(t, st, "tenant-a")
	b := ensureTenantSlug(t, st, "tenant-b")
	srv := New(Config{Store: st})
	t.Cleanup(srv.CloseAll)
	const sampledAt int64 = 1_800_000_000
	const since = sampledAt - 86_400
	for _, c := range []struct {
		name, status string
		createdAt    int64
		ackedAt      any
	}{
		{"expired-failed", "failed", since - 100, since - 1},
		{"expired-timeout", "timeout", since - 1, nil},
		{"lower-failed", "failed", since - 100, since},
		{"lower-timeout-fallback", "timeout", since, nil},
		{"recent-failed", "failed", sampledAt - 100, sampledAt - 20},
		{"recent-timeout", "timeout", sampledAt - 100, sampledAt - 10},
		{"delayed-failed", "failed", since - 100, sampledAt - 5},
		{"upper-failed", "failed", sampledAt - 100, sampledAt},
		{"upper-timeout-fallback", "timeout", sampledAt, nil},
		{"future-failed", "failed", sampledAt, sampledAt + 1},
		{"future-timeout", "timeout", sampledAt + 1, nil},
		{"success", "ok", sampledAt - 100, sampledAt},
		{"sent", "sent", sampledAt - 100, sampledAt},
		{"pending", "pending", sampledAt, nil},
	} {
		seedOverviewCommand(t, db, a, c.name, c.status, c.createdAt, c.ackedAt)
	}
	seedOverviewCommand(t, db, b, "other-tenant", "failed", sampledAt, sampledAt)

	rows, total, err := srv.overviewFailedCommands(&a, sampledAt)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"upper-timeout-fallback", "upper-failed", "delayed-failed", "recent-timeout", "recent-failed", "lower-timeout-fallback", "lower-failed"}
	if total != len(want) || len(rows) != len(want) {
		t.Fatalf("window: total=%d rows=%+v, want %d", total, rows, len(want))
	}
	for i, row := range rows {
		if row.Result != want[i] {
			t.Fatalf("row %d = %+v, want %q", i, row, want[i])
		}
	}
	if rows[0].AckedAt != 0 || rows[0].CreatedAt != sampledAt || rows[1].AckedAt != sampledAt {
		t.Fatalf("failure timestamps were not preserved: %+v", rows[:2])
	}
	rows, total, err = srv.overviewFailedCommands(&a, sampledAt+2*86_400)
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 || rows == nil || len(rows) != 0 {
		t.Fatalf("failures did not age out of rolling window: total=%d rows=%+v", total, rows)
	}
}

func TestOverviewCommandWindowHTTPKeepsHistory(t *testing.T) {
	st, db := openOverviewCommandStore(t)
	a := ensureTenantSlug(t, st, "tenant-a")
	b := ensureTenantSlug(t, st, "tenant-b")
	now := time.Now().Unix()
	oldFailed := seedOverviewCommand(t, db, a, "old-failed", "failed", now-90_000, now-86_401)
	oldTimeout := seedOverviewCommand(t, db, a, "old-timeout", "timeout", now-90_000, nil)
	success := seedOverviewCommand(t, db, a, "success", "ok", now-100, now-2)
	seedOverviewCommand(t, db, a, "future-failed", "failed", now, now+3600)
	seedOverviewCommand(t, db, a, "recent-timeout", "timeout", now-100, now-1)
	for i := 0; i < 24; i++ {
		seedOverviewCommand(t, db, a, "recent-failed", "failed", now-90_000, now-int64(i)-2)
	}
	other := seedOverviewCommand(t, db, b, "other-tenant", "timeout", now-100, now-1)
	readA := issueTenantToken(t, st, a, `["read"]`)
	readB := issueTenantToken(t, st, b, `["read"]`)
	for _, tc := range []struct {
		name        string
		requireAuth bool
		token       string
		wantTotal   int
	}{
		{"tenant-a", true, readA, 25},
		{"tenant-b", true, readB, 1},
		{"unscoped", false, "", 26},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := New(Config{Store: st, RequireAuth: tc.requireAuth})
			ts := httptest.NewServer(srv.Routes())
			t.Cleanup(func() { ts.Close(); srv.CloseAll() })
			before := time.Now().Unix()
			view, _ := getOverview(t, ts, tc.token)
			if view.ServerTime < before || view.ServerTime > time.Now().Unix() {
				t.Fatalf("invalid sample time: %d", view.ServerTime)
			}
			if view.CommandsFailed != tc.wantTotal || len(view.FailedCommands) != min(tc.wantTotal, 20) {
				t.Fatalf("count/preview mismatch: %+v", view)
			}
			for _, row := range view.FailedCommands {
				if row.AckedAt < view.ServerTime-86_400 || row.AckedAt > view.ServerTime || (row.Status != "failed" && row.Status != "timeout") {
					t.Fatalf("row outside the advertised sample/window: %+v", row)
				}
				if (tc.name == "tenant-a" && row.ID == other) || (tc.name == "tenant-b" && row.ID != other) {
					t.Fatalf("cross-tenant failure: %+v", row)
				}
			}
			if tc.name != "tenant-a" {
				return
			}
			// Overview filtering is read-only and must never become the activity API's default.
			for _, query := range []string{"", "?status=failed", "?status=timeout"} {
				resp := doJSON(t, http.MethodGet, ts.URL+"/api/commands"+query, "", bearerJSON(readA), nil)
				raw := readBody(t, resp)
				var history struct {
					Commands []api.CommandView `json:"commands"`
				}
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("command history = %d: %s", resp.StatusCode, raw)
				}
				if err := json.Unmarshal([]byte(raw), &history); err != nil {
					t.Fatal(err)
				}
				ids := map[int64]bool{}
				for _, row := range history.Commands {
					ids[row.ID] = true
				}
				wantCount, wantID := 29, oldFailed
				if query == "?status=failed" {
					wantCount = 26
				} else if query == "?status=timeout" {
					wantCount, wantID = 2, oldTimeout
				}
				if len(history.Commands) != wantCount || !ids[wantID] || ids[other] || (query == "" && (!ids[oldTimeout] || !ids[success])) {
					t.Fatalf("historical commands hidden or leaked for %q: %+v", query, history.Commands)
				}
			}
		})
	}
}

func TestOverviewWithoutStore(t *testing.T) {
	srv := New(Config{})
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(func() { ts.Close(); srv.CloseAll() })
	before := time.Now().Unix()
	view, raw := getOverview(t, ts, "")
	if view.CommandsFailed != 0 || view.FailedCommands == nil || len(view.FailedCommands) != 0 || view.RecentEvents == nil {
		t.Fatalf("store-free overview is not an empty read model: %s", raw)
	}
	if view.ServerTime < before || view.ServerTime > time.Now().Unix() {
		t.Fatalf("store-free overview lost its sample time: %d", view.ServerTime)
	}
}

func TestOverviewPrincipalWithoutTenantIsNotGlobal(t *testing.T) {
	st, db := openOverviewCommandStore(t)
	a := ensureTenantSlug(t, st, "tenant-a")
	now := time.Now().Unix()
	seedOverviewCommand(t, db, a, "private-failure", "failed", now, now)
	srv := New(Config{Store: st})
	t.Cleanup(srv.CloseAll)
	for _, tenantID := range []int64{0, -1} {
		req := httptest.NewRequest(http.MethodGet, "/api/overview", nil)
		req = req.WithContext(auth.WithPrincipal(req.Context(), &auth.Principal{TenantID: tenantID, Role: "admin"}))
		w := httptest.NewRecorder()
		srv.handleOverview(w, req)
		var view api.OverviewView
		if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
			t.Fatal(err)
		}
		if w.Code != http.StatusOK || view.CommandsFailed != 0 || len(view.FailedCommands) != 0 {
			t.Fatalf("principal tenant %d received global commands: %d %s", tenantID, w.Code, w.Body.String())
		}
	}
}

func TestOverviewStoreFailureIsUnavailableNotEmpty(t *testing.T) {
	for _, table := range []string{"events", "commands"} {
		t.Run(table, func(t *testing.T) {
			st, db := openOverviewCommandStore(t)
			if _, err := db.Exec("DROP TABLE " + table); err != nil {
				t.Fatal(err)
			}
			srv := New(Config{Store: st})
			t.Cleanup(srv.CloseAll)
			w := httptest.NewRecorder()
			srv.handleOverview(w, httptest.NewRequest(http.MethodGet, "/api/overview", nil))
			if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "overview unavailable") || strings.Contains(w.Body.String(), "commands_failed") {
				t.Fatalf("unavailable %s presented as a healthy empty overview: %d %s", table, w.Code, w.Body.String())
			}
		})
	}
}

func TestOverviewLegacyTokenKeepsDefaultTenant(t *testing.T) {
	st, db := openOverviewCommandStore(t)
	defaultTenant := ensureTenantSlug(t, st, "default")
	otherTenant := ensureTenantSlug(t, st, "other")
	now := time.Now().Unix()
	want := seedOverviewCommand(t, db, defaultTenant, "default-failure", "failed", now, now)
	seedOverviewCommand(t, db, otherTenant, "other-failure", "failed", now, now)
	const token = "test-overview-legacy"
	srv := New(Config{Store: st, RequireAuth: true, Token: token})
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(func() { ts.Close(); srv.CloseAll() })
	view, _ := getOverview(t, ts, token)
	if view.CommandsFailed != 1 || len(view.FailedCommands) != 1 || view.FailedCommands[0].ID != want {
		t.Fatalf("legacy token lost its default tenant scope: %+v", view)
	}
}
