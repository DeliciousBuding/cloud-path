package edge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/device"
	"github.com/DeliciousBuding/cloud-path/sdk/go/model"
)

// ---- 无硬件端口放宽（PortRequired 结构性约定）----

var portFakeSeq atomic.Int64

// noPortFake 声明「不需要真实端口」（examples/demo 用的就是这个约定）。
type noPortFake struct{ name string }

func (a *noPortFake) Name() string                { return a.name }
func (a *noPortFake) SupportedCommands() []string { return []string{"ping"} }
func (a *noPortFake) PortRequired() bool          { return false }
func (a *noPortFake) Open(ctx context.Context, cfg device.Config, _ func(device.Event)) (device.Device, error) {
	return &fakeDevice{id: cfg.ID, done: make(chan struct{})}, nil
}

// needPortFake 显式声明需要端口。
type needPortFake struct{ noPortFake }

func (a *needPortFake) PortRequired() bool { return true }

func registerPortFakes(t *testing.T) (noPort, needPort, implicit string) {
	t.Helper()
	n := portFakeSeq.Add(1)
	noPort = fmt.Sprintf("portopt-no-%d", n)
	needPort = fmt.Sprintf("portopt-yes-%d", n)
	implicit = fmt.Sprintf("portopt-impl-%d", n)
	device.Register(&noPortFake{name: noPort})
	device.Register(&needPortFake{noPortFake{name: needPort}})
	device.Register(&fakeAdapter{name: implicit}) // 未实现 PortRequired
	return
}

func TestPortRequiredFailsClosedForUnknownAdapter(t *testing.T) {
	if !PortRequired("adapter-that-does-not-exist") {
		t.Fatal("未注册适配器必须按 port 必填处理（fail-closed，不得放宽）")
	}
	noPort, needPort, implicit := registerPortFakes(t)
	if PortRequired(noPort) {
		t.Fatal("声明 PortRequired()==false 的适配器不应要求端口")
	}
	if !PortRequired(needPort) {
		t.Fatal("声明 PortRequired()==true 的适配器必须要求端口")
	}
	if !PortRequired(implicit) {
		t.Fatal("未实现 PortRequired 的适配器必须要求端口（缺省不放宽）")
	}
	// 真实适配器：demo 无硬件可省 port，stcb 必须填 port。
	if PortRequired("demo") {
		t.Fatal("examples/demo 必须声明 port 非必填，否则没板子的电脑接不进来")
	}
}

func TestLoadConfigPortOptionalForNoHardwareAdapter(t *testing.T) {
	noPort, _, implicit := registerPortFakes(t)
	cfg, err := LoadConfig(writeCfg(t, "server: ws://x/ws/edge\ndevices:\n  - {id: d1, adapter: "+noPort+"}\n"))
	if err != nil {
		t.Fatalf("无硬件适配器缺 port 应通过: %v", err)
	}
	if cfg.Devices[0].Port != "" {
		t.Fatalf("port 不应被凭空填充: %q", cfg.Devices[0].Port)
	}
	if cfg.Devices[0].PollCommand != DefaultPollCommand || cfg.Devices[0].SyncCommand != DefaultSyncCommand {
		t.Fatalf("生命周期命令缺省值未生效: %+v", cfg.Devices[0])
	}
	if _, err := LoadConfig(writeCfg(t, "server: ws://x/ws/edge\ndevices:\n  - {id: d1, adapter: "+implicit+"}\n")); err == nil {
		t.Fatal("未声明 PortRequired 的适配器缺 port 必须报错（stcb 校验强度不得被削弱）")
	} else if !strings.Contains(err.Error(), "port 必填") {
		t.Fatalf("错误信息应指明 port 必填: %v", err)
	}
	// 真实 demo 适配器：无 port 也能加载（同学零硬件接入的前提）。
	if _, err := LoadConfig(writeCfg(t, "server: ws://x/ws/edge\ndevices:\n  - {id: sim-1, adapter: demo}\n")); err != nil {
		t.Fatalf("adapter: demo 缺 port 应通过: %v", err)
	}
}

func TestLoadConfigExtraAndCommandOverrides(t *testing.T) {
	noPort, _, _ := registerPortFakes(t)
	cfg, err := LoadConfig(writeCfg(t, `
server: ws://x/ws/edge
devices:
  - id: d1
    adapter: `+noPort+`
    poll_command: status
    sync_command: " "
    extra:
      tick_interval_s: "5"
      note: hello
`))
	if err != nil {
		t.Fatal(err)
	}
	d := cfg.Devices[0]
	if d.PollCommand != "status" {
		t.Fatalf("poll_command 覆盖未生效: %q", d.PollCommand)
	}
	if d.SyncCommand != DefaultSyncCommand {
		t.Fatalf("空白 sync_command 应回落缺省值: %q", d.SyncCommand)
	}
	if d.Extra["tick_interval_s"] != "5" || d.Extra["note"] != "hello" {
		t.Fatalf("extra 未原样透传: %v", d.Extra)
	}
}

// ---- 多设备 / 多 Edge 硬化（真实 demo 适配器 + 真实 WS，无硬件）----

func demoKeys(ids ...string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, api.DeviceKey("e-demo", id))
	}
	return out
}

// TestNoHardwareEdgeReportsAllDevices 是「零硬件也能接入」的运行时证明：
// 真实 edge.Run + 真实 demo 适配器 + 真实 WS，3 台设备各自独立上报，
// 设备键严格是 "<edge_id>/<device_id>"。
func TestNoHardwareEdgeReportsAllDevices(t *testing.T) {
	rec := startEdgeRecorder(t, false)
	cfg := demoConfig(rec.url("ws"), "d1", "d2", "d3")
	runEdge(t, cfg)

	hello := rec.waitHello(t, 1)
	if hello.EdgeID != "e-demo" {
		t.Fatalf("hello edge_id = %q", hello.EdgeID)
	}
	if len(hello.Devices) != 3 {
		t.Fatalf("hello 设备数 = %d, want 3（验收要求 ≥3 台设备）: %+v", len(hello.Devices), hello.Devices)
	}
	for _, d := range hello.Devices {
		if d.Adapter != "demo" {
			t.Fatalf("hello 设备 adapter = %q, want demo", d.Adapter)
		}
		if d.Port != "" {
			t.Fatalf("无硬件设备不应上报端口: %+v", d)
		}
	}

	keys := demoKeys("d1", "d2", "d3")
	for _, k := range keys {
		st := rec.waitState(t, k, func(st api.StateData) bool {
			return st.Online && st.Raw["kind"] == "reference-demo-device" && st.Raw["hardware"] == "none"
		})
		if st.Raw["uptime_s"] == nil {
			t.Fatalf("%s 未上报真实 uptime: %+v", k, st.Raw)
		}
		if st.UpdatedAt == 0 {
			t.Fatalf("%s 的 state.updated_at 为零值", k)
		}
	}
	// 每台设备都必须上报 Descriptor（schema-driven UI 的前提）。
	for _, k := range keys {
		if rec.count(func(e api.Envelope) bool { return e.Type == api.MsgDescriptor && e.Device == k }) == 0 {
			t.Fatalf("%s 未上报 Descriptor", k)
		}
	}
}

// TestDescriptorEnvelopeCarriesRealTimestamps 是缺陷 D1 的端到端回归：
// 上报出去的 observation 必须带真实 observed_at 与 Edge 盖戳的 received_at，
// 且 observed_at <= received_at，二者都接近真实时刻（不得是 0001-01-01 零值）。
func TestDescriptorEnvelopeCarriesRealTimestamps(t *testing.T) {
	rec := startEdgeRecorder(t, false)
	cfg := demoConfig(rec.url("ws"), "d1")
	runEdge(t, cfg)
	key := api.DeviceKey("e-demo", "d1")
	rec.waitState(t, key, func(st api.StateData) bool { return st.Online })

	before := time.Now().Add(-2 * time.Second)
	var desc model.Descriptor
	waitForEnvelope(t, 130*time.Second, func() bool {
		for _, e := range rec.all() {
			if e.Type != api.MsgDescriptor || e.Device != key {
				continue
			}
			var d model.Descriptor
			if err := json.Unmarshal(e.Data, &d); err != nil {
				continue
			}
			if len(d.Entities) == 0 {
				continue
			}
			// 启动时序存在合法窗口：onServerOnline 触发时 dev 尚未就绪，上报的是
			// Adapter 静态骨架 Descriptor（有结构、无观测值——产品行为，见
			// descriptorEnvelope 的回落分支）。D1 回归的对象是带观测值的实时
			// Descriptor，因此等到携带观测值的那一份再断言，不拿骨架误判。
			obs := 0
			for _, ent := range d.Entities {
				obs += len(ent.Observations)
			}
			if obs == 0 {
				continue
			}
			desc = d
			return true
		}
		return false
	}, "未收到 Descriptor")

	if desc.DeviceID != key {
		t.Fatalf("Descriptor.device_id = %q, want %q（稳定键必须是 <edge_id>/<device_id>）", desc.DeviceID, key)
	}
	checked := 0
	for _, e := range desc.Entities {
		for name, o := range e.Observations {
			checked++
			if o.ObservedAt.IsZero() {
				t.Errorf("entity %q observation %q 的 observed_at 是零值（缺陷 D1）", e.EntityID, name)
			}
			if o.ReceivedAt.IsZero() {
				t.Errorf("entity %q observation %q 的 received_at 是零值（Edge 必须盖戳）", e.EntityID, name)
			}
			if o.ObservedAt.After(o.ReceivedAt.Add(time.Second)) {
				t.Errorf("entity %q observation %q 的 observed_at(%v) 晚于 received_at(%v)",
					e.EntityID, name, o.ObservedAt, o.ReceivedAt)
			}
			if o.ReceivedAt.Before(before) || o.ReceivedAt.After(time.Now().Add(2*time.Second)) {
				t.Errorf("entity %q observation %q 的 received_at(%v) 不在真实上报时刻附近", e.EntityID, name, o.ReceivedAt)
			}
		}
	}
	if checked != 6 {
		t.Fatalf("应校验 6 个 entity 的观测时间戳，实际 %d", checked)
	}
}

// TestCommandsRouteToCorrectDeviceOnly 是暗卷 #7 的 Edge 侧半边 + 缺陷 D2 回归：
// 分别下发命令不得串到同 Edge 的另一台设备；ack 必须带回正确 command_id 与设备键；
// 成功命令的 detail 必须非空、限长、脱敏。
func TestCommandsRouteToCorrectDeviceOnly(t *testing.T) {
	rec := startEdgeRecorder(t, false)
	cfg := demoConfig(rec.url("ws"), "d1", "d2")
	runEdge(t, cfg)

	k1, k2 := api.DeviceKey("e-demo", "d1"), api.DeviceKey("e-demo", "d2")
	rec.waitState(t, k1, func(st api.StateData) bool { return st.Online })
	rec.waitState(t, k2, func(st api.StateData) bool { return st.Online })

	// 逐条下发并等 ack：edge 对每条命令各起一个协程（慢命令不阻塞读循环），
	// 因此并发下发本身不保证跨命令的先后顺序；本用例要验的是「路由与结果」，
	// 顺序由测试自己控制。同设备串行性另有专门用例。
	for _, tc := range []struct {
		id     int64
		key    string
		cmd    string
		args   string
		wantIn []string
	}{
		{101, k1, "set", "value=42", []string{"set", "value=42"}},
		{102, k2, "ping", "", []string{"ping", "pings=1"}},
		{103, k1, "dump", "", []string{"dump", "level=42"}},
		{104, k2, "noop", "", []string{"noop"}},
	} {
		rec.sendCommand(t, tc.key, tc.id, tc.cmd, tc.args)
		ack := rec.waitAck(t, tc.id)
		if ack.Device != tc.key {
			t.Fatalf("ack %d 设备键 = %q, want %q（命令串线）", tc.id, ack.Device, tc.key)
		}
		if ack.Status != "ok" {
			t.Fatalf("ack %d status = %q, want ok（detail=%q）", tc.id, ack.Status, ack.Detail)
		}
		// 缺陷 D2：成功命令必须有可读的执行结果，不能只有 status=ok。
		if strings.TrimSpace(ack.Detail) == "" {
			t.Fatalf("ack %d 成功但 detail 为空（执行结果反馈缺失）", tc.id)
		}
		if len(ack.Detail) > DetailLimit {
			t.Fatalf("ack %d detail 超过长度上限: %d > %d", tc.id, len(ack.Detail), DetailLimit)
		}
		for _, want := range tc.wantIn {
			if !strings.Contains(ack.Detail, want) {
				t.Fatalf("ack %d detail 缺少真实结果 %q: %q", tc.id, want, ack.Detail)
			}
		}
		assertSanitized(t, ack.Detail)
	}

	// 状态层面再次确认没有串线：d1 只有 set，d2 只有 ping。
	st1 := rec.waitState(t, k1, func(st api.StateData) bool { return rawInt(st, "level") == 42 })
	if rawInt(st1, "pings") != 0 {
		t.Fatalf("命令串线：d1 收到了本该发给 d2 的 ping（pings=%v）", st1.Raw["pings"])
	}
	st2 := rec.waitState(t, k2, func(st api.StateData) bool { return rawInt(st, "pings") == 1 })
	if rawInt(st2, "level") != 0 {
		t.Fatalf("命令串线：d2 收到了本该发给 d1 的 set（level=%v）", st2.Raw["level"])
	}
}

// assertSanitized 校验 detail 不含绝对路径、secret 形态与多行原文。
func assertSanitized(t *testing.T, detail string) {
	t.Helper()
	if strings.ContainsAny(detail, "\r\n\t") {
		t.Errorf("detail 含折行（疑似 stdout/stderr 原文）: %q", detail)
	}
	for _, bad := range []string{`C:\`, `C:/`, "/home/", "/Users/", "/var/", "/tmp/"} {
		if strings.Contains(detail, bad) {
			t.Errorf("detail 含本机绝对路径 %q: %q", bad, detail)
		}
	}
	lower := strings.ToLower(detail)
	for _, bad := range []string{"token=", "password=", "secret=", "authorization", "bearer "} {
		if strings.Contains(lower, bad) && !strings.Contains(lower, "redacted") {
			t.Errorf("detail 疑似含凭据形态 %q: %q", bad, detail)
		}
	}
}

// TestUnknownDeviceCommandFailsClosed 验证发往未知设备键的命令回 failed ack，
// 且不落到任何真实设备上。
func TestUnknownDeviceCommandFailsClosed(t *testing.T) {
	rec := startEdgeRecorder(t, false)
	cfg := demoConfig(rec.url("ws"), "d1")
	runEdge(t, cfg)

	k1 := api.DeviceKey("e-demo", "d1")
	rec.waitState(t, k1, func(st api.StateData) bool { return st.Online })
	rec.sendCommand(t, "e-demo/nope", 201, "ping", "")
	ack := rec.waitAck(t, 201)
	if ack.Status != "failed" {
		t.Fatalf("未知设备命令应回 failed，got %+v", ack)
	}
	if ack.Device != "e-demo/nope" {
		t.Fatalf("ack 设备键应原样回带: %+v", ack)
	}
	if strings.TrimSpace(ack.Detail) == "" {
		t.Fatal("失败 ack 也必须带可读 detail")
	}
	if st, ok := rec.lastState(k1); ok && rawInt(st, "pings") != 0 {
		t.Fatalf("未知设备命令不得落到 d1: %+v", st.Raw)
	}
}

// TestUnsupportedCommandFailsClosedWithDetail 验证白名单外命令（如 stcb 专属的 sync
// 打到 demo 设备）回 failed 且带真实原因——绝不静默成功。
func TestUnsupportedCommandFailsClosedWithDetail(t *testing.T) {
	rec := startEdgeRecorder(t, false)
	cfg := demoConfig(rec.url("ws"), "d1")
	runEdge(t, cfg)
	k1 := api.DeviceKey("e-demo", "d1")
	rec.waitState(t, k1, func(st api.StateData) bool { return st.Online })

	rec.sendCommand(t, k1, 301, "sync", "0148")
	ack := rec.waitAck(t, 301)
	if ack.Status != "failed" {
		t.Fatalf("demo 不支持 sync，应回 failed，got %+v", ack)
	}
	if !strings.Contains(ack.Detail, "不支持的命令") {
		t.Fatalf("失败 detail 应说明原因: %q", ack.Detail)
	}
	assertSanitized(t, ack.Detail)
}

// ---- 一台设备故障不影响同 Edge 的其他设备 ----

var failingAdapterSeq atomic.Int64

// failingAdapter 的 Open 永远失败（模拟端口打不开/设备不存在）。
type failingAdapter struct {
	name    string
	opens   atomic.Int64
	errText string
}

func (a *failingAdapter) Name() string                { return a.name }
func (a *failingAdapter) SupportedCommands() []string { return []string{"ping"} }
func (a *failingAdapter) Open(ctx context.Context, cfg device.Config, _ func(device.Event)) (device.Device, error) {
	a.opens.Add(1)
	return nil, errors.New(a.errText)
}

// TestOneDeviceFailureDoesNotAffectSiblings 是验收项「断开任意一台…其他设备不受影响」
// 的设备级半边：一台设备永远打不开，同 Edge 的另一台照常在线、照常接受命令。
func TestOneDeviceFailureDoesNotAffectSiblings(t *testing.T) {
	rec := startEdgeRecorder(t, false)
	bad := &failingAdapter{name: fmt.Sprintf("failadp-%d", failingAdapterSeq.Add(1)), errText: "端口不存在"}
	device.Register(bad)

	cfg := demoConfig(rec.url("ws"), "good")
	cfg.Devices = append(cfg.Devices, DeviceCfg{
		ID: "bad", Adapter: bad.name, Port: "COM99", Baud: 9600,
		PollCommand: DefaultPollCommand, SyncCommand: DefaultSyncCommand,
	})
	runEdge(t, cfg)

	kGood, kBad := api.DeviceKey("e-demo", "good"), api.DeviceKey("e-demo", "bad")

	// 故障设备必须诚实上报离线（不得伪装在线），并且不断重试。
	rec.waitState(t, kBad, func(st api.StateData) bool { return !st.Online })
	waitForEnvelope(t, 30*time.Second, func() bool { return bad.opens.Load() >= 2 }, "故障设备应持续重试打开")

	// 兄弟设备完全不受影响：在线 + 可独立控制。
	rec.waitState(t, kGood, func(st api.StateData) bool { return st.Online })
	rec.sendCommand(t, kGood, 401, "set", "value=7")
	ack := rec.waitAck(t, 401)
	if ack.Status != "ok" || ack.Device != kGood {
		t.Fatalf("兄弟设备命令失败: %+v", ack)
	}
	rec.waitState(t, kGood, func(st api.StateData) bool { return rawInt(st, "level") == 7 })

	// 故障设备上的命令必须 failed，绝不静默成功。
	rec.sendCommand(t, kBad, 402, "ping", "")
	badAck := rec.waitAck(t, 402)
	if badAck.Status != "failed" {
		t.Fatalf("设备离线时命令必须回 failed，got %+v", badAck)
	}
	if !strings.Contains(badAck.Detail, "offline") {
		t.Fatalf("失败 detail 应说明设备离线: %q", badAck.Detail)
	}
}
