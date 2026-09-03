package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/DeliciousBuding/cloud-path/internal/api"
)

const (
	wsReadLimit   = 64 << 10 // 单条 WS 消息上限 64KB（状态/事件足够）
	wsWriteWait   = 5 * time.Second
	wsPingPeriod  = 30 * time.Second
	sendChanSize  = 256
	helloTimeout  = 10 * time.Second
	browserReadLm = 4096
)

// writePump 串行写循环：从 send chan 取已序列化消息写 WS，ctx 取消即退出。
func writePump(ctx context.Context, ws *websocket.Conn, send <-chan []byte) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-send:
			if !ok {
				return
			}
			wctx, cancel := context.WithTimeout(ctx, wsWriteWait)
			err := ws.Write(wctx, websocket.MessageText, msg)
			cancel()
			if err != nil {
				slog.Debug("ws write failed", "err", err)
				return
			}
		}
	}
}

// pingPump 周期 Ping 保活。失败即 cancel —— 半开连接（对端断电/网络黑洞）
// 靠 Ping 超时收敛，读循环随后退出并触发清理。
func pingPump(ctx context.Context, cancel context.CancelFunc, ws *websocket.Conn) {
	t := time.NewTicker(wsPingPeriod)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			pctx, pcancel := context.WithTimeout(ctx, wsWriteWait)
			err := ws.Ping(pctx)
			pcancel()
			if err != nil {
				slog.Debug("ws ping failed, closing", "err", err)
				cancel()
				return
			}
		}
	}
}

// ---------- edge 接入 ----------

// handleEdgeWS 处理 edge 长连接：hello 鉴权注册 → 读循环（state/event/ack）→ 断线清理。
func (s *Server) handleEdgeWS(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, s.acceptOptsPtr())
	if err != nil {
		slog.Warn("edge ws accept failed", "err", err, "remote", r.RemoteAddr,
			"origin", r.Header.Get("Origin"))
		return
	}
	ws.SetReadLimit(wsReadLimit)
	defer ws.CloseNow()

	// 1) 等 hello
	hctx, hcancel := context.WithTimeout(r.Context(), helloTimeout)
	_, data, err := ws.Read(hctx)
	hcancel()
	if err != nil {
		ws.Close(websocket.StatusProtocolError, "hello timeout")
		return
	}
	var env api.Envelope
	if err := json.Unmarshal(data, &env); err != nil || env.Type != api.MsgHello {
		ws.Close(websocket.StatusProtocolError, "first message must be hello")
		return
	}
	var hello api.HelloData
	if err := json.Unmarshal(env.Data, &hello); err != nil || hello.EdgeID == "" {
		ws.Close(websocket.StatusProtocolError, "bad hello payload")
		return
	}
	if !validEdgeID(hello.EdgeID) {
		ws.Close(websocket.StatusProtocolError, "invalid edge_id")
		slog.Warn("edge rejected: invalid edge_id", "edge", hello.EdgeID, "remote", r.RemoteAddr)
		return
	}
	if s.cfg.Token != "" && hello.Token != s.cfg.Token {
		ws.Close(websocket.StatusPolicyViolation, "invalid token")
		slog.Warn("edge auth failed", "edge", hello.EdgeID, "remote", r.RemoteAddr)
		return
	}

	// 2) 注册连接（同 edge_id 重连挤掉旧连接：新连接优先）
	ctx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
	defer cancel()
	link := &edgeLink{
		edgeID: hello.EdgeID, version: hello.Version,
		connectedAt: time.Now(), send: make(chan []byte, sendChanSize),
		cancel: cancel,
	}
	for _, d := range hello.Devices {
		link.devices = append(link.devices, api.DeviceKey(hello.EdgeID, d.ID))
	}

	s.mu.Lock()
	if old, ok := s.edges[hello.EdgeID]; ok && old != link {
		slog.Info("edge reconnected, evicting old link", "edge", hello.EdgeID)
		old.cancel()
	}
	s.edges[hello.EdgeID] = link
	metas := s.applyMeta(hello.EdgeID, hello.Devices)
	s.mu.Unlock()
	s.persistDevices(hello.EdgeID, metas) // 落库在锁外
	slog.Info("edge connected", "edge", hello.EdgeID, "devices", link.devices, "version", hello.Version)

	edgeData, _ := json.Marshal(api.EdgeUpData{EdgeID: hello.EdgeID, Devices: link.devices, Version: hello.Version})
	s.broadcast(api.Envelope{V: api.Version, Type: api.MsgEdgeUp, Device: hello.EdgeID, Ts: time.Now().Unix(), Data: edgeData})

	// 3) 断线清理：设备全部标离线并广播
	defer func() {
		s.mu.Lock()
		current := s.edges[hello.EdgeID] == link
		if current {
			delete(s.edges, hello.EdgeID)
		}
		s.mu.Unlock()
		if current { // 只有仍是注册连接时才标离线（被重连挤掉的旧连接不清新状态）
			s.markEdgeOffline(link)
		}
		s.broadcast(api.Envelope{V: api.Version, Type: api.MsgEdgeDown, Device: hello.EdgeID, Ts: time.Now().Unix(), Data: edgeData})
		slog.Info("edge disconnected", "edge", hello.EdgeID, "was_current", current)
	}()

	go writePump(ctx, ws, link.send)
	go pingPump(ctx, cancel, ws)

	// 4) 读循环
	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			if code := websocket.CloseStatus(err); code != -1 {
				slog.Info("edge ws closed", "edge", hello.EdgeID, "code", code)
			} else {
				slog.Info("edge ws read ended", "edge", hello.EdgeID, "err", err)
			}
			return
		}
		var msg api.Envelope
		if err := json.Unmarshal(data, &msg); err != nil {
			slog.Warn("edge bad message", "edge", hello.EdgeID, "err", err)
			continue
		}
		if msg.V != api.Version {
			slog.Warn("edge version mismatch", "edge", hello.EdgeID, "v", msg.V)
			continue
		}
		// 安全：只接受该 edge 注册过的设备键
		if msg.Device != "" && !s.ownsDevice(link, msg.Device) {
			slog.Warn("edge sent foreign device key", "edge", hello.EdgeID, "device", msg.Device)
			continue
		}
		switch msg.Type {
		case api.MsgState:
			var st api.StateData
			if err := json.Unmarshal(msg.Data, &st); err != nil {
				slog.Warn("edge bad state payload", "edge", hello.EdgeID, "err", err)
				continue
			}
			s.mu.Lock()
			pend := s.applyState(msg.Device, &st)
			s.mu.Unlock()
			s.persistStates([]statePersist{pend}) // 落库在锁外
			msg.Ts = time.Now().Unix()
			s.broadcast(msg)
		case api.MsgEvent:
			var ev api.EventData
			if err := json.Unmarshal(msg.Data, &ev); err != nil {
				slog.Warn("edge bad event payload", "edge", hello.EdgeID, "err", err)
				continue
			}
			if ev.Type == "" {
				continue
			}
			if msg.Ts == 0 {
				msg.Ts = time.Now().Unix()
			}
			if s.cfg.Store != nil {
				payload, _ := json.Marshal(ev)
				id, err := s.cfg.Store.AddEvent(msg.Device, ev.Type, string(payload), msg.Ts)
				if err != nil {
					slog.Warn("store event", "err", err, "device", msg.Device)
				} else {
					slog.Info("event", "device", msg.Device, "type", ev.Type, "id", id)
				}
			}
			s.broadcast(msg)
		case api.MsgCommandAck:
			var ack api.AckData
			if err := json.Unmarshal(msg.Data, &ack); err != nil {
				slog.Warn("edge bad ack payload", "edge", hello.EdgeID, "err", err)
				continue
			}
			if s.cfg.Store != nil {
				if err := s.cfg.Store.UpdateCommandStatus(ack.CommandID, ack.Status, ack.Detail); err != nil {
					slog.Warn("store ack", "err", err, "cmd_id", ack.CommandID)
				}
			}
			slog.Info("command ack", "device", msg.Device, "cmd_id", ack.CommandID, "status", ack.Status)
			s.broadcast(msg)
		case api.MsgPong:
			// 库层已处理，忽略
		default:
			slog.Debug("edge unhandled msg type", "type", msg.Type)
		}
	}
}

// validEdgeID 限制 edge_id 形状：小写字母/数字/-/_，1..64 字符。
// 设备键是 "<edge_id>/<device_id>"，含 '/' 会破坏键解析。
func validEdgeID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_'
		if !ok {
			return false
		}
	}
	return true
}

func (s *Server) ownsDevice(link *edgeLink, key string) bool {
	prefix := link.edgeID + "/"
	if !strings.HasPrefix(key, prefix) {
		return false
	}
	for _, d := range link.devices {
		if d == key {
			return true
		}
	}
	return false
}

// ---------- 浏览器接入 ----------

// handleBrowserWS 浏览器实时订阅：连接即发全量快照，随后接收 fan-out。
func (s *Server) handleBrowserWS(w http.ResponseWriter, r *http.Request) {
	// 账号模式：会话 cookie 或服务令牌；否则沿用 P1 的 token 可选校验（查询参数，浏览器 WS 无法自定义 header）。
	if s.accountMode() {
		if s.currentPrincipal(r) == nil {
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
	} else if s.cfg.Token != "" && !s.tokenOK(r) {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}
	ws, err := websocket.Accept(w, r, s.acceptOptsPtr())
	if err != nil {
		slog.Warn("browser ws accept failed", "err", err, "remote", r.RemoteAddr,
			"origin", r.Header.Get("Origin"))
		return
	}
	ws.SetReadLimit(browserReadLm)
	defer ws.CloseNow()

	ctx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
	defer cancel()
	bc := &browserConn{send: make(chan []byte, sendChanSize), cancel: cancel}

	s.mu.Lock()
	s.browsers[bc] = struct{}{}
	snap := s.snapshot()
	s.mu.Unlock()

	go writePump(ctx, ws, bc.send)
	go pingPump(ctx, cancel, ws)

	// 首帧全量快照
	snapData, _ := json.Marshal(snap)
	wctx, wcancel := context.WithTimeout(ctx, wsWriteWait)
	err = ws.Write(wctx, websocket.MessageText, snapData)
	wcancel()
	if err != nil {
		s.mu.Lock()
		delete(s.browsers, bc)
		s.mu.Unlock()
		return
	}

	// 读循环：浏览器无实质上行，读到错误即断开并摘除
	for {
		if _, _, err := ws.Read(ctx); err != nil {
			s.mu.Lock()
			delete(s.browsers, bc)
			s.mu.Unlock()
			return
		}
	}
}
