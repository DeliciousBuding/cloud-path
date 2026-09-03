package edge

import (
	"testing"
	"time"

	"github.com/DeliciousBuding/cloudpath/internal/api"
)

func testClient() *wsClient {
	cfg := &Config{Server: "ws://127.0.0.1:1/ws/edge", EdgeID: "e1", ReportIntervalS: 30}
	return newWSClient(cfg, "test", []api.DeviceMeta{{ID: "d1", Adapter: "stcb"}}, nil, nil)
}

func eventEnv(typ string) api.Envelope {
	return api.Envelope{V: api.Version, Type: api.MsgEvent, Device: "e1/d1",
		Ts: time.Now().Unix(), Data: mustJSON(api.EventData{Type: typ})}
}

func stateEnv() api.Envelope {
	return api.Envelope{V: api.Version, Type: api.MsgState, Device: "e1/d1",
		Ts: time.Now().Unix(), Data: mustJSON(api.StateData{Online: true, Raw: map[string]any{"hour": 1}})}
}

func drain(ch chan []byte) int {
	n := 0
	for {
		select {
		case <-ch:
			n++
		default:
			return n
		}
	}
}

// 离线时事件必须缓冲（不可重放），状态直接丢（幂等，下一拍重发）。
func TestEnqueueOfflineBuffersEventsOnly(t *testing.T) {
	c := testClient()
	if c.Online() {
		t.Fatal("新客户端应为离线")
	}
	if c.enqueue(eventEnv("REMIND")) {
		t.Fatal("离线 enqueue 不应报告已入队")
	}
	if got := c.Buffered(); got != 1 {
		t.Fatalf("缓冲事件 = %d, want 1", got)
	}
	if c.enqueue(stateEnv()) {
		t.Fatal("离线状态不应入队")
	}
	if got := c.Buffered(); got != 1 {
		t.Fatalf("状态不应进缓冲，got %d", got)
	}
	if n := drain(c.send); n != 0 {
		t.Fatalf("离线不应有消息进写队列，got %d", n)
	}
}

func TestEnqueueOnlineGoesToSendChan(t *testing.T) {
	c := testClient()
	c.setOnline(true)
	if !c.enqueue(stateEnv()) {
		t.Fatal("在线状态应入写队列")
	}
	if !c.enqueue(eventEnv("BOOT")) {
		t.Fatal("在线事件应入写队列")
	}
	if n := drain(c.send); n != 2 {
		t.Fatalf("写队列 = %d, want 2", n)
	}
	if got := c.Buffered(); got != 0 {
		t.Fatalf("在线不应缓冲，got %d", got)
	}
}

// 在线但写队列满：事件转缓冲不丢，状态丢弃。
func TestEnqueueQueueFullFallsBackToBuffer(t *testing.T) {
	c := testClient()
	c.setOnline(true)
	for i := 0; i < sendQueue; i++ {
		c.send <- []byte("x")
	}
	if c.enqueue(eventEnv("MISSED")) {
		t.Fatal("队满不应报告已入队")
	}
	if got := c.Buffered(); got != 1 {
		t.Fatalf("队满事件应转缓冲，got %d", got)
	}
	c.enqueue(stateEnv())
	if got := c.Buffered(); got != 1 {
		t.Fatalf("队满状态应丢弃，got %d", got)
	}
}

func TestBufferOverflowDropsOldest(t *testing.T) {
	c := testClient()
	total := offlineBufferCap + 20
	for i := 0; i < total; i++ {
		c.buffer(eventEnv("BOOT"), []byte{byte(i)})
	}
	if got := c.Buffered(); got != offlineBufferCap {
		t.Fatalf("缓冲上限 = %d, want %d", got, offlineBufferCap)
	}
	c.mu.Lock()
	dropped, buffered := c.dropped, c.buffered
	first := c.pending[0][0]
	c.mu.Unlock()
	if dropped != 20 || buffered != total {
		t.Fatalf("dropped=%d buffered=%d, want 20/%d", dropped, buffered, total)
	}
	if first != 20 {
		t.Fatalf("应丢最旧保最新：首条 = %d, want 20", first)
	}
}

func TestFlushPendingReplays(t *testing.T) {
	c := testClient()
	for i := 0; i < 5; i++ {
		c.buffer(eventEnv("BOOT"), []byte{byte(i)})
	}
	if n := c.flushPending(); n != 5 {
		t.Fatalf("回放 = %d, want 5", n)
	}
	if got := c.Buffered(); got != 0 {
		t.Fatalf("回放后缓冲应清空，got %d", got)
	}
	if n := drain(c.send); n != 5 {
		t.Fatalf("写队列 = %d, want 5", n)
	}
	if n := c.flushPending(); n != 0 {
		t.Fatalf("空缓冲回放 = %d, want 0", n)
	}
}

// 写队列满时部分回放，剩余留在缓冲等下一拍。
func TestFlushPendingPartial(t *testing.T) {
	c := testClient()
	for i := 0; i < sendQueue+10; i++ {
		c.buffer(eventEnv("BOOT"), []byte("e"))
	}
	n := c.flushPending()
	if n != sendQueue {
		t.Fatalf("部分回放 = %d, want %d", n, sendQueue)
	}
	if got := c.Buffered(); got != 10 {
		t.Fatalf("剩余缓冲 = %d, want 10", got)
	}
}

// 状态上报：变化即发、无变化抑制、force 无视 diff、超心跳间隔兜底重发。
func TestReportStateDiffSuppression(t *testing.T) {
	c := testClient()
	c.setOnline(true)
	e := &Edge{cfg: &Config{ReportIntervalS: 30}, client: c, sups: map[string]*supervisor{}}
	sup := &supervisor{}
	e.sups["e1/d1"] = sup

	e.reportState("e1/d1", sup, false)
	if n := drain(c.send); n != 1 {
		t.Fatalf("首次上报应发送，got %d", n)
	}
	e.reportState("e1/d1", sup, false)
	if n := drain(c.send); n != 0 {
		t.Fatalf("无变化应被抑制，got %d", n)
	}
	e.reportState("e1/d1", sup, true)
	if n := drain(c.send); n != 1 {
		t.Fatalf("force 应无视 diff，got %d", n)
	}
	// 心跳兜底：把上次发送时间拨老
	sup.mu.Lock()
	sup.sentAt = time.Now().Add(-time.Minute)
	sup.mu.Unlock()
	e.reportState("e1/d1", sup, false)
	if n := drain(c.send); n != 1 {
		t.Fatalf("超心跳间隔应兜底重发，got %d", n)
	}
}

// 重连回调应触发全部设备强制补报（断线期间状态被丢弃，必须刷新面板）。
func TestOnServerOnlineForcesReport(t *testing.T) {
	var c *wsClient
	e := &Edge{cfg: &Config{ReportIntervalS: 30}, sups: map[string]*supervisor{}}
	c = newWSClient(&Config{Server: "ws://x/ws/edge", EdgeID: "e1", ReportIntervalS: 30},
		"test", nil, nil, e.onServerOnline)
	e.client = c
	e.sups["e1/d1"] = &supervisor{}
	e.sups["e1/d2"] = &supervisor{}

	c.setOnline(true)
	c.onOnline()
	if n := drain(c.send); n != 2 {
		t.Fatalf("重连补报 = %d, want 2", n)
	}
}
