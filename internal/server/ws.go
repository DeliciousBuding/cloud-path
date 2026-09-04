package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/audit"
	"github.com/DeliciousBuding/cloud-path/internal/auth"
	"github.com/DeliciousBuding/cloud-path/internal/model"
	"github.com/DeliciousBuding/cloud-path/internal/store"
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
		s.audit(r, audit.Event{
			TenantID: s.auditTenantID(nil), ActorType: audit.ActorEdge, ActorName: hello.EdgeID,
			Action: audit.ActionEdgeAuthFailure, TargetType: audit.TargetEdge, TargetID: hello.EdgeID,
			Outcome:  audit.OutcomeFailure,
			Metadata: audit.NewMetadata().String("reason", "invalid_edge_id").Map(),
		})
		return
	}
	// 1.5) 鉴权：legacy -token（default 租户）或租户服务令牌（要求 edge scope，租户以令牌为准）。
	tenant := hello.Tenant
	if tenant == "" {
		tenant = defaultTenantSlug
	}
	defaultTid := s.auditTenantID(nil)
	edgeAuthFail := func(reason string, tenantID int64) {
		s.audit(r, audit.Event{
			TenantID: tenantID, ActorType: audit.ActorEdge, ActorName: hello.EdgeID,
			Action: audit.ActionEdgeAuthFailure, TargetType: audit.TargetEdge, TargetID: hello.EdgeID,
			Outcome:  audit.OutcomeFailure,
			Metadata: audit.NewMetadata().String("reason", reason).Map(),
		})
	}
	var tid int64
	if auth.IsTenantToken(hello.Token) {
		row, ok := s.validTenantToken(hello.Token, true)
		if !ok {
			failTid := defaultTid
			if row.TenantID > 0 {
				failTid = row.TenantID
			}
			edgeAuthFail("invalid_token", failTid)
			ws.Close(websocket.StatusPolicyViolation, "invalid token")
			slog.Warn("edge auth failed", "edge", hello.EdgeID, "remote", r.RemoteAddr)
			return
		}
		tid = row.TenantID
		if t, err := s.cfg.Store.GetTenantByID(row.TenantID); err == nil {
			tenant = t.Slug
		}
	} else if s.cfg.Token != "" {
		if !auth.ConstantTimeEqual(hello.Token, s.cfg.Token) {
			edgeAuthFail("invalid_token", defaultTid)
			ws.Close(websocket.StatusPolicyViolation, "invalid token")
			slog.Warn("edge auth failed", "edge", hello.EdgeID, "remote", r.RemoteAddr)
			return
		}
		tenant = defaultTenantSlug // legacy -token 绑定 default 租户
	} else if s.accountMode() {
		// 账号模式（已有 user / -require-auth）且未配 legacy token：edge 必须出示租户令牌。
		edgeAuthFail("invalid_token", defaultTid)
		ws.Close(websocket.StatusPolicyViolation, "invalid token")
		slog.Warn("edge auth failed", "edge", hello.EdgeID, "remote", r.RemoteAddr)
		return
	} else {
		// 无凭据开发态：自报 tenant 不被信任，恒绑定 default（单租户语义，不越权）。
		tenant = defaultTenantSlug
	}
	if tid == 0 {
		t, terr := s.tenantIDForSlug(tenant)
		if terr != nil {
			edgeAuthFail("unknown_tenant", defaultTid)
			ws.Close(websocket.StatusPolicyViolation, "unknown tenant")
			slog.Warn("edge rejected: unknown tenant", "edge", hello.EdgeID, "tenant", tenant, "err", terr)
			return
		}
		tid = t
	}
	s.audit(r, audit.Event{
		TenantID: tid, ActorType: audit.ActorEdge, ActorName: hello.EdgeID,
		Action: audit.ActionEdgeAuthSuccess, TargetType: audit.TargetTenant, TargetID: tenant,
		Outcome: audit.OutcomeSuccess,
	})

	// 2) 注册连接（同租户同 edge_id 重连挤旧连接；跨租户同名 fail-closed，不驱逐）
	ctx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
	defer cancel()
	link := &edgeLink{
		edgeID: hello.EdgeID, version: hello.Version, tenant: tenant, tenantID: tid,
		connectedAt: time.Now(), send: make(chan []byte, sendChanSize),
		cancel: cancel,
	}
	var deviceKeys []string
	for _, d := range hello.Devices {
		key := api.DeviceKey(hello.EdgeID, d.ID)
		deviceKeys = append(deviceKeys, key)
		link.devices = append(link.devices, key)
	}

	// 2a) 内存绑定预检：跨租户同名 edge/device 直接拒绝（不踢线、不落库、不广播）。
	s.mu.Lock()
	if old, ok := s.edges[hello.EdgeID]; ok && old != nil && old.tenantID != tid {
		s.mu.Unlock()
		edgeAuthFail("edge_id_owned_by_other_tenant", tid)
		ws.Close(websocket.StatusPolicyViolation, "edge_id owned by another tenant")
		slog.Warn("edge rejected: cross-tenant edge_id collision", "edge", hello.EdgeID,
			"tenant", tenant, "remote", r.RemoteAddr)
		return
	}
	if conflict := s.identityConflictLocked(hello.EdgeID, tenant, deviceKeys); conflict != "" {
		s.mu.Unlock()
		edgeAuthFail("identity_owned_by_other_tenant", tid)
		ws.Close(websocket.StatusPolicyViolation, "identity owned by another tenant")
		slog.Warn("edge rejected: cross-tenant identity collision", "edge", hello.EdgeID,
			"conflict", conflict, "tenant", tenant, "remote", r.RemoteAddr)
		return
	}
	s.mu.Unlock()

	// 2b) 事务内 fail-closed 落库（锁外）。写入失败即拒绝，不注册连接、不广播。
	if err := s.persistDevices(hello.EdgeID, hello.Devices, tid); err != nil {
		reason := "identity_binding_conflict"
		if errors.Is(err, store.ErrEdgeTenantMismatch) {
			reason = "edge_id_owned_by_other_tenant"
		} else if errors.Is(err, store.ErrDeviceTenantMismatch) {
			reason = "device_id_owned_by_other_tenant"
		}
		edgeAuthFail(reason, tid)
		ws.Close(websocket.StatusPolicyViolation, "identity owned by another tenant")
		slog.Warn("edge rejected: identity persist conflict", "edge", hello.EdgeID,
			"tenant", tenant, "err", err)
		return
	}

	// 2c) 注册连接：同租户重连挤旧；2a/2b 之后出现的跨租户竞态仍 fail-closed。
	s.mu.Lock()
	if old, ok := s.edges[hello.EdgeID]; ok && old != nil {
		if old.tenantID != tid {
			s.mu.Unlock()
			edgeAuthFail("edge_id_owned_by_other_tenant", tid)
			ws.Close(websocket.StatusPolicyViolation, "edge_id owned by another tenant")
			return
		}
		if old != link {
			slog.Info("edge reconnected, evicting old link", "edge", hello.EdgeID, "tenant", tenant)
			old.cancel()
		}
	}
	s.edges[hello.EdgeID] = link
	s.applyMeta(hello.EdgeID, tenant, hello.Devices)
	onlinePend, onlineEnvs := s.markLinkDevicesOnlineLocked(link)
	s.mu.Unlock()
	slog.Info("edge connected", "edge", hello.EdgeID, "devices", link.devices, "version", hello.Version)

	// 2c') edge 上线即恢复其声明设备的在线态（markEdgeOffline 的镜像：连接本身就是
	// 设备可达性的真实事实）。落库在锁外，随后 Edge 的 state 上报会立即按设备真实
	// 情况校正（含 online=false），因此这里不会长期掩盖任何离线事实。
	s.persistStates(onlinePend)
	for _, env := range onlineEnvs {
		s.broadcast(env)
	}

	edgeData, _ := json.Marshal(api.EdgeUpData{EdgeID: hello.EdgeID, Devices: link.devices, Version: hello.Version})
	s.broadcastAs(api.Envelope{V: api.Version, Type: api.MsgEdgeUp, Device: hello.EdgeID, Ts: time.Now().Unix(), Data: edgeData}, link.tenant)

	// 3) 断线清理：仅当仍是注册且同租户的连接时才摘除并标离线。
	defer func() {
		s.mu.Lock()
		current := s.edges[hello.EdgeID] == link && link.tenantID == tid
		if current {
			delete(s.edges, hello.EdgeID)
			// Capability 文档随连接生命周期存在：离线 Edge 的插件不再可查，
			// 留在 catalog 里会让 UI 显示当前无法下发的能力。
			delete(s.edgeCapabilities, hello.EdgeID)
		}
		s.mu.Unlock()
		if current { // 只有仍是注册连接时才标离线（被重连挤掉的旧连接不清新状态）
			s.markEdgeOffline(link)
		}
		s.broadcastAs(api.Envelope{V: api.Version, Type: api.MsgEdgeDown, Device: hello.EdgeID, Ts: time.Now().Unix(), Data: edgeData}, link.tenant)
		slog.Info("edge disconnected", "edge", hello.EdgeID, "was_current", current, "tenant", tenant)
	}()

	go writePump(ctx, ws, link.send)
	go pingPump(ctx, cancel, ws)

	// 3.5) hello 成功后下发当前完整插件期望态快照（control-plane-sync §4.2）。
	// 只发「当前完整快照」，因此 Edge 离线期间的多次 desired 变更在这里一次性收敛，
	// 不回放中间副作用；PluginStore 未接线时按旧协议不下发。
	s.sendPluginDesiredToLink(link)

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
				id, err := s.cfg.Store.AddEventTenant(tid, msg.Device, ev.Type, string(payload), msg.Ts)
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
				ok, err := s.cfg.Store.UpdateCommandStatusScoped(ack.CommandID, msg.Device, tid, ack.Status, ack.Detail)
				if err != nil {
					slog.Warn("store ack", "err", err, "cmd_id", ack.CommandID)
				} else if !ok {
					slog.Warn("foreign command ack ignored", "device", msg.Device, "cmd_id", ack.CommandID, "tenant", tenant)
				}
			}
			slog.Info("command ack", "device", msg.Device, "cmd_id", ack.CommandID, "status", ack.Status)
			s.broadcast(msg)
		case api.MsgDescriptor:
			var desc model.Descriptor
			if err := json.Unmarshal(msg.Data, &desc); err != nil {
				slog.Warn("edge bad descriptor payload", "edge", hello.EdgeID, "err", err)
				continue
			}
			if err := desc.Validate(); err != nil {
				slog.Warn("edge invalid descriptor", "edge", hello.EdgeID, "device", msg.Device, "err", err)
				continue
			}
			s.mu.Lock()
			s.storeDescriptor(msg.Device, desc)
			s.mu.Unlock()
			data, _ := json.Marshal(desc)
			s.broadcast(api.Envelope{V: api.Version, Type: api.MsgDescriptor, Device: msg.Device,
				Ts: time.Now().Unix(), Data: data})
		case api.MsgCapabilities:
			s.handleCapabilitiesMsg(hello.EdgeID, &msg)
		case api.MsgPluginStatus:
			// 身份只取自已鉴权 link（tenant/edge），payload 不自报身份。
			s.handlePluginStatusMsg(link, &msg)
		case api.MsgPluginAck:
			s.handlePluginAckMsg(link, &msg)
		case api.MsgPong:
			// 库层已处理，忽略
		default:
			// 未知消息类型必须忽略并记 debug，绝不因此断开连接（向后兼容，§4）。
			slog.Debug("edge unhandled msg type", "type", msg.Type)
		}
	}
}

// Capability 文档上报的规模上限：单条 WS 消息已被 wsReadLimit 限制，这里再按语义
// 限量，避免一个插件把 catalog 撑爆（正常 Driver 只有十几个 Capability）。
const (
	maxCapabilitySources     = 64
	maxCapabilitiesPerSource = 256
)

// handleCapabilitiesMsg 接收 Edge 上报的 Capability 文档全量快照（覆盖式）。
//
// 设备无关：只按 capability.schema.json 校验并存储，不解释任何硬件语义；非法文档
// 单条跳过并记 warn，不让一个坏插件拖垮整批。不向浏览器广播——前端消费路径是
// GET /api/capabilities（与 /api/descriptors 随行字段），保持单一事实源。
func (s *Server) handleCapabilitiesMsg(edgeID string, msg *api.Envelope) {
	var data api.CapabilitiesData
	if err := json.Unmarshal(msg.Data, &data); err != nil {
		slog.Warn("edge bad capabilities payload", "edge", edgeID, "err", err)
		return
	}
	if len(data.Sources) > maxCapabilitySources {
		slog.Warn("edge capabilities payload rejected: too many sources",
			"edge", edgeID, "sources", len(data.Sources), "limit", maxCapabilitySources)
		return
	}
	// 规模/形状超限一律 **整批拒绝并保留旧文档**（fail-closed）：这是插件实现 bug，
	// 静默跳过会让一次坏上报把之前正确的 catalog 擦空，比拒绝更难排障。
	// 单条文档的契约校验失败则只跳过该条（数据质量问题，不影响同批其他能力）。
	for _, src := range data.Sources {
		if strings.TrimSpace(src.Source) == "" || len(src.Capabilities) > maxCapabilitiesPerSource {
			slog.Warn("edge capabilities payload rejected: bad source shape",
				"edge", edgeID, "source", src.Source, "capabilities", len(src.Capabilities),
				"limit", maxCapabilitiesPerSource)
			return
		}
	}

	kept := make([]api.CapabilitySource, 0, len(data.Sources))
	total := 0
	for _, src := range data.Sources {
		name := strings.TrimSpace(src.Source)
		docs := make([]model.Capability, 0, len(src.Capabilities))
		for _, c := range src.Capabilities {
			if err := c.Validate(); err != nil {
				slog.Warn("edge capability doc invalid, skipped", "edge", edgeID,
					"source", name, "capability", c.Metadata.ID, "err", err)
				continue
			}
			docs = append(docs, c)
		}
		if len(docs) == 0 {
			continue
		}
		kept = append(kept, api.CapabilitySource{Source: name, Capabilities: docs})
		total += len(docs)
	}

	s.mu.Lock()
	if len(kept) == 0 {
		delete(s.edgeCapabilities, edgeID)
	} else {
		s.edgeCapabilities[edgeID] = kept
	}
	s.mu.Unlock()
	slog.Info("edge capabilities received", "edge", edgeID, "sources", len(kept), "capabilities", total)
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
// 连接时解析身份租户并记录到 browserConn（账号模式=会话/令牌租户；L1 令牌=default；
// 无账号开发模式=空，全局接收），后续 snapshot/fan-out 都按该租户过滤。
func (s *Server) handleBrowserWS(w http.ResponseWriter, r *http.Request) {
	var tenant string
	if s.accountMode() {
		p := s.currentPrincipal(r)
		if p == nil {
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		tenant = p.TenantSlug
	} else if s.cfg.Token != "" {
		if !s.tokenOK(r) {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}
		tenant = defaultTenantSlug
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
	bc := &browserConn{send: make(chan []byte, sendChanSize), cancel: cancel, tenant: tenant}

	s.mu.Lock()
	s.browsers[bc] = struct{}{}
	snap := s.snapshotFor(tenant)
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
