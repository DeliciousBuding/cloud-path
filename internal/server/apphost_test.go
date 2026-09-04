package server

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/appruntime"
	"github.com/DeliciousBuding/cloud-path/internal/model"
	"github.com/DeliciousBuding/cloud-path/internal/server/storeport"
	"github.com/DeliciousBuding/cloud-path/internal/store"
	sdkapplication "github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/application"
)

// TestAppConfigBytes 锁定 desired config map → 应用配置提取：
// app_config 键承载 JSON 字符串；缺失/非法 map 诚实返回 nil（应用侧会拒绝空配置）。
func TestAppConfigBytes(t *testing.T) {
	raw := `{"app_config":"{\"timezone\":\"Asia/Shanghai\"}","other":"x"}`
	got := appConfigBytes(raw)
	var cfg appConfig
	if err := json.Unmarshal(got, &cfg); err != nil || cfg.Timezone != "Asia/Shanghai" {
		t.Fatalf("appConfigBytes(%q) = %s err=%v", raw, got, err)
	}
	if appConfigBytes(`{}`) != nil {
		t.Fatal("缺失 app_config 应返回 nil")
	}
	if appConfigBytes(`not-json`) != nil {
		t.Fatal("非法 JSON 应返回 nil")
	}
}

// TestBuildWindowTick 锁定窗口 tick 构造：配置时区的当日 RFC3339 start/end、
// 应用期望的 WindowJSON 字段、非法 HH:MM / 跨午夜窗口诚实返回 nil。
func TestBuildWindowTick(t *testing.T) {
	tz := time.FixedZone("CST", 8*3600)
	local := time.Date(2026, 9, 4, 20, 30, 0, 0, tz)
	w := appWindowSpec{ID: "w1", Compartment: "c1", Start: "20:30", End: "20:40"}

	tick := buildWindowTick(w, local, tz)
	if tick == nil {
		t.Fatal("expected tick")
	}
	if tick.ScheduleID != "window-w1" {
		t.Fatalf("schedule id = %q", tick.ScheduleID)
	}
	var payload struct {
		ID          string `json:"id"`
		Compartment string `json:"compartment"`
		Start       string `json:"start"`
		End         string `json:"end"`
	}
	if err := json.Unmarshal([]byte(tick.WindowJSON), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ID != "w1" || payload.Compartment != "c1" {
		t.Fatalf("payload = %+v", payload)
	}
	start, err := time.Parse(time.RFC3339, payload.Start)
	if err != nil {
		t.Fatal(err)
	}
	if start.Hour() != 20 || start.Minute() != 30 {
		t.Fatalf("start = %v（应为配置时区 20:30）", start)
	}
	end, _ := time.Parse(time.RFC3339, payload.End)
	if end.Sub(start) != 10*time.Minute {
		t.Fatalf("窗口时长 = %v", end.Sub(start))
	}

	// 非法/跨午夜：诚实 nil，不构造反向窗口
	if buildWindowTick(appWindowSpec{ID: "x", Start: "9:30", End: "10:00"}, local, tz) != nil {
		t.Fatal("非法 HH:MM 应返回 nil")
	}
	if buildWindowTick(appWindowSpec{ID: "x", Start: "23:50", End: "00:10"}, local, tz) != nil {
		t.Fatal("end <= start（跨午夜）应返回 nil")
	}
}

// TestParseHHMM 锁定时间解析边界。
func TestParseHHMM(t *testing.T) {
	cases := map[string]bool{"00:00": true, "23:59": true, "24:00": false, "12:60": false, "9:30": false, "": false, "12-30": false}
	for in, want := range cases {
		if _, _, ok := parseHHMM(in); ok != want {
			t.Fatalf("parseHHMM(%q) ok=%v want %v", in, ok, want)
		}
	}
}

// TestDispatchDeviceCommandAppPath 锁定应用效果 → 设备命令的下发内核：
// 命令行落库、edge 链路收到 MsgCommand 信封、发送队列满诚实失败。
func TestDispatchDeviceCommandAppPath(t *testing.T) {
	srv, _ := setup(t)
	tenantID := ensureTenantSlug(t, srv.cfg.Store, "tenant-app")

	// 命令行落库（CreateCommandTenant 是 INSERT…SELECT FROM devices）要求设备
	// 已在 store 注册；在线态与 Descriptor 仍走内存注入。
	registerDevice := func(id, edgeID string) {
		t.Helper()
		if err := srv.cfg.Store.UpsertDeviceTenant(id, edgeID, "test", id, "", tenantID); err != nil {
			t.Fatal(err)
		}
	}
	registerDevice("e1/d1", "e1")
	registerDevice("e2/d2", "e2")

	// 构造一台在线设备（edge link）+ Descriptor（实体 buzzer）
	link := &edgeLink{
		edgeID: "e1", tenant: "tenant-app", tenantID: tenantID,
		send: make(chan []byte, 1), cancel: func() {},
	}
	srv.mu.Lock()
	srv.edges["e1"] = link
	srv.devices["e1/d1"] = onlineDevice("e1/d1", "e1")
	srv.descriptors["e1/d1"] = model.Descriptor{
		DeviceID: "e1/d1",
		Entities: []model.Entity{{EntityID: "buzzer", Capabilities: []string{"cloudpath.dev/capability/buzzer@1"}}},
	}
	srv.mu.Unlock()

	if key := srv.deviceKeyForEntity("buzzer"); key != "e1/d1" {
		t.Fatalf("deviceKeyForEntity = %q", key)
	}
	if key := srv.deviceKeyForEntity("nope"); key != "" {
		t.Fatalf("unknown entity should be empty, got %q", key)
	}

	ctx := context.Background()
	id, err := srv.dispatchDeviceCommand(ctx, tenantID, "e1/d1", "buzzer", `{"freq":5,"duration":6}`)
	if err != nil {
		t.Fatal(err)
	}
	// 链路收到命令信封
	select {
	case raw := <-link.send:
		var env struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &env); err != nil || env.Type != "command" {
			t.Fatalf("envelope = %s err=%v", raw, err)
		}
	default:
		t.Fatal("edge link 未收到命令")
	}
	// 命令行已落库且标 sent
	cmds, err := srv.cfg.Store.ListCommandsTenant(tenantID, "e1/d1", "", 10)
	if err != nil || len(cmds) != 1 || cmds[0].ID != id || cmds[0].Status != "sent" {
		t.Fatalf("commands = %+v err=%v", cmds, err)
	}

	// 队列满：诚实失败 + 命令行标 failed
	full := &edgeLink{edgeID: "e2", tenant: "tenant-app", tenantID: tenantID, send: make(chan []byte, 0), cancel: func() {}}
	srv.mu.Lock()
	srv.edges["e2"] = full
	srv.devices["e2/d2"] = onlineDevice("e2/d2", "e2")
	srv.descriptors["e2/d2"] = model.Descriptor{
		DeviceID: "e2/d2",
		Entities: []model.Entity{{EntityID: "led", Capabilities: []string{"cloudpath.dev/capability/led@1"}}},
	}
	srv.mu.Unlock()
	if _, err := srv.dispatchDeviceCommand(ctx, tenantID, "e2/d2", "led", `{}`); err == nil {
		t.Fatal("队列满应返回错误")
	}

	// 未知设备 / 离线 edge
	if _, err := srv.dispatchDeviceCommand(ctx, tenantID, "e9/d9", "buzzer", ""); err == nil {
		t.Fatal("未知设备应失败")
	}
}

// TestNotifyCommandAckFinalStatesOnly 锁定：只有最终态（ok/failed/timeout）触发
// RequestCompleted；sent 中间态与无关命令号不触发。appruntime 未运行实例时
// Dispatch 报错只记日志（不 panic）。
func TestNotifyCommandAckFinalStatesOnly(t *testing.T) {
	srv, _ := setup(t)
	ah, err := NewAppHost(srv, AppHostConfig{
		Enabled:    true,
		PluginsDir: t.TempDir(),
		LockPath:   filepath.Join(t.TempDir(), "plugins.lock"),
		StateDir:   t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ah.Close)

	// 未运行的实例命令引用：Notify 只应安全 no-op（runtime 无该实例，Dispatch 报错被吞）
	ah.mu.Lock()
	ah.appCmds[42] = appCommandRef{InstanceID: "ghost", RequestID: "req-1", EntityID: "buzzer", Action: "buzzer"}
	ah.mu.Unlock()

	ah.NotifyCommandAck(42, "sent", "") // 中间态：不删除引用
	ah.mu.Lock()
	_, still := ah.appCmds[42]
	ah.mu.Unlock()
	if !still {
		t.Fatal("sent 中间态不应消费引用")
	}

	ah.NotifyCommandAck(42, "ok", "device ACK") // 最终态：消费引用（Dispatch 对不存在实例报错被记日志）
	ah.mu.Lock()
	_, still = ah.appCmds[42]
	ah.mu.Unlock()
	if still {
		t.Fatal("最终态应消费引用")
	}
}

// TestEffectFromSDKCancelJob 锁定 CancelScheduledTask → cancel_job 效果转换
// （此前是 unknown union 被拒——应用一完成窗口就会丢效果）。
func TestEffectFromSDKCancelJob(t *testing.T) {
	src := appruntime.EffectSource{PluginInstanceID: "box1", TenantID: "1"}
	raw := &sdkapplication.ApplicationEffect{
		PluginInstanceID: "box1",
		Union:            &sdkapplication.CancelScheduledTask{ScheduleID: "window-check-w1"},
	}
	eff, err := appruntime.EffectFromSDK(raw, src)
	if err != nil {
		t.Fatal(err)
	}
	if eff.Kind != appruntime.EffectCancelJob || eff.CancelJob == nil || eff.CancelJob.ScheduleID != "window-check-w1" {
		t.Fatalf("effect = %+v", eff)
	}
	if eff.PluginInstanceID != "box1" {
		t.Fatalf("instance = %q", eff.PluginInstanceID)
	}
}

// TestAppHostObservedProjectionFeedsPlane 锁定 2026-09-05 真板 E2E 发现的缺口：
// AppHost 的 observed 投影必须并入内存 plane（此前直写 PluginStore，API/UI
// 投影永远 has_observed=false），且伪 edge "server" 在承载实例时判在线（否则
// instanceView 的 EdgeOnline 门把状态打成 unknown，应用明明在跑也看不见）。
func TestAppHostObservedProjectionFeedsPlane(t *testing.T) {
	_, srv, _, _, tid, _ := setupPluginSync(t)
	ah, err := NewAppHost(srv, AppHostConfig{
		Enabled:    true,
		PluginsDir: t.TempDir(),
		LockPath:   filepath.Join(t.TempDir(), "plugins.lock"),
		StateDir:   t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ah.Close)
	srv.SetAppHost(ah)

	// 与 API 写路径同构：storeport 行 → plane.store 落库 → remember 同步 desired revision
	now := time.Now().Unix()
	portRow := storeport.PluginInstanceRow{
		TenantID: tid, EdgeID: AppHostEdgeID, InstanceID: "box-t",
		PluginID: "app-x", Version: "1.0", Enabled: true,
		ConfigJSON: "{}", CreatedAt: now, UpdatedAt: now,
	}
	rev, err := srv.plugin.store.CreatePluginInstance(portRow)
	if err != nil {
		t.Fatal(err)
	}
	portRow.Revision = rev
	srv.plugin.remember(portRow)
	// 注入运行记录（真实链路由 reconcile 填充；本测试只锁投影接线）
	runRow := store.PluginInstanceRow{
		TenantID: tid, EdgeID: AppHostEdgeID, InstanceID: "box-t",
		PluginID: "app-x", Version: "1.0", Enabled: true, Revision: rev,
	}
	ah.mu.Lock()
	ah.running["box-t"] = &appInstanceRun{row: runRow}
	ah.mu.Unlock()

	ah.reportObserved()

	// plane：observed 并入 + applied=desired（本地宿主语义）
	srv.plugin.mu.Lock()
	ep := srv.plugin.tenants[tid].edges[AppHostEdgeID]
	var observedState string
	var hostOnline bool
	if o, ok := ep.observed["box-t"]; ok {
		observedState, hostOnline = o.State, o.HostOnline
	}
	applied, desired := ep.appliedRevision, ep.desiredRevision
	srv.plugin.mu.Unlock()
	if observedState != string(appruntime.StateRunning) || !hostOnline {
		t.Fatalf("observed = state:%q host_online:%v", observedState, hostOnline)
	}
	if applied != desired || desired == 0 {
		t.Fatalf("applied=%d desired=%d（AppHost 本地宿主：applied 即 desired）", applied, desired)
	}

	// 伪 edge 在线判定：只标事实承载的租户
	if online := srv.pluginEdgeOnline(); !online[pluginEdgeKey{tenantID: tid, edgeID: AppHostEdgeID}] {
		t.Fatal("伪 edge server 应在线（AppHost 正在承载该租户实例）")
	}

	// API 读面：instance view 呈现 running（不再被 EdgeOnline 门打成 unknown）
	view := srv.pluginInstanceView(tid, "", AppHostEdgeID, "box-t")
	if !view.HasObserved || !view.EdgeOnline || view.Observed == nil || view.Observed.State != string(appruntime.StateRunning) {
		t.Fatalf("view = has_observed:%v edge_online:%v observed:%+v", view.HasObserved, view.EdgeOnline, view.Observed)
	}
	if view.Drift || view.Stale {
		t.Fatalf("view drift=%v stale=%v（本地宿主：applied 即 desired，observed 刚上报）", view.Drift, view.Stale)
	}
}

// onlineDevice 构造测试用的在线设备视图。
func onlineDevice(id, edgeID string) *api.DeviceView {
	return &api.DeviceView{ID: id, EdgeID: edgeID, Online: true, State: map[string]any{}}
}
