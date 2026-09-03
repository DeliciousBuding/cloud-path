package edge

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/DeliciousBuding/cloud-path/internal/api"
)

const (
	wsReadLimit  = 64 << 10
	wsWriteWait  = 5 * time.Second
	wsPingPeriod = 30 * time.Second
	sendQueue    = 256
	// offlineBufferCap 是断线期间缓冲的事件条数上限：事件不可重放（丢了就没了），
	// 状态消息幂等（下一拍会重发）所以直接丢。超上限丢最旧保最新。
	offlineBufferCap = 512
	// ackDoubtWindow 是 command_ack 的「写达存疑窗口」。
	//
	// ws.Write 返回 nil 只说明字节进了本端 socket，不代表对端收到：连接可能正在
	// 死（FIN/RST 还没被读泵观察到），此时写仍然「成功」。会话若在写入后这段
	// 时间内死掉，该 ack 视为**未写达**并退回离线缓冲，重连后重发——否则一次
	// 真实的执行结果（尤其 failed）会永久丢失，命令在 Server 上永远停在 sent。
	//
	// 只有 command_ack 走这条写前保护：它按 command_id 幂等（server 侧是
	// UPDATE by id），重发安全；event 在冻结契约里没有去重键，重发会在 server
	// 事件流产生重复行，因此事件保持既有尽力语义（宁漏不重）。
	ackDoubtWindow = 500 * time.Millisecond
)

// wsClient 维护到 server 的 WS 长连接：自动重连（指数退避+抖动）、
// 串行写队列（慢网不阻塞采集）、hello 注册、断线事件缓冲与重连回放。
type wsClient struct {
	url     string
	token   string
	version string

	// httpClient 用于 WS 拨号。nil 表示用系统信任库的缺省 client
	// （公网 wss:// + 正规证书即走这条路）；测试注入信任自签 CA 的 client
	// 以验证 TLS 拨号真的可用。
	httpClient *http.Client

	edgeID  string
	devices []api.DeviceMeta

	send chan []byte // 已序列化消息队列

	mu       sync.Mutex
	ws       *websocket.Conn
	online   bool
	pending  [][]byte // 离线期间缓冲的事件（有界，重连后回放）
	buffered int      // 累计缓冲条数（诊断）
	dropped  int      // 缓冲溢出丢弃条数（诊断）

	onCommand func(api.Envelope)
	onOnline  func() // 重连成功后回调（上层据此强制补报状态）
	// onPluginDesired 处理 server 下发的插件期望态快照（未配置插件控制面时为 nil）。
	onPluginDesired func(api.Envelope)
}

// setPluginHandler 注入插件期望态处理器（未导出：由 Run 按配置接线）。
func (c *wsClient) setPluginHandler(fn func(api.Envelope)) { c.onPluginDesired = fn }

func newWSClient(cfg *Config, version string, metas []api.DeviceMeta,
	onCommand func(api.Envelope), onOnline func()) *wsClient {
	return &wsClient{
		url: cfg.Server, token: cfg.Token, version: version,
		edgeID: cfg.EdgeID, devices: metas,
		send: make(chan []byte, sendQueue), onCommand: onCommand, onOnline: onOnline,
	}
}

// Online 返回当前是否已连上 server。
func (c *wsClient) Online() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.online
}

// Buffered 返回离线缓冲中的事件条数（诊断/测试用）。
func (c *wsClient) Buffered() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.pending)
}

func (c *wsClient) setOnline(v bool) {
	c.mu.Lock()
	c.online = v
	c.mu.Unlock()
}

// run 是重连主循环，ctx 取消即退出。
func (c *wsClient) run(ctx context.Context) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		t0 := time.Now()
		err := c.session(ctx)
		if ctx.Err() != nil {
			return
		}
		slog.Warn("server connection lost, reconnecting", "err", err, "backoff", backoff,
			"buffered_events", c.Buffered())
		c.setOnline(false)
		if time.Since(t0) > time.Minute {
			backoff = time.Second // 健康会话后重置退避
		}
		sleep := backoff + time.Duration(float64(backoff)*0.2*(rand.Float64()*2-1)) // ±20% 抖动防惊群
		select {
		case <-ctx.Done():
			return
		case <-time.After(sleep):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

// session 建立一次连接并跑到断开。
func (c *wsClient) session(ctx context.Context) error {
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// ws:// 与 wss:// 走同一条拨号路径：wss 由 net/http 完成 TLS 握手与证书校验
	// （缺省用系统信任库，公网正规证书直接可用；校验失败即重连退避，不降级明文）。
	hc := c.httpClient
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	ws, _, err := websocket.Dial(dialCtx, c.url, &websocket.DialOptions{HTTPClient: hc})
	if err != nil {
		return err
	}
	ws.SetReadLimit(wsReadLimit)
	defer ws.CloseNow()

	c.mu.Lock()
	c.ws = ws
	c.mu.Unlock()

	// hello 注册
	hello := api.HelloData{EdgeID: c.edgeID, Token: c.token, Version: c.version, Devices: c.devices}
	if err := c.writeEnvelope(ctx, ws, api.Envelope{
		V: api.Version, Type: api.MsgHello, Ts: time.Now().Unix(),
		Data: mustJSON(hello),
	}); err != nil {
		return err
	}
	c.setOnline(true)
	slog.Info("connected to server", "url", c.url, "edge", c.edgeID)
	if n := c.flushPending(); n > 0 {
		slog.Info("replayed buffered events", "count", n, "edge", c.edgeID)
	}
	if c.onOnline != nil {
		c.onOnline()
	}

	sessCtx, sessCancel := context.WithCancel(ctx)
	defer sessCancel()
	// 本次会话的写达存疑状态（只跟踪 command_ack）。
	sess := newWSSession()

	// 写泵
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer sessCancel()
		for {
			select {
			case <-sessCtx.Done():
				return
			case msg := <-c.send:
				wctx, cancel := context.WithTimeout(sessCtx, wsWriteWait)
				err := ws.Write(wctx, websocket.MessageText, msg)
				cancel()
				if err != nil {
					// 消息已经出队却发现连接正在死：必须放回离线缓冲，
					// 否则一次真实的 failed ack / 事件就被静默吞掉了。
					c.requeue(msg)
					return
				}
				if isAckPayload(msg) {
					// 写返回 nil 不等于对端收到：进入存疑窗口。会话在窗口内死掉
					// 则由 drain 退回缓冲重发（幂等），窗口内仍存活才算真写达。
					sess.track(c, msg)
				}
			}
		}
	}()
	// ping 泵：失败即取消会话（半开连接收敛）
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer sessCancel()
		t := time.NewTicker(wsPingPeriod)
		defer t.Stop()
		for {
			select {
			case <-sessCtx.Done():
				return
			case <-t.C:
				pctx, cancel := context.WithTimeout(sessCtx, wsWriteWait)
				err := ws.Ping(pctx)
				cancel()
				if err != nil {
					return
				}
			}
		}
	}()

	// 读循环（本协程）：处理 server 下行消息
	for {
		_, data, err := ws.Read(sessCtx)
		if err != nil {
			sessCancel()
			wg.Wait()
			// 写泵已停：把「写了但会话在存疑窗口内死掉」的 command_ack 退回
			// 离线缓冲，下一次会话重连后重发。顺序必须是 sessCancel → wg.Wait
			// → drain，否则会与写泵并发的 track 竞争。
			for _, lost := range sess.drain() {
				c.requeue(lost)
			}
			return err
		}
		var env api.Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			slog.Warn("bad server message", "err", err)
			continue
		}
		switch env.Type {
		case api.MsgCommand:
			if c.onCommand != nil {
				// 命令执行可能较慢（stcb 对时逐字节 250ms+），异步执行不阻塞读
				go c.onCommand(env)
			}
		case api.MsgPluginDesired:
			if c.onPluginDesired != nil {
				// 应用期望态可能要启动插件进程（秒级），同样异步不阻塞读；
				// Syncer 内部串行，多份快照不会交错写本地状态。
				go c.onPluginDesired(env)
				continue
			}
			// 本 Edge 未承载插件控制面：忽略并记 debug，绝不断开连接
			// （control-plane-sync.md §4 的向后兼容要求）。
			slog.Debug("plugin_desired ignored: plugin control plane disabled on this edge",
				"type", string(env.Type))
		case api.MsgPing, api.MsgPong:
			// 由 websocket 库与本客户端的 ping 泵处理
		default:
			// 未知/浏览器向消息：忽略并记 debug，不断开连接。
			slog.Debug("ignoring server message", "type", string(env.Type))
		}
	}
}

func (c *wsClient) writeEnvelope(ctx context.Context, ws *websocket.Conn, env api.Envelope) error {
	data := mustJSON(env)
	wctx, cancel := context.WithTimeout(ctx, wsWriteWait)
	defer cancel()
	return ws.Write(wctx, websocket.MessageText, data)
}

// enqueue 入队一条消息。
//   - 在线：投递到写队列（队满则事件/ack 转缓冲、状态丢弃）
//   - 离线：事件与命令 ack 进缓冲等重连回放，状态直接丢（幂等，下一拍重发）
//
// 命令 ack 必须缓冲：命令是在连接活着的时候收到的，执行完却可能已经断开——
// 若直接丢弃，一次真实的 failed 就永远到不了 Server，命令会停在 sent
// （「断线期间设备命令不得静默丢失成成功」）。ack 按 command_id 幂等，重放安全。
//
// 返回 true 表示已进入写队列（状态上报据此更新 diff 基线）。
func (c *wsClient) enqueue(env api.Envelope) bool {
	data := mustJSON(env)
	if data == nil {
		return false
	}
	c.mu.Lock()
	online := c.online
	c.mu.Unlock()

	if online {
		select {
		case c.send <- data:
			return true
		default:
			slog.Warn("send queue full", "type", env.Type, "device", env.Device)
			if isBufferable(env.Type) {
				c.buffer(env, data)
			}
			return false
		}
	}
	if isBufferable(env.Type) {
		c.buffer(env, data)
	}
	return false
}

// isBufferable 报告该类型消息是否进离线缓冲：不可重放的事件与命令 ack 进缓冲；
// 状态/Descriptor 幂等（重连后由 onOnline 全量重报），缓冲它们只会挤掉真事件。
func isBufferable(t api.MsgType) bool {
	return t == api.MsgEvent || t == api.MsgCommandAck
}

// requeue 把一条**已出队但写失败**的消息放回离线缓冲（仅限可缓冲类型）。
// 与 buffer 的区别只在调用时机：buffer 发生在入队时（已知离线），requeue 发生在
// 出队后写失败时（连接正在死）。两者都必须存在，否则消息会从缝里漏掉。
func (c *wsClient) requeue(data []byte) {
	typ, ok := payloadType(data)
	if !ok || !isBufferable(typ) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.pending) >= offlineBufferCap {
		c.pending = append(c.pending[:0], c.pending[1:]...)
		c.dropped++
	}
	c.pending = append(c.pending, data)
	c.buffered++
	slog.Warn("undelivered message requeued for replay", "type", string(typ),
		"buffered", len(c.pending))
}

// payloadType 从一条已序列化信封里取出消息类型。
func payloadType(data []byte) (api.MsgType, bool) {
	var probe struct {
		Type api.MsgType `json:"type"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return "", false
	}
	return probe.Type, true
}

// isAckPayload 报告一条已序列化信封是否为 command_ack（唯一需要写达确认的类型）。
func isAckPayload(data []byte) bool {
	typ, ok := payloadType(data)
	return ok && typ == api.MsgCommandAck
}

// wsSession 跟踪一次连接的「写达存疑」状态。
//
// 存在的理由：TCP 上写成功与对端收到之间没有本地可判定的因果——连接正在死时
// ws.Write 依然可能返回 nil。对幂等的 command_ack，我们用一段时间窗口把「写了
// 但会话随即死掉」判定为未写达并退回缓冲重发，从而关掉「ack 从缝里漏掉、命令
// 永远停在 sent」这条丢失路径。
type wsSession struct {
	mu       sync.Mutex
	dead     bool
	inflight [][]byte // 已写出但尚未越过存疑窗口的 command_ack
}

func newWSSession() *wsSession { return &wsSession{} }

// track 记录一条已成功写出的 command_ack。会话已死则直接退回缓冲（写在了正在
// 死的连接上）；否则挂一个存疑窗口定时器，窗口内会话仍存活才算真写达。
func (s *wsSession) track(c *wsClient, data []byte) {
	s.mu.Lock()
	if s.dead {
		s.mu.Unlock()
		c.requeue(data)
		return
	}
	s.inflight = append(s.inflight, data)
	s.mu.Unlock()
	time.AfterFunc(ackDoubtWindow, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.dead {
			return // 会话已死：drain 已接管，这里不得再移除
		}
		s.inflight = removeFirstEqual(s.inflight, data)
	})
}

// drain 标记会话死亡并取出全部未确认写达的 command_ack（调用方负责退回缓冲）。
func (s *wsSession) drain() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dead = true
	out := s.inflight
	s.inflight = nil
	return out
}

// unconfirmed 返回仍未确认写达的 ack 条数（诊断/测试用）。
func (s *wsSession) unconfirmed() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.inflight)
}

// removeFirstEqual 按内容相等移除第一条匹配项（同一 payload 可能被重发多次，
// 每次写达各自确认一条）。
func removeFirstEqual(list [][]byte, data []byte) [][]byte {
	for i, d := range list {
		if bytes.Equal(d, data) {
			return append(list[:i:i], list[i+1:]...)
		}
	}
	return list
}

// buffer 把不可重放消息（事件 / 命令 ack）放入离线缓冲（有界，超限丢最旧）。
func (c *wsClient) buffer(env api.Envelope, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.pending) >= offlineBufferCap {
		c.pending = append(c.pending[:0], c.pending[1:]...)
		c.dropped++
	}
	c.pending = append(c.pending, data)
	c.buffered++
	if c.buffered == 1 || c.buffered%50 == 0 {
		slog.Warn("server offline, buffering events", "buffered", len(c.pending),
			"total", c.buffered, "dropped", c.dropped, "type", env.Type)
	}
}

// flushPending 重连后把缓冲的事件与命令 ack 回放进写队列，返回回放条数。
// 写队列满则把剩余部分放回缓冲，下一拍再试。
func (c *wsClient) flushPending() int {
	c.mu.Lock()
	pending := c.pending
	c.pending = nil
	c.mu.Unlock()
	if len(pending) == 0 {
		return 0
	}
	n := 0
	for i, d := range pending {
		select {
		case c.send <- d:
			n++
		default:
			c.mu.Lock()
			c.pending = append(pending[i:], c.pending...)
			c.mu.Unlock()
			return n
		}
	}
	return n
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		slog.Error("json marshal", "err", err)
		return nil
	}
	return b
}
