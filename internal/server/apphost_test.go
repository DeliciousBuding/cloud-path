package server

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/appruntime"
	"github.com/DeliciousBuding/cloud-path/internal/model"
	"github.com/DeliciousBuding/cloud-path/internal/pluginhost"
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

func TestSendNotificationEffectFailsNotImplemented(t *testing.T) {
	exec := &appEffectExecutor{}
	err := exec.Execute(context.Background(), appruntime.Effect{
		Kind: appruntime.EffectSendNotification,
		SendNotification: &appruntime.SendNotification{
			Title: "reminder", Body: "take medicine", Severity: "info",
		},
	})
	if err == nil {
		t.Fatal("SendNotification must fail closed while no notification channel is implemented")
	}
	if !errors.Is(err, appruntime.ErrEffectNotImplemented) {
		t.Fatalf("SendNotification error = %v, want ErrEffectNotImplemented", err)
	}
	if !strings.Contains(err.Error(), "send_notification") {
		t.Fatalf("SendNotification error should name the unsupported effect: %v", err)
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
		send: make(chan []byte, 1), commandSend: make(chan edgeCommandFrame, 1),
		done: make(chan struct{}), cancel: func() {},
	}
	sentFrames := make(chan edgeCommandFrame, 1)
	go func() {
		frame := <-link.commandSend
		sentFrames <- frame
		frame.result <- nil
	}()
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
	id, err := srv.dispatchDeviceCommandWithHook(ctx, tenantID, "e1/d1", "buzzer", `{"freq":5,"duration":6}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	// 链路收到命令信封
	select {
	case frame := <-sentFrames:
		var env struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(frame.payload, &env); err != nil || env.Type != "command" {
			t.Fatalf("envelope = %s err=%v", frame.payload, err)
		}
	case <-time.After(time.Second):
		t.Fatal("edge link 未收到命令")
	}
	// 命令行已落库且标 sent
	cmds, err := srv.cfg.Store.ListCommandsTenant(tenantID, "e1/d1", "", 10)
	if err != nil || len(cmds) != 1 || cmds[0].ID != id || cmds[0].Status != "sent" {
		t.Fatalf("commands = %+v err=%v", cmds, err)
	}

	// 队列满：诚实失败 + 命令行标 failed
	full := &edgeLink{edgeID: "e2", tenant: "tenant-app", tenantID: tenantID,
		send: make(chan []byte), commandSend: make(chan edgeCommandFrame),
		done: make(chan struct{}), cancel: func() {}}
	srv.mu.Lock()
	srv.edges["e2"] = full
	srv.devices["e2/d2"] = onlineDevice("e2/d2", "e2")
	srv.descriptors["e2/d2"] = model.Descriptor{
		DeviceID: "e2/d2",
		Entities: []model.Entity{{EntityID: "led", Capabilities: []string{"cloudpath.dev/capability/led@1"}}},
	}
	srv.mu.Unlock()
	if _, err := srv.dispatchDeviceCommandWithHook(ctx, tenantID, "e2/d2", "led", `{}`, nil); err == nil {
		t.Fatal("队列满应返回错误")
	}

	// 未知设备 / 离线 edge
	if _, err := srv.dispatchDeviceCommandWithHook(ctx, tenantID, "e9/d9", "buzzer", "", nil); err == nil {
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
	ah.running[appInstKey{tid, "box-t"}] = &appInstanceRun{row: runRow, tenantStr: strconv.FormatInt(tid, 10)}
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

// TestAppHostStartReportsObservedImmediately 锁定启动成功后的 observed 投影不等 30s
// tick：真实 fixture 进程 startInstance 返回时，控制面 plane/API 读面必须已是 running。
func TestAppHostStartReportsObservedImmediately(t *testing.T) {
	_, srv, _, mem, tid, _ := setupPluginSync(t)
	paths := versionedFixtureBinaries(t, "0.1.0")
	h, err := NewAppHost(srv, AppHostConfig{
		Enabled:    true,
		PluginsDir: t.TempDir(),
		LockPath:   filepath.Join(t.TempDir(), "plugins.lock"),
		StateDir:   t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	srv.SetAppHost(h)
	if err := h.mgr.RegisterInstallation(pluginhost.Installation{
		PluginID: appRoutingPlugin, Version: "0.1.0", Path: paths["0.1.0"], Kind: pluginhost.KindApplication,
	}); err != nil {
		t.Fatal(err)
	}

	const instanceID = "box-immediate"
	tenantStr := strconv.FormatInt(tid, 10)
	if err := h.mgr.ReconcileInstance(context.Background(), pluginhost.InstanceSpec{
		Tenant: tenantStr, ID: instanceID, PluginID: appRoutingPlugin, Version: "0.1.0",
	}, true); err != nil {
		t.Fatal(err)
	}
	config, err := json.Marshal(map[string]string{"target": instanceID, "version": "0.1.0"})
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := json.Marshal(map[string]string{appConfigKey: string(config)})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	portRow := storeport.PluginInstanceRow{
		TenantID: tid, EdgeID: AppHostEdgeID, InstanceID: instanceID,
		PluginID: appRoutingPlugin, Version: "0.1.0", Enabled: true,
		ConfigJSON: string(wrapped), CreatedAt: now, UpdatedAt: now,
	}
	rev, err := mem.CreatePluginInstance(portRow)
	if err != nil {
		t.Fatal(err)
	}
	portRow.Revision = rev
	srv.plugin.remember(portRow)

	row := store.PluginInstanceRow{
		TenantID: tid, EdgeID: AppHostEdgeID, InstanceID: instanceID,
		PluginID: appRoutingPlugin, Version: "0.1.0", Enabled: true,
		ConfigJSON: string(wrapped), Revision: rev,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.startInstance(ctx, row); err != nil {
		t.Fatal(err)
	}

	// 不调用 reportObserved、不等待 observedLoop tick，直接检查投影。
	srv.plugin.mu.Lock()
	ep := srv.plugin.tenants[tid].edges[AppHostEdgeID]
	observed, ok := ep.observed[instanceID]
	applied, desired := ep.appliedRevision, ep.desiredRevision
	srv.plugin.mu.Unlock()
	if !ok || !observed.HostOnline || observed.State != string(appruntime.StateRunning) {
		t.Fatalf("immediate observed = ok:%v %+v", ok, observed)
	}
	if applied != desired || desired == 0 {
		t.Fatalf("applied=%d desired=%d, want immediate convergence", applied, desired)
	}
	view := srv.pluginInstanceView(tid, "", AppHostEdgeID, instanceID)
	if !view.HasObserved || !view.EdgeOnline || view.Observed == nil || view.Observed.State != string(appruntime.StateRunning) {
		t.Fatalf("immediate API view = has_observed:%v edge_online:%v observed:%+v", view.HasObserved, view.EdgeOnline, view.Observed)
	}
}

// TestAppHostFailedInstanceProjection 锁定绑定/启动失败必须作为 observed
// 事实可见：desired 保留、state=failed、detail 可读，且伪 edge 仍判在线。
func TestAppHostFailedInstanceProjection(t *testing.T) {
	_, srv, _, _, tid, _ := setupPluginSync(t)
	ah, err := NewAppHost(srv, AppHostConfig{
		Enabled: true, PluginsDir: t.TempDir(),
		LockPath: filepath.Join(t.TempDir(), "plugins.lock"), StateDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ah.Close)
	srv.SetAppHost(ah)

	now := time.Now().Unix()
	portRow := storeport.PluginInstanceRow{
		TenantID: tid, EdgeID: AppHostEdgeID, InstanceID: "bind-fail",
		PluginID: "app-x", Version: "1.0", Enabled: true,
		ConfigJSON: "{}", CreatedAt: now, UpdatedAt: now,
	}
	rev, err := srv.plugin.store.CreatePluginInstance(portRow)
	if err != nil {
		t.Fatal(err)
	}
	portRow.Revision = rev
	srv.plugin.remember(portRow)
	row := store.PluginInstanceRow{
		TenantID: tid, EdgeID: AppHostEdgeID, InstanceID: "bind-fail",
		PluginID: "app-x", Version: "1.0", Enabled: true, Revision: rev,
	}
	ah.mu.Lock()
	ah.failed[appInstKey{tid, "bind-fail"}] = appInstanceFailure{
		row: row, detail: "bind: application binding invalid: exclusive actuator binding conflict",
	}
	ah.mu.Unlock()

	ah.reportObserved()

	srv.plugin.mu.Lock()
	ep := srv.plugin.tenants[tid].edges[AppHostEdgeID]
	observed, ok := ep.observed["bind-fail"]
	_, online := srv.pluginEdgeOnline()[pluginEdgeKey{tenantID: tid, edgeID: AppHostEdgeID}]
	srv.plugin.mu.Unlock()
	if !ok || observed.State != string(appruntime.StateFailed) || observed.Health != "ERROR" ||
		!strings.Contains(observed.Detail, "exclusive actuator binding conflict") {
		t.Fatalf("failed observed = ok:%v %+v", ok, observed)
	}
	if !online {
		t.Fatal("AppHost 仅承载失败实例时仍应保持伪 edge 在线，否则失败态被渲染成 unknown")
	}
	view := srv.pluginInstanceView(tid, "", AppHostEdgeID, "bind-fail")
	if !view.HasObserved || !view.EdgeOnline || view.Observed == nil || !view.Drift ||
		view.Observed.State != string(appruntime.StateFailed) || !strings.Contains(view.Observed.Detail, "exclusive actuator binding conflict") {
		t.Fatalf("failed instance view = %+v", view)
	}
}

// onlineDevice 构造测试用的在线设备视图。
func onlineDevice(id, edgeID string) *api.DeviceView {
	return &api.DeviceView{ID: id, EdgeID: edgeID, Online: true, State: map[string]any{}}
}

// TestRouteDeviceEventIsolation 锁定设备事件扇入的路由内核（P5 软件层）：
// 同租户 + 实体绑定的实例才收到事件；跨租户设备事件绝不路由给其他租户的
// 应用；未绑定实体不投递；空实体 no-op；nil 接收者安全。
// 物理按键（key@1/press）无法自动化——这是它全部软件路径的可测边界。
func TestRouteDeviceEventIsolation(t *testing.T) {
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

	mkRun := func(tenant int64, instance string, entities map[string]string) {
		t.Helper()
		ah.mu.Lock()
		defer ah.mu.Unlock()
		ah.running[appInstKey{tenant, instance}] = &appInstanceRun{
			row:         store.PluginInstanceRow{TenantID: tenant, InstanceID: instance},
			tenantStr:   strconv.FormatInt(tenant, 10),
			reqByEntity: entities,
		}
	}
	mkRun(tid, "box-a", map[string]string{"key1": "compartments", "buzz": "reminder-output"})
	mkRun(tid, "box-b", map[string]string{"key2": "compartments"})
	mkRun(tid+1, "box-foreign", map[string]string{"key1": "compartments"})

	// 同租户 + 已绑定：只路由到绑定该实体的实例
	routes := ah.routeDeviceEvent(tid, "key1")
	if len(routes) != 1 || routes[0].run.row.InstanceID != "box-a" || routes[0].req != "compartments" {
		t.Fatalf("key1 路由 = %+v（want 仅 box-a/compartments）", routes)
	}
	// 另一实体路由到另一实例（绑定映射按实体区分）
	routes = ah.routeDeviceEvent(tid, "key2")
	if len(routes) != 1 || routes[0].run.row.InstanceID != "box-b" {
		t.Fatalf("key2 路由 = %+v（want 仅 box-b）", routes)
	}
	// 未绑定实体：不投递
	if routes := ah.routeDeviceEvent(tid, "key9-unbound"); len(routes) != 0 {
		t.Fatalf("未绑定实体不得投递: %+v", routes)
	}
	// 跨租户：其他租户的事件只路由到该租户自己的实例
	routes = ah.routeDeviceEvent(tid+1, "key1")
	if len(routes) != 1 || routes[0].run.row.InstanceID != "box-foreign" {
		t.Fatalf("跨租户事件路由 = %+v（want 仅 box-foreign，绝不进 box-a）", routes)
	}
	// 空实体：no-op
	if routes := ah.routeDeviceEvent(tid, ""); len(routes) != 0 {
		t.Fatalf("空实体 = %+v", routes)
	}
	// nil 接收者安全（未启用 AppHost 的 Server 直接调用路径）
	var nilHost *AppHost
	nilHost.DispatchDeviceEvent(tid, "e1/d1", "key1", "cloudpath.dev/capability/key@1/press", 0)
}

// TestDesiredProtocolInstancesKeepsTenantAndEdgeIdentity 锁定协议面选择的两个身份
// 边界：AppHost 只处理部署到 Server 伪 edge 的行，且运行键带租户。
func TestDesiredProtocolInstancesKeepsTenantAndEdgeIdentity(t *testing.T) {
	rows := []store.PluginInstanceRow{
		{TenantID: 1, EdgeID: AppHostEdgeID, InstanceID: "worker", PluginID: "app-x", Enabled: true},
		{TenantID: 2, EdgeID: AppHostEdgeID, InstanceID: "worker", PluginID: "app-x", Enabled: true},
		{TenantID: 1, EdgeID: "edge-real", InstanceID: "worker", PluginID: "app-x", Enabled: true},
		{TenantID: 1, EdgeID: AppHostEdgeID, InstanceID: "off", PluginID: "app-x", Enabled: false},
		{TenantID: 1, EdgeID: AppHostEdgeID, InstanceID: "other", PluginID: "app-not-installed", Enabled: true},
	}
	hosted := serverHostedRows(rows)
	if len(hosted) != 4 {
		t.Fatalf("serverHostedRows = %d rows, want 4 (real-edge row must stay out)", len(hosted))
	}
	for _, r := range hosted {
		if r.EdgeID != AppHostEdgeID {
			t.Fatalf("real edge row leaked into AppHost scope: %+v", r)
		}
	}
	desired := desiredProtocolInstances(hosted, map[string]string{"app-x": "1.0"})
	if len(desired) != 2 {
		t.Fatalf("desired = %d entries, want 2: disabled and uninstalled rows must not run", len(desired))
	}
	for _, tid := range []int64{1, 2} {
		r, ok := desired[appInstKey{tid, "worker"}]
		if !ok || r.TenantID != tid {
			t.Fatalf("tenant %d lost its own instance: %+v (ok=%v)", tid, r, ok)
		}
	}
}
