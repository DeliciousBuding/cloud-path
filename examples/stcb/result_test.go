package stcb

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"go.bug.st/serial"

	"github.com/DeliciousBuding/cloud-path/sdk/go/driverkit"
	"github.com/DeliciousBuding/cloud-path/sdk/go/model"
)

// fakePort 是可编程的串口替身：写入含 'S' 的字节时按固件行为回一帧转储。
// 有了它，命令结果摘要（缺陷 D2）与 Descriptor 时间戳（缺陷 D1）都能在没有
// 真板的机器上做真实回归——被测代码路径与真板完全一致，只有串口是替身。
type fakePort struct {
	mu       sync.Mutex
	rx       []byte
	tx       []byte
	reply    string // 收到 'S' 时回复的帧；空串 = 从不回复（模拟哑板/未接线）
	replyV   string // 收到 'V' 时回复的 V 帧；空串 = 从不回复
	closed   bool
	writeErr error
}

var _ serial.Port = (*fakePort)(nil)

func (p *fakePort) SetMode(*serial.Mode) error { return nil }

func (p *fakePort) Read(out []byte) (int, error) {
	deadline := time.Now().Add(20 * time.Millisecond)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		if len(p.rx) > 0 {
			n := copy(out, p.rx)
			p.rx = p.rx[n:]
			p.mu.Unlock()
			return n, nil
		}
		closed := p.closed
		p.mu.Unlock()
		if closed {
			return 0, errors.New("fakePort: closed")
		}
		time.Sleep(2 * time.Millisecond)
	}
	return 0, nil
}

func (p *fakePort) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.writeErr != nil {
		return 0, p.writeErr
	}
	p.tx = append(p.tx, b...)
	if p.reply != "" && bytes.Contains(b, []byte("S")) {
		p.rx = append(p.rx, []byte(p.reply)...)
	}
	if p.replyV != "" && bytes.Contains(b, []byte("V")) {
		p.rx = append(p.rx, []byte(p.replyV)...)
	}
	return len(b), nil
}

func (p *fakePort) written() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return string(p.tx)
}

func (p *fakePort) Drain() error                                         { return nil }
func (p *fakePort) ResetInputBuffer() error                              { return nil }
func (p *fakePort) ResetOutputBuffer() error                             { return nil }
func (p *fakePort) SetDTR(bool) error                                    { return nil }
func (p *fakePort) SetRTS(bool) error                                    { return nil }
func (p *fakePort) GetModemStatusBits() (*serial.ModemStatusBits, error) { return nil, nil }
func (p *fakePort) SetReadTimeout(time.Duration) error                   { return nil }
func (p *fakePort) Break(time.Duration) error                            { return nil }
func (p *fakePort) Close() error {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	return nil
}

// frame0148 是 Captain 公网实测里 sync 0148 之后的真实帧形态：
// state=0(待机) hour=01 min=48 三槽=0(待确认)。
const frame0148 = "S:00148000\n"

// newTestDev 构造一台接在 fakePort 上的设备并启动 RX 循环（与真板同一条代码路径）。
func newTestDev(t *testing.T, reply string) (*dev, *fakePort, *eventSink) {
	t.Helper()
	fp := &fakePort{reply: reply}
	sink := &eventSink{}
	d := &dev{id: "d1", name: "测试板", portName: "COM_TEST", port: fp, done: make(chan struct{}), onEvent: sink.fn}
	ctx, cancel := context.WithCancel(context.Background())
	go d.rxLoop(ctx)
	t.Cleanup(func() { cancel(); _ = fp.Close() })
	return d, fp, sink
}

type eventSink struct {
	mu sync.Mutex
	ev []string
}

func (s *eventSink) fn(e driverkit.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ev = append(s.ev, e.Type)
}

func (s *eventSink) types() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.ev...)
}

// assertDetailShape 校验摘要满足上报红线：单行、限长、无绝对路径、无凭据形态。
func assertDetailShape(t *testing.T, detail string) {
	t.Helper()
	if strings.TrimSpace(detail) == "" {
		t.Fatal("成功命令的 detail 不得为空（执行结果反馈缺失）")
	}
	if strings.ContainsAny(detail, "\r\n\t") {
		t.Errorf("detail 含折行（疑似 stdout/stderr 原文）: %q", detail)
	}
	if len(detail) > 240 {
		t.Errorf("detail 长度 %d 超过 edge 侧上限 240", len(detail))
	}
	for _, bad := range []string{`C:\`, `C:/`, "/home/", "/Users/", "/var/", "/tmp/"} {
		if strings.Contains(detail, bad) {
			t.Errorf("detail 含本机绝对路径 %q: %q", bad, detail)
		}
	}
	lower := strings.ToLower(detail)
	for _, bad := range []string{"token=", "password=", "secret=", "authorization", "bearer "} {
		if strings.Contains(lower, bad) {
			t.Errorf("detail 疑似含凭据形态 %q: %q", bad, detail)
		}
	}
}

// ---- 缺陷 D2：成功命令必须回真实执行结果摘要 ----

func TestSendWithResultDumpReportsRealFrame(t *testing.T) {
	d, _, _ := newTestDev(t, frame0148)
	detail, err := d.SendWithResult(context.Background(), driverkit.Command{Cmd: "dump"})
	if err != nil {
		t.Fatalf("dump: %v", err)
	}
	assertDetailShape(t, detail)
	for _, want := range []string{"dump_raw=S:00148000", "clock=01:48", "state=待机", "slots=待确认/待确认/待确认", "drift_min="} {
		if !strings.Contains(detail, want) {
			t.Errorf("dump 摘要缺少真实帧字段 %q: %q", want, detail)
		}
	}
}

func TestSendWithResultSyncReportsSyncedClock(t *testing.T) {
	d, fp, sink := newTestDev(t, frame0148)
	detail, err := d.SendWithResult(context.Background(), driverkit.Command{Cmd: "sync", Args: "0148"})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	assertDetailShape(t, detail)
	if !strings.HasPrefix(detail, "synced clock=01:48") {
		t.Fatalf("sync 摘要应回同步后的真实板钟: %q", detail)
	}
	if !strings.Contains(detail, "drift_min=") || !strings.Contains(detail, "state=待机") {
		t.Fatalf("sync 摘要应含漂移与状态机: %q", detail)
	}
	// 线上字节序列必须与协议一致：T0148 慢发 + 之后主动取帧 S。
	if w := fp.written(); !strings.HasPrefix(w, "T0148") || !strings.HasSuffix(w, "S") {
		t.Fatalf("写入字节序列 = %q, want 前缀 T0148 且以 S 结尾", w)
	}
	if len(sink.types()) == 0 {
		t.Log("提示：fakePort 未产生事件行（本用例只验证摘要与字节序列）")
	}
}

func TestSendWithResultSyncWithoutReplyIsHonest(t *testing.T) {
	d, _, _ := newTestDev(t, "") // 哑板：从不回帧
	detail, err := d.SendWithResult(context.Background(), driverkit.Command{Cmd: "sync", Args: "0148"})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	assertDetailShape(t, detail)
	if !strings.Contains(detail, "未收到回帧") {
		t.Fatalf("未回帧时必须诚实说明: %q", detail)
	}
	if strings.Contains(detail, "clock=") {
		t.Fatalf("未收到回帧却报了板钟（拿旧帧冒充同步结果）: %q", detail)
	}
}

func TestSendWithResultDumpWithoutReplyIsHonest(t *testing.T) {
	d, _, _ := newTestDev(t, "")
	detail, err := d.SendWithResult(context.Background(), driverkit.Command{Cmd: "dump"})
	if err != nil {
		t.Fatalf("dump: %v", err)
	}
	assertDetailShape(t, detail)
	if !strings.Contains(detail, "未收到回帧") {
		t.Fatalf("未回帧时必须诚实说明: %q", detail)
	}
}

func TestSendWithResultActuatorCommands(t *testing.T) {
	for _, tc := range []struct{ cmd, wire, want string }{
		{"trigger", "R", "trigger(R)"},
		{"open", "O", "open(O)"},
	} {
		d, fp, _ := newTestDev(t, frame0148)
		detail, err := d.SendWithResult(context.Background(), driverkit.Command{Cmd: tc.cmd})
		if err != nil {
			t.Fatalf("%s: %v", tc.cmd, err)
		}
		assertDetailShape(t, detail)
		if !strings.HasPrefix(detail, tc.want) {
			t.Errorf("%s 摘要前缀 = %q, want %q", tc.cmd, detail, tc.want)
		}
		if !strings.Contains(detail, "clock=01:48") {
			t.Errorf("%s 摘要应含执行后的真实帧: %q", tc.cmd, detail)
		}
		if !strings.HasPrefix(fp.written(), tc.wire) {
			t.Errorf("%s 写入字节 = %q, want 前缀 %q", tc.cmd, fp.written(), tc.wire)
		}
	}
}

func TestSendWithResultRaw(t *testing.T) {
	d, fp, _ := newTestDev(t, frame0148)
	detail, err := d.SendWithResult(context.Background(), driverkit.Command{Cmd: "raw", Args: "S"})
	if err != nil {
		t.Fatalf("raw: %v", err)
	}
	assertDetailShape(t, detail)
	if !strings.HasPrefix(detail, "raw(S)") {
		t.Fatalf("raw 摘要前缀 = %q", detail)
	}
	if !strings.Contains(fp.written(), "S") {
		t.Fatalf("raw 未把 args 原样写线: %q", fp.written())
	}
}

// TestSendWithResultISPDoesNotBlockOnReply 验证 ISP 不做无意义的等帧：
// 进入 ISP 后固件停止回应，摘要必须立即给出且诚实说明后果。
func TestSendWithResultISPDoesNotBlockOnReply(t *testing.T) {
	d, _, _ := newTestDev(t, "")
	t0 := time.Now()
	detail, err := d.SendWithResult(context.Background(), driverkit.Command{Cmd: "isp"})
	if err != nil {
		t.Fatalf("isp: %v", err)
	}
	if elapsed := time.Since(t0); elapsed > 300*time.Millisecond {
		t.Fatalf("isp 摘要不该等回帧，耗时 %v", elapsed)
	}
	assertDetailShape(t, detail)
	if !strings.Contains(detail, "ISP") || !strings.Contains(detail, "重新上电") {
		t.Fatalf("isp 摘要应说明后果: %q", detail)
	}
}

func TestSendWithResultPropagatesFailure(t *testing.T) {
	d, fp, _ := newTestDev(t, frame0148)
	fp.mu.Lock()
	fp.writeErr = errors.New("写失败")
	fp.mu.Unlock()
	detail, err := d.SendWithResult(context.Background(), driverkit.Command{Cmd: "dump"})
	if err == nil {
		t.Fatalf("写失败必须返回错误，got detail=%q", detail)
	}
	if detail != "" {
		t.Fatalf("失败时不得给出成功摘要: %q", detail)
	}
	// 端口死亡后同样 fail-closed。
	d.markDead()
	if _, err := d.SendWithResult(context.Background(), driverkit.Command{Cmd: "ping"}); err == nil {
		t.Fatal("端口死后命令必须失败")
	}
}

func TestSendWithResultUnknownCommand(t *testing.T) {
	d, _, _ := newTestDev(t, frame0148)
	if _, err := d.SendWithResult(context.Background(), driverkit.Command{Cmd: "rm -rf /"}); err == nil {
		t.Fatal("白名单外命令必须被拒绝")
	}
}

// ---- 缺陷 D1：observation 必须带真实采集时刻 ----

// TestDescriptorObservationsCarryRealObservedAt 锁定 5 个 entity 的观测时间戳：
// observed_at = 该帧 dump 被解析出来的真实时刻（不是零值，也不是「现在」硬填），
// received_at 由可信 Edge 盖戳，适配器不得自填。
func TestDescriptorObservationsCarryRealObservedAt(t *testing.T) {
	d := &dev{id: "d1", done: make(chan struct{})}
	parsedAt := time.Now()
	d.handleLine(strings.TrimSpace(frame0148)) // 真实解析路径
	// 故意等一段：若 observed_at 是 Descriptor() 里硬填的 now，就会与解析时刻脱节。
	time.Sleep(250 * time.Millisecond)

	desc := d.Descriptor()
	if err := desc.Validate(); err != nil {
		t.Fatalf("descriptor.Validate: %v", err)
	}
	wantEntities := []string{
		"clock", "alarm",
		"compartment-1", "compartment-2", "compartment-3",
		"temperature", "illuminance", "hall", "vibration", "key",
		"buzzer", "led", "display", "motor",
	}
	if len(desc.Entities) != len(wantEntities) {
		t.Fatalf("entity 数 = %d, want %d", len(desc.Entities), len(wantEntities))
	}
	checked := 0
	for _, e := range desc.Entities {
		for name, o := range e.Observations {
			checked++
			if o.ObservedAt.IsZero() {
				t.Errorf("entity %q observation %q 的 observed_at 为零值（缺陷 D1）", e.EntityID, name)
				continue
			}
			if o.ObservedAt.Before(parsedAt.Add(-time.Second)) || o.ObservedAt.After(parsedAt.Add(time.Second)) {
				t.Errorf("entity %q observation %q 的 observed_at=%v 不在真实解析时刻 %v 附近",
					e.EntityID, name, o.ObservedAt, parsedAt)
			}
			if !o.ReceivedAt.IsZero() {
				t.Errorf("entity %q observation %q 的 received_at 应由 Edge 盖戳，适配器不得自填", e.EntityID, name)
			}
			if o.Quality != model.QualityGood {
				t.Errorf("entity %q observation %q 的 quality = %q", e.EntityID, name, o.Quality)
			}
		}
	}
	if checked != 5 {
		t.Fatalf("仅转储帧时 5 个 entity 应有观测，实际 %d 条", checked)
	}
	// 只有转储帧（无 V 帧）时，9 个新传感器/执行器 entity 不得凭空合成观测。
	for _, id := range []string{"temperature", "illuminance", "hall", "vibration", "key", "buzzer", "led", "display", "motor"} {
		for _, e := range desc.Entities {
			if e.EntityID != id {
				continue
			}
			if len(e.Observations) != 0 {
				t.Errorf("无 V 帧时 entity %q 不得带观测: %+v", id, e.Observations)
			}
		}
	}
	if desc.Status != model.DeviceOnline {
		t.Fatalf("有帧时 status = %q, want online", desc.Status)
	}
}

// TestDescriptorWithoutDumpHasNoFakeObservations 反向验证：没有真实帧时
// 不得凭空合成观测值（宁缺勿假）。
func TestDescriptorWithoutDumpHasNoFakeObservations(t *testing.T) {
	d := &dev{id: "d1", done: make(chan struct{})}
	desc := d.Descriptor()
	if desc.Status != model.DeviceUnavailable {
		t.Fatalf("无帧时 status = %q, want unavailable", desc.Status)
	}
	for _, e := range desc.Entities {
		if len(e.Observations) != 0 {
			t.Errorf("entity %q 无帧却带了观测值: %+v", e.EntityID, e.Observations)
		}
	}
}

// ---- 固件 v2：V 帧全量传感器快照 + B/L/N/M 执行器命令 ----

func TestSendWithResultSensorReportsRealFrame(t *testing.T) {
	d, fp, _ := newTestDev(t, frame0148)
	fp.replyV = frameSensorV + "\n"
	detail, err := d.SendWithResult(context.Background(), driverkit.Command{Cmd: "sensor"})
	if err != nil {
		t.Fatalf("sensor: %v", err)
	}
	assertDetailShape(t, detail)
	for _, want := range []string{
		"sensor_raw=" + frameSensorV, "clock=09:30:55", "state=逾期", "temp_c=", "rop=", "hall=1", "vib=0", "key=0",
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("sensor 摘要缺少 %q: %q", want, detail)
		}
	}
	if w := fp.written(); w != "V" {
		t.Fatalf("sensor 应写单字节 V, got %q", w)
	}
}

func TestSendWithResultSensorWithoutReplyIsHonest(t *testing.T) {
	d, _, _ := newTestDev(t, "") // 哑板：从不回任何帧
	detail, err := d.SendWithResult(context.Background(), driverkit.Command{Cmd: "sensor"})
	if err != nil {
		t.Fatalf("sensor: %v", err)
	}
	assertDetailShape(t, detail)
	if !strings.Contains(detail, "未收到回帧") {
		t.Fatalf("无 V 帧回帧时必须诚实说明（不伪造传感器值）: %q", detail)
	}
	if strings.Contains(detail, "temp_c=") {
		t.Fatalf("未收到回帧却报了温度值（拿旧帧冒充）: %q", detail)
	}
}

func TestSendWithResultActuatorCommandsV2(t *testing.T) {
	for _, tc := range []struct{ cmd, args, wire, want string }{
		{"buzzer", `{"freq":4,"duration":3}`, "B43", "buzzer(B)"},
		{"led", `{"pattern":9}`, "L90", "led(L)"},
		{"display", `{"digits":[1,2,3,4,5,6,7,8]}`, "N12345678", "display(N)"},
		{"motor", `{"steps":2}`, "M2", "motor(M)"},
	} {
		d, fp, _ := newTestDev(t, frame0148)
		detail, err := d.SendWithResult(context.Background(), driverkit.Command{Cmd: tc.cmd, Args: tc.args})
		if err != nil {
			t.Fatalf("%s: %v", tc.cmd, err)
		}
		assertDetailShape(t, detail)
		if !strings.HasPrefix(detail, tc.want) {
			t.Errorf("%s 摘要前缀 = %q, want %q", tc.cmd, detail, tc.want)
		}
		if w := fp.written(); w != tc.wire {
			t.Errorf("%s 写入字节 = %q, want %q", tc.cmd, w, tc.wire)
		}
	}
}

func TestSendWithResultActuatorInvalidArgs(t *testing.T) {
	d, _, _ := newTestDev(t, frame0148)
	for _, c := range []driverkit.Command{
		{Cmd: "buzzer", Args: `{"freq":10,"duration":3}`},
		{Cmd: "led", Args: `{"pattern":10}`},
		{Cmd: "display", Args: `{"digits":[1,2,3]}`},
		{Cmd: "motor", Args: `{"steps":9}`},
		{Cmd: "led", Args: ""},
	} {
		if _, err := d.SendWithResult(context.Background(), c); err == nil {
			t.Errorf("命令 %s args=%q 越界/非法必须被拒绝", c.Cmd, c.Args)
		}
	}
}
