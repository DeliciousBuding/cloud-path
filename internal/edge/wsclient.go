package edge

import (
	"context"
	"encoding/json"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/DeliciousBuding/cloudpath/internal/api"
)

const (
	wsReadLimit  = 64 << 10
	wsWriteWait  = 5 * time.Second
	wsPingPeriod = 30 * time.Second
	sendQueue    = 256
	// offlineBufferCap 是断线期间缓冲的事件条数上限：事件不可重放（丢了就没了），
	// 状态消息幂等（下一拍会重发）所以直接丢。超上限丢最旧保最新。
	offlineBufferCap = 512
)

// wsClient 维护到 server 的 WS 长连接：自动重连（指数退避+抖动）、
// 串行写队列（慢网不阻塞采集）、hello 注册、断线事件缓冲与重连回放。
type wsClient struct {
	url     string
	token   string
	version string

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
}

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
	ws, _, err := websocket.Dial(dialCtx, c.url, &websocket.DialOptions{
		HTTPClient: &http.Client{Timeout: 15 * time.Second},
	})
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
					return
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

	// 读循环（本协程）：处理 server 下行命令
	for {
		_, data, err := ws.Read(sessCtx)
		if err != nil {
			sessCancel()
			wg.Wait()
			return err
		}
		var env api.Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			slog.Warn("bad server message", "err", err)
			continue
		}
		if env.Type == api.MsgCommand && c.onCommand != nil {
			// 命令执行可能较慢（sync 逐字节 250ms+），异步执行不阻塞读
			go c.onCommand(env)
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
//   - 在线：投递到写队列（队满则事件转缓冲、状态丢弃）
//   - 离线：事件进缓冲等重连回放，状态直接丢（幂等，下一拍重发）
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
			if env.Type == api.MsgEvent {
				c.buffer(env, data)
			}
			return false
		}
	}
	if env.Type == api.MsgEvent {
		c.buffer(env, data)
	}
	return false
}

// buffer 把事件放入离线缓冲（有界，超限丢最旧）。
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

// flushPending 重连后把缓冲事件回放进写队列，返回回放条数。
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
