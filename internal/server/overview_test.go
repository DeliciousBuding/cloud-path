package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/DeliciousBuding/cloud-path/examples/demo"
	"github.com/DeliciousBuding/cloud-path/internal/api"
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
	resp := doJSON(t, http.MethodGet, ts.URL+"/api/overview", "", bearerJSON(token), nil)
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
