package edge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/device"
)

// ---- 断线重连（验收阻断项）----

// TestReconnectRehelloAndReportsAllDevices 是验收项「断开任意一台电脑再恢复后能
// 自动重连」的 Edge 侧回归：断开后必须指数退避重连，重连后重新 hello（带全量
// 设备），并立即重报所有设备的当前状态与 Descriptor，让 Server/UI 恢复真实在线态。
func TestReconnectRehelloAndReportsAllDevices(t *testing.T) {
	rec := startEdgeRecorder(t, false)
	cfg := demoConfig(rec.url("ws"), "d1", "d2")
	runEdge(t, cfg)

	k1, k2 := api.DeviceKey("e-demo", "d1"), api.DeviceKey("e-demo", "d2")
	rec.waitState(t, k1, func(st api.StateData) bool { return st.Online })
	rec.waitState(t, k2, func(st api.StateData) bool { return st.Online })
	first := rec.waitHello(t, 1)
	if len(first.Devices) != 2 {
		t.Fatalf("首次 hello 设备数 = %d, want 2", len(first.Devices))
	}

	// 断开这台「电脑」到 server 的连接。
	mark := rec.mark()
	rec.dropCurrent()

	// 必须自动重连并重新注册（全量设备，不是增量）。
	second := rec.waitHello(t, 2)
	if second.EdgeID != first.EdgeID {
		t.Fatalf("重连后 edge_id 变了: %q -> %q", first.EdgeID, second.EdgeID)
	}
	if len(second.Devices) != 2 {
		t.Fatalf("重连 hello 必须带全量设备，got %d: %+v", len(second.Devices), second.Devices)
	}
	if rec.connections() < 2 {
		t.Fatalf("连接数 = %d, want >= 2", rec.connections())
	}

	// 重连后必须立即重报**所有**设备的状态与 Descriptor（面板不得停在旧值）。
	rec.waitAfter(t, mark, 20*time.Second, func(envs []api.Envelope) bool {
		return hasType(envs, api.MsgState, k1) && hasType(envs, api.MsgState, k2) &&
			hasType(envs, api.MsgDescriptor, k1) && hasType(envs, api.MsgDescriptor, k2)
	}, "重连后未全量重报状态与 Descriptor")

	// 重连后设备仍可被独立控制（链路真的恢复了，不只是注册成功）。
	rec.sendCommand(t, k2, 901, "set", "value=9")
	ack := rec.waitAck(t, 901)
	if ack.Status != "ok" || ack.Device != k2 {
		t.Fatalf("重连后命令失败: %+v", ack)
	}
	rec.waitState(t, k2, func(st api.StateData) bool { return rawInt(st, "level") == 9 })
}

// TestReconnectBackoffKeepsRetrying 验证断开后持续重试（不是只试一次就放弃）。
func TestReconnectBackoffKeepsRetrying(t *testing.T) {
	rec := startEdgeRecorder(t, false)
	cfg := demoConfig(rec.url("ws"), "d1")
	runEdge(t, cfg)
	rec.waitHello(t, 1)

	for i := 0; i < 2; i++ {
		rec.dropCurrent()
		rec.waitHello(t, i+2)
	}
	if rec.connections() < 3 {
		t.Fatalf("连接数 = %d, want >= 3（退避重连必须持续）", rec.connections())
	}
}

// ---- wss://（公网 TLS）----

// TestWSSDialOverTLS 用真实 TLS server 验证 wss:// 拨号真的能工作：
// 公网部署（cloudpath.vectorcontrol.tech）走的就是这条路径。
func TestWSSDialOverTLS(t *testing.T) {
	rec := startEdgeRecorder(t, true)
	cfg := demoConfig(rec.url("wss"), "d1")
	if !strings.HasPrefix(cfg.Server, "wss://") {
		t.Fatalf("测试应使用 wss://，got %q", cfg.Server)
	}
	runEdge(t, cfg, withHTTPClient(rec.client()))

	hello := rec.waitHello(t, 1)
	if hello.EdgeID != "e-demo" || len(hello.Devices) != 1 {
		t.Fatalf("wss 上的 hello 异常: %+v", hello)
	}
	key := api.DeviceKey("e-demo", "d1")
	rec.waitState(t, key, func(st api.StateData) bool { return st.Online })
	rec.sendCommand(t, key, 911, "ping", "")
	ack := rec.waitAck(t, 911)
	if ack.Status != "ok" {
		t.Fatalf("wss 上命令失败: %+v", ack)
	}
	if strings.TrimSpace(ack.Detail) == "" {
		t.Fatal("wss 上成功命令的 detail 也不得为空")
	}
}

// TestWSSRejectsUntrustedCertificate 反向验证：证书不可信时**不得**降级成明文、
// 也不得当成已连接（fail-closed + 继续退避重试）。
func TestWSSRejectsUntrustedCertificate(t *testing.T) {
	rec := startEdgeRecorder(t, true)
	cfg := demoConfig(rec.url("wss"), "d1")
	cfg.PollIntervalS = 3600

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg, "test") }() // 不注入信任自签 CA 的 client
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("Run 未在取消后返回")
		}
	}()

	time.Sleep(2500 * time.Millisecond)
	if rec.connections() != 0 {
		t.Fatalf("证书不可信却建立了 %d 条连接（TLS 校验被绕过）", rec.connections())
	}
	if n := rec.count(func(e api.Envelope) bool { return e.Type == api.MsgHello }); n != 0 {
		t.Fatalf("证书不可信却发出了 %d 条 hello", n)
	}
}

// ---- 断线期间的命令不得静默丢失 ----

// TestOfflineAckIsBufferedNotDropped 单元级锁定缓冲语义：
// 离线时命令 ack 必须进缓冲（重连后回放），状态消息幂等可直接丢
// （重连由 onServerOnline 全量重报）。
func TestOfflineAckIsBufferedNotDropped(t *testing.T) {
	cfg := &Config{Server: "ws://127.0.0.1:1/ws/edge", EdgeID: "e1", ReportIntervalS: 30}
	c := newWSClient(cfg, "test", nil, nil, nil)

	ackData := mustJSON(api.AckData{CommandID: 777, Status: "failed", Detail: "device offline"})
	if c.enqueue(api.Envelope{V: api.Version, Type: api.MsgCommandAck, Device: "e1/d1", Ts: time.Now().Unix(), Data: ackData}) {
		t.Fatal("离线时不应报告已进入写队列")
	}
	if c.Buffered() != 1 {
		t.Fatalf("failed ack 必须进离线缓冲，got %d 条", c.Buffered())
	}

	stateData := mustJSON(api.StateData{Online: true, Raw: map[string]any{"a": 1}, UpdatedAt: time.Now().Unix()})
	c.enqueue(api.Envelope{V: api.Version, Type: api.MsgState, Device: "e1/d1", Ts: time.Now().Unix(), Data: stateData})
	if c.Buffered() != 1 {
		t.Fatalf("状态消息幂等，不得占用离线缓冲（会挤掉真事件），got %d 条", c.Buffered())
	}

	if n := c.flushPending(); n != 1 {
		t.Fatalf("回放条数 = %d, want 1", n)
	}
	if c.Buffered() != 0 {
		t.Fatalf("回放后缓冲应为空，got %d", c.Buffered())
	}
	raw := <-c.send
	var env api.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if env.Type != api.MsgCommandAck || env.Device != "e1/d1" {
		t.Fatalf("回放内容错误: %+v", env)
	}
	var ack api.AckData
	if err := json.Unmarshal(env.Data, &ack); err != nil {
		t.Fatal(err)
	}
	if ack.CommandID != 777 || ack.Status != "failed" {
		t.Fatalf("回放的 ack 丢失了原始事实: %+v", ack)
	}
}

// TestRequeueAfterFailedWrite 单元级锁定「已出队但写失败」的消息不得被吞掉：
// 可缓冲类型（事件 / 命令 ack）放回离线缓冲等重连回放，幂等的状态消息则丢弃
// （由重连后的全量重报覆盖）。
func TestRequeueAfterFailedWrite(t *testing.T) {
	cfg := &Config{Server: "ws://127.0.0.1:1/ws/edge", EdgeID: "e1", ReportIntervalS: 30}
	c := newWSClient(cfg, "test", nil, nil, nil)

	ack := mustJSON(api.Envelope{V: api.Version, Type: api.MsgCommandAck, Device: "e1/d1",
		Ts: time.Now().Unix(), Data: mustJSON(api.AckData{CommandID: 55, Status: "failed", Detail: "device offline"})})
	c.requeue(ack)
	if c.Buffered() != 1 {
		t.Fatalf("写失败的 ack 必须回到离线缓冲，got %d 条", c.Buffered())
	}
	event := mustJSON(api.Envelope{V: api.Version, Type: api.MsgEvent, Device: "e1/d1",
		Ts: time.Now().Unix(), Data: mustJSON(api.EventData{Type: "REMIND"})})
	c.requeue(event)
	if c.Buffered() != 2 {
		t.Fatalf("写失败的事件必须回到离线缓冲，got %d 条", c.Buffered())
	}
	state := mustJSON(api.Envelope{V: api.Version, Type: api.MsgState, Device: "e1/d1",
		Ts: time.Now().Unix(), Data: mustJSON(api.StateData{Online: true})})
	c.requeue(state)
	if c.Buffered() != 2 {
		t.Fatalf("状态消息幂等，不得占用离线缓冲，got %d 条", c.Buffered())
	}
	// 无法解析的字节直接丢弃，不得把垃圾塞进缓冲。
	c.requeue([]byte("not json"))
	if c.Buffered() != 2 {
		t.Fatalf("垃圾字节不得进缓冲，got %d 条", c.Buffered())
	}
	if n := c.flushPending(); n != 2 {
		t.Fatalf("回放条数 = %d, want 2", n)
	}
}

var gatedSeq atomic.Int64

// gatedAdapter 的 Send 会阻塞到 gate 关闭，用于精确制造「命令执行中断线」。
type gatedAdapter struct {
	name    string
	gate    chan struct{}
	entered chan string
}

func (a *gatedAdapter) Name() string                { return a.name }
func (a *gatedAdapter) SupportedCommands() []string { return nil } // 无生命周期命令，避免轮询干扰
func (a *gatedAdapter) Open(ctx context.Context, cfg device.Config, _ func(device.Event)) (device.Device, error) {
	return &gatedDevice{id: cfg.ID, a: a, done: make(chan struct{})}, nil
}

type gatedDevice struct {
	id   string
	a    *gatedAdapter
	done chan struct{}
	once sync.Once
}

func (d *gatedDevice) ID() string { return d.id }
func (d *gatedDevice) Snapshot() device.State {
	return device.State{Online: true, Raw: map[string]any{"gated": true}, UpdatedAt: time.Now()}
}
func (d *gatedDevice) Send(ctx context.Context, c device.Command) error {
	select {
	case d.a.entered <- fmt.Sprintf("%s/%s", d.id, c.Cmd):
	default:
	}
	select {
	case <-d.a.gate:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (d *gatedDevice) Done() <-chan struct{} { return d.done }
func (d *gatedDevice) Close() error {
	d.once.Do(func() { close(d.done) })
	return nil
}

// TestCommandDuringDisconnectStillAcks 是「断线期间设备命令不得静默丢失成成功」
// 的端到端回归：命令执行途中断线，本地执行成功后 ack 必须缓冲并在重连后回放，
// Server 因此能看到真实结果，而不是永远停在 sent。
func TestCommandDuringDisconnectStillAcks(t *testing.T) {
	rec := startEdgeRecorder(t, false)
	adp := &gatedAdapter{
		name:    fmt.Sprintf("gated-%d", gatedSeq.Add(1)),
		gate:    make(chan struct{}),
		entered: make(chan string, 4),
	}
	device.Register(adp)

	cfg := &Config{
		Server: rec.url("ws"), EdgeID: "e-gated",
		PollIntervalS: 3600, SyncIntervalS: 3600, ReportIntervalS: 30,
		Devices: []DeviceCfg{{ID: "d1", Adapter: adp.name, Port: "COM_TEST", Baud: 9600}},
	}
	runEdge(t, cfg)

	key := api.DeviceKey("e-gated", "d1")
	rec.waitState(t, key, func(st api.StateData) bool { return st.Online })
	rec.waitHello(t, 1)

	// 命令进入执行（阻塞在设备侧）→ 此刻断线 → 再放行执行。
	rec.sendCommand(t, key, 951, "trigger", "")
	select {
	case <-adp.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("命令未到达设备")
	}
	rec.dropCurrent()
	close(adp.gate)

	// 重连后必须补上这条命令的真实 ack（本地执行成功 = ok）。
	ack := rec.waitAck(t, 951)
	if ack.Status != "ok" {
		t.Fatalf("断线期间执行成功的命令应回 ok，got %+v", ack)
	}
	if ack.Device != key {
		t.Fatalf("ack 设备键 = %q, want %q", ack.Device, key)
	}
	if strings.TrimSpace(ack.Detail) == "" {
		t.Fatal("ack detail 不得为空")
	}
	if rec.connections() < 2 {
		t.Fatalf("应已重连（连接数 %d）", rec.connections())
	}
}

// TestOfflineDeviceCommandFailsClosed 验证设备离线时命令回 failed 且带真实原因，
// 绝不静默成功（断线期间不得把命令丢失成「成功」）。
func TestOfflineDeviceCommandFailsClosed(t *testing.T) {
	rec := startEdgeRecorder(t, false)
	cfg := demoConfig(rec.url("ws"), "d1")
	// 让设备始终打不开：命令必须 failed。
	cfg.Devices[0].Adapter = fmt.Sprintf("failadp-%d", 900000+gatedSeq.Add(1))
	device.Register(&failingAdapter{name: cfg.Devices[0].Adapter, errText: "端口不存在"})
	runEdge(t, cfg)

	key := api.DeviceKey("e-demo", "d1")
	rec.waitState(t, key, func(st api.StateData) bool { return !st.Online })
	rec.sendCommand(t, key, 961, "ping", "")
	ack := rec.waitAck(t, 961)
	if ack.Status != "failed" {
		t.Fatalf("设备离线时命令必须 failed，got %+v", ack)
	}
	if !strings.Contains(ack.Detail, "offline") {
		t.Fatalf("失败 detail 应说明设备离线: %q", ack.Detail)
	}
	assertSanitized(t, ack.Detail)
}

// ---- command_ack 写达存疑窗口（防「ack 从缝里漏掉、命令永远停在 sent」）----

// ackEnvelope 构造一条可序列化的 command_ack 信封。
func ackEnvelope(commandID int64, status, detail string) []byte {
	return mustJSON(api.Envelope{V: api.Version, Type: api.MsgCommandAck, Device: "e1/d1",
		Ts: time.Now().Unix(), Data: mustJSON(api.AckData{CommandID: commandID, Status: status, Detail: detail})})
}

// TestAckWrittenOnDyingSessionIsRequeued 锁定存疑窗口的核心不变量：
// ws.Write 返回 nil（字节进了本端 socket）但会话随即死掉时，这条 ack 必须被
// 判定为**未写达**并退回离线缓冲，等重连重发——否则一次真实的执行结果永久丢失，
// Server 上的命令永远停在 sent。
func TestAckWrittenOnDyingSessionIsRequeued(t *testing.T) {
	c := testClient()
	sess := newWSSession()
	ack := ackEnvelope(7001, "failed", "device offline")

	sess.track(c, ack) // 模拟写泵：写「成功」了
	if c.Buffered() != 0 {
		t.Fatalf("会话存活时不应立刻退回缓冲，got %d", c.Buffered())
	}
	if sess.unconfirmed() != 1 {
		t.Fatalf("写达未确认的 ack = %d, want 1", sess.unconfirmed())
	}

	// 会话在存疑窗口内死掉（对端电脑掉线 / server 重启）。
	lost := sess.drain()
	if len(lost) != 1 {
		t.Fatalf("会话死亡时应交出未确认 ack %d 条, want 1", len(lost))
	}
	for _, d := range lost {
		c.requeue(d)
	}
	if c.Buffered() != 1 {
		t.Fatalf("未写达的 ack 必须回到离线缓冲，got %d", c.Buffered())
	}
	if n := c.flushPending(); n != 1 {
		t.Fatalf("重连后必须重放该 ack，got %d", n)
	}
}

// TestAckConfirmedAfterDoubtWindow 反向锁定：会话活过存疑窗口即视为真写达，
// 不得退回缓冲重发（否则每次正常执行都会产生重复 ack）。
func TestAckConfirmedAfterDoubtWindow(t *testing.T) {
	c := testClient()
	sess := newWSSession()
	sess.track(c, ackEnvelope(7002, "ok", "dump level=1"))

	time.Sleep(ackDoubtWindow + 250*time.Millisecond)
	if got := sess.unconfirmed(); got != 0 {
		t.Fatalf("存疑窗口后应确认写达，仍未确认 %d 条", got)
	}
	if c.Buffered() != 0 {
		t.Fatalf("已写达的 ack 不得退回缓冲（会重复上报），got %d", c.Buffered())
	}
	if lost := sess.drain(); len(lost) != 0 {
		t.Fatalf("会话正常结束时不应有未确认 ack，got %d", len(lost))
	}
}

// TestTrackOnDeadSessionRequeuesImmediately 覆盖另一条时序：写完成时会话已经死了
// （track 晚于 drain），此时必须直接退回缓冲，不得挂进已死会话的 inflight。
func TestTrackOnDeadSessionRequeuesImmediately(t *testing.T) {
	c := testClient()
	sess := newWSSession()
	if lost := sess.drain(); len(lost) != 0 {
		t.Fatalf("空会话 drain = %d, want 0", len(lost))
	}
	sess.track(c, ackEnvelope(7003, "ok", "noop commands=1"))
	if c.Buffered() != 1 {
		t.Fatalf("已死会话上的写必须立即退回缓冲，got %d", c.Buffered())
	}
	if sess.unconfirmed() != 0 {
		t.Fatalf("已死会话不得再持有 inflight，got %d", sess.unconfirmed())
	}
}

// TestOnlyAckIsTrackedForDoubtWindow 锁定「只有 command_ack 走写前保护」：
// event 在冻结契约里没有去重键，重放会在 server 事件流产生重复行，因此
// 事件保持既有尽力语义（写失败才 requeue，写成功即视为写达）。
func TestOnlyAckIsTrackedForDoubtWindow(t *testing.T) {
	if !isAckPayload(ackEnvelope(7004, "ok", "x")) {
		t.Fatal("command_ack 应被识别为需要写达确认")
	}
	event := mustJSON(api.Envelope{V: api.Version, Type: api.MsgEvent, Device: "e1/d1",
		Ts: time.Now().Unix(), Data: mustJSON(api.EventData{Type: "REMIND"})})
	if isAckPayload(event) {
		t.Fatal("event 不得进入写达存疑窗口（重放会产生重复事件行）")
	}
	state := mustJSON(api.Envelope{V: api.Version, Type: api.MsgState, Device: "e1/d1",
		Ts: time.Now().Unix(), Data: mustJSON(api.StateData{Online: true})})
	if isAckPayload(state) {
		t.Fatal("state 不得进入写达存疑窗口（幂等，重连后全量重报）")
	}
	if isAckPayload([]byte("not json")) {
		t.Fatal("非法 payload 不得被当成 ack")
	}
}
