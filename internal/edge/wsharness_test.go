package edge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	_ "github.com/DeliciousBuding/cloud-path/examples/demo" // 参考演示设备（无硬件）：本包测试用它跑真实 edge 运行时
	"github.com/DeliciousBuding/cloud-path/internal/api"
)

// ---- 记录型 WS server：收下 edge 全部上行信封，并可下发命令 ----

// edgeRecorder 是一台可编程的假 server。它保存所有收到的信封，并保留最近一条
// 活跃连接用于下发 command / plugin_desired，从而在真实 WS 上驱动真实 edge 运行时。
type edgeRecorder struct {
	mu    sync.Mutex
	envs  []api.Envelope
	conns int
	cur   *websocket.Conn

	srv    *httptest.Server
	closer func() // 由测试注入的主动断连动作（重连测试用）
}

// startEdgeRecorder 启动记录型 server。tls=true 时用 httptest.NewTLSServer，
// edge 必须以 wss:// 拨号（公网 TLS 路径）。
func startEdgeRecorder(t *testing.T, tls bool) *edgeRecorder {
	t.Helper()
	rec := &edgeRecorder{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
		if err != nil {
			return
		}
		rec.mu.Lock()
		rec.conns++
		rec.cur = ws
		rec.mu.Unlock()
		defer func() {
			rec.mu.Lock()
			if rec.cur == ws {
				rec.cur = nil
			}
			rec.mu.Unlock()
			ws.CloseNow()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		for {
			_, data, err := ws.Read(ctx)
			if err != nil {
				return
			}
			var env api.Envelope
			if err := json.Unmarshal(data, &env); err != nil {
				continue
			}
			rec.mu.Lock()
			rec.envs = append(rec.envs, env)
			rec.mu.Unlock()
		}
	})
	if tls {
		rec.srv = httptest.NewTLSServer(handler)
	} else {
		rec.srv = httptest.NewServer(handler)
	}
	t.Cleanup(rec.srv.Close)
	return rec
}

// url 返回 edge 应连接的 WS 端点。scheme 为 ws 或 wss。
func (r *edgeRecorder) url(scheme string) string {
	host := strings.TrimPrefix(strings.TrimPrefix(r.srv.URL, "http://"), "https://")
	return scheme + "://" + host + "/ws/edge"
}

// client 返回信任本 server 证书的 HTTP client（wss 拨号注入用）。
func (r *edgeRecorder) client() *http.Client { return r.srv.Client() }

func (r *edgeRecorder) all() []api.Envelope {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]api.Envelope, len(r.envs))
	copy(out, r.envs)
	return out
}

func (r *edgeRecorder) connections() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.conns
}

// dropCurrent 主动断开当前连接（模拟对端电脑掉线）。
func (r *edgeRecorder) dropCurrent() {
	r.mu.Lock()
	ws := r.cur
	r.cur = nil
	r.mu.Unlock()
	if ws != nil {
		ws.CloseNow()
	}
}

// send 向 edge 下发一条信封（命令 / 期望态）。
func (r *edgeRecorder) send(t *testing.T, env api.Envelope) {
	t.Helper()
	waitForEnvelope(t, 30*time.Second, func() bool {
		r.mu.Lock()
		ws := r.cur
		r.mu.Unlock()
		return ws != nil
	}, "edge 连接未建立")
	r.mu.Lock()
	ws := r.cur
	r.mu.Unlock()
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := ws.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatalf("下发 %s 失败: %v", env.Type, err)
	}
}

// sendCommand 下发一条设备命令。
func (r *edgeRecorder) sendCommand(t *testing.T, deviceKey string, commandID int64, cmd, args string) {
	t.Helper()
	data, _ := json.Marshal(api.CommandData{CommandID: commandID, Cmd: cmd, Args: args})
	r.send(t, api.Envelope{V: api.Version, Type: api.MsgCommand, Device: deviceKey,
		Ts: time.Now().Unix(), Data: data})
}

// mark 返回当前已收到的信封数，用于断言「重连之后」才到达的新消息。
func (r *edgeRecorder) mark() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.envs)
}

// envsFrom 返回第 from 条（0-based）之后收到的信封。
func (r *edgeRecorder) envsFrom(from int) []api.Envelope {
	all := r.all()
	if from >= len(all) {
		return nil
	}
	return all[from:]
}

// waitAfter 等待第 from 条之后出现满足条件的一批信封。
func (r *edgeRecorder) waitAfter(t *testing.T, from int, timeout time.Duration, cond func(envs []api.Envelope) bool, why string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond(r.envsFrom(from)) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s（超时 %v，重连后收到 %d 条信封）", why, timeout, len(r.envsFrom(from)))
}

// hasType 报告信封批中是否有某设备键的某类消息。
func hasType(envs []api.Envelope, typ api.MsgType, deviceKey string) bool {
	for _, e := range envs {
		if e.Type == typ && e.Device == deviceKey {
			return true
		}
	}
	return false
}

// count 统计满足条件的信封数。
func (r *edgeRecorder) count(match func(api.Envelope) bool) int {
	n := 0
	for _, e := range r.all() {
		if match(e) {
			n++
		}
	}
	return n
}

// lastState 返回某设备键最近一次 state 载荷。
func (r *edgeRecorder) lastState(key string) (api.StateData, bool) {
	var out api.StateData
	found := false
	for _, e := range r.all() {
		if e.Type != api.MsgState || e.Device != key {
			continue
		}
		var st api.StateData
		if err := json.Unmarshal(e.Data, &st); err == nil {
			out, found = st, true
		}
	}
	return out, found
}

// waitState 等待某设备键状态满足条件。
func (r *edgeRecorder) waitState(t *testing.T, key string, cond func(api.StateData) bool) api.StateData {
	t.Helper()
	deadline := time.Now().Add(130 * time.Second)
	for time.Now().Before(deadline) {
		if st, ok := r.lastState(key); ok && cond(st) {
			return st
		}
		time.Sleep(20 * time.Millisecond)
	}
	st, ok := r.lastState(key)
	t.Fatalf("设备 %s 状态未在超时内满足条件（最后 ok=%v raw=%v）", key, ok, st.Raw)
	return st
}

// ackView 是带设备键的 ack 视图（api.AckData 是冻结契约，不带设备键）。
type ackView struct {
	api.AckData
	Device string
}

// waitAck 等待指定 command_id 的 ack。
func (r *edgeRecorder) waitAck(t *testing.T, commandID int64) ackView {
	t.Helper()
	deadline := time.Now().Add(130 * time.Second)
	for time.Now().Before(deadline) {
		for _, e := range r.all() {
			if e.Type != api.MsgCommandAck {
				continue
			}
			var ack api.AckData
			if err := json.Unmarshal(e.Data, &ack); err != nil || ack.CommandID != commandID {
				continue
			}
			return ackView{AckData: ack, Device: e.Device}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("未收到 command_id=%d 的 ack", commandID)
	return ackView{}
}

// waitHello 等待第 n 条（1-based）hello。
func (r *edgeRecorder) waitHello(t *testing.T, n int) api.HelloData {
	t.Helper()
	deadline := time.Now().Add(130 * time.Second)
	for time.Now().Before(deadline) {
		seen := 0
		for _, e := range r.all() {
			if e.Type != api.MsgHello {
				continue
			}
			var h api.HelloData
			if err := json.Unmarshal(e.Data, &h); err != nil {
				continue
			}
			seen++
			if seen >= n {
				return h
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("未收到第 %d 条 hello（已有 %d 条）", n, r.count(func(e api.Envelope) bool { return e.Type == api.MsgHello }))
	return api.HelloData{}
}

// waitForEnvelope 轮询等待条件成立，超时给出可读原因。
func waitForEnvelope(t *testing.T, timeout time.Duration, cond func() bool, why string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s（超时 %v）", why, timeout)
}

// rawInt 读取 State.Raw 中的整数字段（JSON 解码后为 float64）。
func rawInt(st api.StateData, key string) int64 {
	switch v := st.Raw[key].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	default:
		return -1
	}
}

// demoConfig 构造 N 台无硬件 demo 设备的 edge 配置。
// 心跳周期拉到 1h：ticks 恒为 0，状态断言完全确定（不依赖时序）。
func demoConfig(url string, ids ...string) *Config {
	cfg := &Config{
		Server: url, EdgeID: "e-demo",
		PollIntervalS: 1, SyncIntervalS: 3600, ReportIntervalS: 30,
	}
	for _, id := range ids {
		cfg.Devices = append(cfg.Devices, DeviceCfg{
			ID: id, Adapter: "demo", Name: "参考 " + id,
			Extra:       map[string]string{"tick_interval_s": "3600"},
			PollCommand: DefaultPollCommand,
			SyncCommand: DefaultSyncCommand,
		})
	}
	return cfg
}

// runEdge 启动 edge.Run 并在测试结束时收敛（取消 + 等待返回）。
func runEdge(t *testing.T, cfg *Config, opts ...RunOption) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg, "test", opts...) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil && !strings.Contains(fmt.Sprint(err), "context canceled") {
				t.Errorf("Run 返回错误: %v", err)
			}
		case <-time.After(30 * time.Second):
			t.Error("Run 未在取消后返回")
		}
	})
	return ctx
}
