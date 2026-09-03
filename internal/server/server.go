// Package server 是 Cloudpath 中心服务：chi REST + WS hub（edge 接入 / 浏览器 fan-out）
// + SQLite 持久化 + 内嵌 webui。
//
// 并发不变量（改代码前先读）：
//  1. s.mu 只保护内存态（devices/edges/browsers/限流器），**锁内绝不做磁盘 I/O**；
//     需要落库时在锁内收集数据、锁外调用 persistXxx。
//  2. broadcast 只读 browsers 集合，慢消费者丢帧不阻塞（历史可从 REST 补）。
//  3. Store 可为 nil（API-only 模式）：所有落库路径必须先判空。
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/device"
	"github.com/DeliciousBuding/cloud-path/internal/store"
	"github.com/DeliciousBuding/cloud-path/webui"
)

const (
	defaultRetentionDays = 30
	defaultCmdRatePerMin = 20
	maxCommandArgsLen    = 64
)

// Config 是服务配置。
type Config struct {
	Store    *store.Store
	Token    string // 共享令牌；空 = 无鉴权（仅限本机/内网）
	Version  string
	WebUIDir string // 开发模式：从磁盘目录服务前端（优先于内嵌）
	// RetentionDays 是事件/命令保留天数，超期由 sweeper 清理。<=0 用默认 30 天。
	RetentionDays int
	// CmdRatePerMin 是单设备每分钟命令下发上限（防跑飞的 UI/脚本刷串口）。<=0 用默认 20。
	CmdRatePerMin int
	// AllowedOrigins 是 WS 握手允许的浏览器 Origin 模式（如 "console.example.com"、
	// "*.example.com:8443"）。留空 = 开发策略：同源 + localhost/127.0.0.1 任意端口。
	// 公网部署必须显式配置（非浏览器客户端不带 Origin，不受影响）。
	AllowedOrigins []string
}

func (c Config) retentionDays() int {
	if c.RetentionDays <= 0 {
		return defaultRetentionDays
	}
	return c.RetentionDays
}

func (c Config) cmdRatePerMin() int {
	if c.CmdRatePerMin <= 0 {
		return defaultCmdRatePerMin
	}
	return c.CmdRatePerMin
}

// Server 持有全部运行态。
type Server struct {
	cfg       Config
	startedAt time.Time

	mu       sync.RWMutex
	devices  map[string]*api.DeviceView // key: "<edge>/<dev>"
	edges    map[string]*edgeLink       // key: edge_id（在线连接）
	browsers map[*browserConn]struct{}
	cmdHits  map[string][]time.Time // 命令限流滑窗：device key → 命中时刻
}

type edgeLink struct {
	edgeID      string
	version     string
	devices     []string
	connectedAt time.Time
	send        chan []byte
	cancel      context.CancelFunc
}

type browserConn struct {
	send   chan []byte
	cancel context.CancelFunc
}

// New 创建服务并从数据库水合上次已知状态（重启后面板不空白，离线标记）。
func New(cfg Config) *Server {
	s := &Server{
		cfg:       cfg,
		startedAt: time.Now(),
		devices:   map[string]*api.DeviceView{},
		edges:     map[string]*edgeLink{},
		browsers:  map[*browserConn]struct{}{},
		cmdHits:   map[string][]time.Time{},
	}
	s.hydrate()
	return s
}

func (s *Server) hydrate() {
	st := s.cfg.Store
	if st == nil {
		return
	}
	rows, err := st.ListDevices()
	if err != nil {
		slog.Warn("hydrate: list devices", "err", err)
		return
	}
	states, err := st.GetStates()
	if err != nil {
		slog.Warn("hydrate: get states", "err", err)
		return
	}
	for _, d := range rows {
		v := &api.DeviceView{
			ID: d.ID, EdgeID: d.EdgeID, Adapter: d.Adapter,
			Name: d.Name, Port: d.Port, Online: false,
			State: map[string]any{}, LastSeen: d.LastSeen,
		}
		if sr, ok := states[d.ID]; ok {
			if err := json.Unmarshal([]byte(sr.State), &v.State); err != nil {
				slog.Warn("hydrate: bad state json", "device", d.ID, "err", err)
				v.State = map[string]any{}
			}
			v.UpdatedAt = sr.UpdatedAt
			v.Online = false // 重启后一律离线，等 edge 重新上报
		}
		s.devices[d.ID] = v
	}
	slog.Info("hydrated devices from store", "count", len(rows))
}

// ---------- 内存态变更（锁内）与落库（锁外） ----------

// applyMeta 在锁内登记设备元信息，返回需要落库的条目（调用方锁外持久化）。
func (s *Server) applyMeta(edgeID string, metas []api.DeviceMeta) []api.DeviceMeta {
	for _, m := range metas {
		key := api.DeviceKey(edgeID, m.ID)
		v, ok := s.devices[key]
		if !ok {
			v = &api.DeviceView{ID: key, State: map[string]any{}}
			s.devices[key] = v
		}
		v.EdgeID = edgeID
		v.Adapter = m.Adapter
		v.Name = m.Name
		v.Port = m.Port
	}
	return metas
}

// persistDevices 落库设备元信息（锁外调用）。
func (s *Server) persistDevices(edgeID string, metas []api.DeviceMeta) {
	if s.cfg.Store == nil {
		return
	}
	for _, m := range metas {
		key := api.DeviceKey(edgeID, m.ID)
		if err := s.cfg.Store.UpsertDevice(key, edgeID, m.Adapter, m.Name, m.Port); err != nil {
			slog.Warn("upsert device", "err", err, "device", key)
		}
	}
}

// statePersist 是一次待落库的状态快照。
type statePersist struct {
	key       string
	stateJSON string
	online    bool
	updatedAt int64
}

// applyState 在锁内更新内存态，返回待落库数据（调用方锁外持久化）。
func (s *Server) applyState(key string, st *api.StateData) statePersist {
	v, ok := s.devices[key]
	if !ok {
		v = &api.DeviceView{ID: key, State: map[string]any{}}
		s.devices[key] = v
	}
	v.Online = st.Online
	if st.Raw == nil {
		st.Raw = map[string]any{}
	}
	v.State = st.Raw
	v.UpdatedAt = st.UpdatedAt
	v.LastSeen = time.Now().Unix()
	b, err := json.Marshal(st.Raw)
	if err != nil {
		slog.Warn("marshal state", "err", err, "device", key)
		b = []byte("{}")
	}
	return statePersist{key: key, stateJSON: string(b), online: st.Online, updatedAt: st.UpdatedAt}
}

func (s *Server) persistStates(items []statePersist) {
	if s.cfg.Store == nil {
		return
	}
	for _, it := range items {
		if err := s.cfg.Store.SetState(it.key, it.stateJSON, it.online, it.updatedAt); err != nil {
			slog.Warn("set state", "err", err, "device", it.key)
		}
	}
}

// markEdgeOffline 把 edge 全部设备标离线：锁内改内存并收集信封 → 锁外落库 + 广播
// （RWMutex 不可重入，且磁盘 I/O 不得占锁）。
func (s *Server) markEdgeOffline(link *edgeLink) {
	s.mu.Lock()
	envs := make([]api.Envelope, 0, len(link.devices))
	pend := make([]statePersist, 0, len(link.devices))
	for _, key := range link.devices {
		if v, ok := s.devices[key]; ok {
			v.Online = false
			b, _ := json.Marshal(v.State)
			pend = append(pend, statePersist{key: key, stateJSON: string(b), online: false, updatedAt: v.UpdatedAt})
			envs = append(envs, s.stateEnvelope(key, v))
		}
	}
	s.mu.Unlock()
	s.persistStates(pend)
	for _, env := range envs {
		s.broadcast(env)
	}
}

func (s *Server) stateEnvelope(key string, v *api.DeviceView) api.Envelope {
	data, _ := json.Marshal(api.StateData{Online: v.Online, Raw: v.State, UpdatedAt: v.UpdatedAt})
	return api.Envelope{V: api.Version, Type: api.MsgState, Device: key, Ts: time.Now().Unix(), Data: data}
}

// ---------- 广播 ----------

// broadcast 向全部浏览器连接 fan-out。慢消费者丢消息不阻塞（历史仍可从 REST 补）。
func (s *Server) broadcast(env api.Envelope) {
	data, err := json.Marshal(env)
	if err != nil {
		slog.Warn("broadcast: marshal", "err", err, "type", env.Type)
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for bc := range s.browsers {
		select {
		case bc.send <- data:
		default:
			slog.Debug("broadcast: slow browser client, dropping frame", "type", env.Type)
		}
	}
}

func (s *Server) snapshot() api.Envelope {
	data, _ := json.Marshal(api.SnapshotData{Devices: s.deviceViews(), Edges: s.edgeViews()})
	return api.Envelope{V: api.Version, Type: api.MsgSnapshot, Ts: time.Now().Unix(), Data: data}
}

// deviceViews 返回设备视图副本（调用方需持锁；State map 为共享引用，只读使用）。
func (s *Server) deviceViews() []api.DeviceView {
	out := make([]api.DeviceView, 0, len(s.devices))
	for _, v := range s.devices {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// edgeViews 返回边缘节点视图：在线连接 + 曾接入过的离线节点（按设备表反推）。
// 离线节点的 ConnectedAt 取其设备的最后上报时间，前端展示为"最后在线"。
func (s *Server) edgeViews() []api.EdgeView {
	known := map[string]*api.EdgeView{}
	for _, v := range s.devices {
		if v.EdgeID == "" {
			continue
		}
		e, ok := known[v.EdgeID]
		if !ok {
			e = &api.EdgeView{EdgeID: v.EdgeID}
			known[v.EdgeID] = e
		}
		e.Devices = append(e.Devices, v.ID)
		if v.LastSeen > e.ConnectedAt {
			e.ConnectedAt = v.LastSeen
		}
	}
	for _, l := range s.edges {
		e, ok := known[l.edgeID]
		if !ok {
			e = &api.EdgeView{EdgeID: l.edgeID}
			known[l.edgeID] = e
		}
		e.Online = true
		e.Version = l.version
		e.Devices = l.devices
		e.ConnectedAt = l.connectedAt.Unix()
	}
	out := make([]api.EdgeView, 0, len(known))
	for _, e := range known {
		sort.Strings(e.Devices)
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EdgeID < out[j].EdgeID })
	return out
}

// ---------- 路由 ----------

// Routes 组装 chi 路由树。
func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)
	r.Use(middleware.Compress(5, "application/json", "text/html", "text/css",
		"application/javascript", "image/svg+xml"))
	r.Use(securityHeaders)
	r.Use(s.logMiddleware)

	r.Get("/healthz", s.handleHealth)
	r.Route("/api", func(r chi.Router) {
		r.Get("/devices", s.handleListDevices)
		r.Get("/devices/{edgeID}/{deviceID}", s.handleGetDevice)
		r.Post("/devices/{edgeID}/{deviceID}/commands", s.authWrite(s.handlePostCommand))
		r.Get("/events", s.handleListEvents)
		r.Get("/edges", s.handleListEdges)
		r.Get("/commands", s.handleListCommands)
		r.Get("/adapters", s.handleListAdapters)
		r.Get("/stats", s.handleStats)
	})
	r.Get("/ws", s.handleBrowserWS)
	r.Get("/ws/edge", s.handleEdgeWS)
	r.Handle("/*", s.spaHandler())
	return r
}

// devOriginPatterns 是未显式配置 AllowedOrigins 时的开发态放行集：
// 本机 Vite dev server（:5173）与同源页面可用，外站跨源仍被拒。
var devOriginPatterns = []string{"localhost:*", "127.0.0.1:*", "[::1]:*"}

// acceptOpts 组装 WS 握手选项。Origin 校验只对带 Origin 头的浏览器生效，
// edge/CLI 客户端（无 Origin）不受影响；请求自身的 host 始终被放行（coder/websocket 语义）。
func (s *Server) acceptOpts() websocket.AcceptOptions {
	if len(s.cfg.AllowedOrigins) > 0 {
		return websocket.AcceptOptions{OriginPatterns: s.cfg.AllowedOrigins}
	}
	return websocket.AcceptOptions{OriginPatterns: devOriginPatterns}
}

// securityHeaders 加最小安全响应头（管理台可能挂在反代后对外）。
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/ws") || r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		t0 := time.Now()
		next.ServeHTTP(ww, r)
		slog.Debug("http", "method", r.Method, "path", r.URL.Path,
			"status", ww.Status(), "dur_ms", time.Since(t0).Milliseconds())
	})
}

// authWrite 在设置 Token 时强制 Bearer 鉴权（写操作）。未设 token = 本机模式。
func (s *Server) authWrite(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.Token != "" && !s.tokenOK(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token"})
			return
		}
		h(w, r)
	}
}

func (s *Server) tokenOK(r *http.Request) bool {
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if got == "" {
		got = r.URL.Query().Get("token") // 浏览器 WS/EventSource 无法自定义 header
	}
	return got == s.cfg.Token
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Debug("write json", "err", err)
	}
}

// queryInt 解析整型查询参数：缺省/非法/负数一律回退 def，超过 max（>0）则夹到 max。
// 语义统一为"非法输入不改变默认行为"，避免 limit=-1 变成无界或零结果查询。
func queryInt(r *http.Request, name string, def, max int) int {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return def
	}
	if max > 0 && n > max {
		return max
	}
	return n
}

func (s *Server) acceptOptsPtr() *websocket.AcceptOptions {
	o := s.acceptOpts()
	return &o
}

// ---------- REST handlers ----------

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	online, total := 0, len(s.devices)
	for _, v := range s.devices {
		if v.Online {
			online++
		}
	}
	edges := len(s.edges)
	s.mu.RUnlock()
	writeJSON(w, http.StatusOK, api.HealthView{
		OK: true, Version: s.cfg.Version,
		UptimeS:       int64(time.Since(s.startedAt).Seconds()),
		DevicesOnline: online, DevicesTotal: total, EdgesOnline: edges,
	})
}

func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	views := s.deviceViews()
	s.mu.RUnlock()
	writeJSON(w, http.StatusOK, map[string]any{"devices": views})
}

func (s *Server) handleGetDevice(w http.ResponseWriter, r *http.Request) {
	key := api.DeviceKey(chi.URLParam(r, "edgeID"), chi.URLParam(r, "deviceID"))
	s.mu.RLock()
	v, ok := s.devices[key]
	var out api.DeviceView
	if ok {
		out = *v
	}
	s.mu.RUnlock()
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "device not found"})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleListEdges(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	views := s.edgeViews()
	s.mu.RUnlock()
	writeJSON(w, http.StatusOK, map[string]any{"edges": views})
}

func (s *Server) handleListEvents(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Store == nil {
		writeJSON(w, http.StatusOK, map[string]any{"events": []api.EventView{}})
		return
	}
	dev := r.URL.Query().Get("device")
	since := int64(queryInt(r, "since", 0, 0))
	limit := queryInt(r, "limit", 100, 1000)
	rows, err := s.cfg.Store.ListEvents(dev, since, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out := make([]api.EventView, 0, len(rows))
	for _, e := range rows {
		out = append(out, api.EventView{ID: e.ID, DeviceID: e.DeviceID, Ts: e.Ts, Type: e.Type, Payload: e.Payload})
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": out})
}

func (s *Server) handleListCommands(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Store == nil {
		writeJSON(w, http.StatusOK, map[string]any{"commands": []api.CommandView{}})
		return
	}
	rows, err := s.cfg.Store.ListCommands(r.URL.Query().Get("device"),
		r.URL.Query().Get("status"), queryInt(r, "limit", 100, 1000))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out := make([]api.CommandView, 0, len(rows))
	for _, c := range rows {
		cv := api.CommandView{ID: c.ID, DeviceID: c.DeviceID, Cmd: c.Cmd, Args: c.Args,
			Status: c.Status, CreatedAt: c.CreatedAt, Result: c.Result}
		if c.AckedAt.Valid {
			cv.AckedAt = c.AckedAt.Int64
		}
		out = append(out, cv)
	}
	writeJSON(w, http.StatusOK, map[string]any{"commands": out})
}

// handleListAdapters 暴露已注册设备适配器与其命令白名单——前端命令面板以此为准，
// 避免前端硬编码命令集（新增适配器零前端改动）。
func (s *Server) handleListAdapters(w http.ResponseWriter, r *http.Request) {
	out := make([]api.AdapterView, 0, 4)
	for _, name := range device.Names() {
		a, ok := device.Get(name)
		if !ok {
			continue
		}
		out = append(out, api.AdapterView{Name: name, Commands: a.SupportedCommands()})
	}
	writeJSON(w, http.StatusOK, map[string]any{"adapters": out})
}

// handleStats 返回存储侧计数与保留期（系统页）。
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	view := api.StatsView{RetentionDays: s.cfg.retentionDays(), AuthEnabled: s.cfg.Token != ""}
	if s.cfg.Store != nil {
		st, err := s.cfg.Store.Stats()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		view.Devices, view.Events, view.Commands = st.Devices, st.Events, st.Commands
		view.OldestEvent, view.SchemaVersion = st.OldestEvent, st.SchemaVer
	}
	writeJSON(w, http.StatusOK, view)
}

// allowCommand 是单设备命令限流（滑动窗口）。锁内计算，无 I/O。
func (s *Server) allowCommand(key string) bool {
	limit := s.cfg.cmdRatePerMin()
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	window := s.cmdHits[key]
	kept := window[:0]
	for _, t := range window {
		if now.Sub(t) < time.Minute {
			kept = append(kept, t)
		}
	}
	if len(kept) >= limit {
		s.cmdHits[key] = kept
		return false
	}
	s.cmdHits[key] = append(kept, now)
	return true
}

// handlePostCommand 下发命令：参数校验 → 白名单 → 限流 → 建库 → WS 推给 edge → 状态 sent。
func (s *Server) handlePostCommand(w http.ResponseWriter, r *http.Request) {
	key := api.DeviceKey(chi.URLParam(r, "edgeID"), chi.URLParam(r, "deviceID"))
	var body struct {
		Cmd  string `json:"cmd"`
		Args string `json:"args"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil || body.Cmd == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "body 需为 {\"cmd\":\"...\",\"args\":\"...\"}"})
		return
	}
	body.Cmd = strings.TrimSpace(body.Cmd)
	if len(body.Args) > maxCommandArgsLen || strings.ContainsAny(body.Args, "\r\n\x00") {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("args 非法：长度需 <=%d 且不含换行/NUL", maxCommandArgsLen)})
		return
	}

	s.mu.RLock()
	v, devOK := s.devices[key]
	var adapter string
	var link *edgeLink
	if devOK {
		adapter = v.Adapter
		link = s.edges[v.EdgeID]
	}
	s.mu.RUnlock()
	if !devOK {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "device not found"})
		return
	}
	if link == nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "edge offline"})
		return
	}
	// 命令白名单：以适配器注册表为准（server 与 edge 共享同一注册表）
	if a, ok := device.Get(adapter); ok {
		allowed := false
		for _, c := range a.SupportedCommands() {
			if c == body.Cmd {
				allowed = true
				break
			}
		}
		if !allowed {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("adapter %q 不支持命令 %q", adapter, body.Cmd)})
			return
		}
	}
	if !s.allowCommand(key) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{
			"error": fmt.Sprintf("命令过于频繁（上限 %d 次/分钟/设备）", s.cfg.cmdRatePerMin())})
		return
	}
	if s.cfg.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store unavailable"})
		return
	}

	id, err := s.cfg.Store.CreateCommand(key, body.Cmd, body.Args)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	data, _ := json.Marshal(api.CommandData{CommandID: id, Cmd: body.Cmd, Args: body.Args})
	env := api.Envelope{V: api.Version, Type: api.MsgCommand, Device: key, Ts: time.Now().Unix(), Data: data}
	payload, _ := json.Marshal(env)
	select {
	case link.send <- payload:
		if err := s.cfg.Store.UpdateCommandStatus(id, "sent", ""); err != nil {
			slog.Warn("mark command sent", "err", err, "cmd_id", id)
		}
		writeJSON(w, http.StatusOK, api.CommandView{ID: id, DeviceID: key, Cmd: body.Cmd, Args: body.Args,
			Status: "sent", CreatedAt: time.Now().Unix()})
	default:
		if err := s.cfg.Store.UpdateCommandStatus(id, "failed", "edge 发送队列满"); err != nil {
			slog.Warn("mark command failed", "err", err, "cmd_id", id)
		}
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "edge busy"})
	}
}

// RunSweeper 后台维护协程：命令超时标记（30s）+ 保留期清理（1h）。ctx 取消即退出。
func (s *Server) RunSweeper(ctx context.Context) {
	timeoutT := time.NewTicker(30 * time.Second)
	retentionT := time.NewTicker(time.Hour)
	defer timeoutT.Stop()
	defer retentionT.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timeoutT.C:
			if s.cfg.Store == nil {
				continue
			}
			n, err := s.cfg.Store.TimeoutStaleCommands(90 * time.Second)
			if err != nil {
				slog.Warn("sweeper: timeout commands", "err", err)
			} else if n > 0 {
				slog.Info("sweeper: commands timed out", "count", n)
			}
		case <-retentionT.C:
			s.pruneOnce()
		}
	}
}

// pruneOnce 执行一次保留期清理（sweeper 与测试共用）。
func (s *Server) pruneOnce() {
	if s.cfg.Store == nil {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -s.cfg.retentionDays()).Unix()
	ev, err := s.cfg.Store.PruneEvents(cutoff)
	if err != nil {
		slog.Warn("retention: prune events", "err", err)
	}
	cmd, err := s.cfg.Store.PruneCommands(cutoff)
	if err != nil {
		slog.Warn("retention: prune commands", "err", err)
	}
	if ev > 0 || cmd > 0 {
		slog.Info("retention: pruned", "events", ev, "commands", cmd, "days", s.cfg.retentionDays())
	}
}

// CloseAll 断开全部 WS 客户端（优雅停机用，避免 Shutdown 等长连接挂死）。
// 用 cancel 而非 close(chan)：与 broadcast 的并发发送零竞态。
func (s *Server) CloseAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, l := range s.edges {
		l.cancel()
	}
	for b := range s.browsers {
		b.cancel()
	}
	s.edges = map[string]*edgeLink{}
	s.browsers = map[*browserConn]struct{}{}
	s.cmdHits = map[string][]time.Time{}
}

// ---------- 内嵌/磁盘前端 ----------

func (s *Server) webFS() fs.FS {
	if s.cfg.WebUIDir != "" {
		return os.DirFS(s.cfg.WebUIDir)
	}
	if sub, err := fs.Sub(webui.Dist, "dist"); err == nil {
		if entries, err := fs.ReadDir(sub, "."); err == nil && len(entries) > 0 {
			return sub
		}
	}
	return nil
}

// spaHandler 服务前端静态资源；未命中文件回落 index.html（SPA 路由）。
func (s *Server) spaHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fsys := s.webFS()
		if fsys == nil {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, "Cloudpath server 运行中（API-only）。前端未构建：task build，或开发模式 -webui webui/dist")
			return
		}
		rel := strings.TrimPrefix(r.URL.Path, "/")
		if rel == "" {
			rel = "index.html"
		}
		// 路径穿越防护：清理后必须仍在 FS 根内
		clean := strings.TrimPrefix(path.Clean("/"+rel), "/")
		if clean == "" || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
			http.NotFound(w, r)
			return
		}
		if f, err := fsys.Open(clean); err == nil {
			f.Close()
			http.FileServerFS(fsys).ServeHTTP(w, r)
			return
		}
		// SPA fallback
		index, err := fs.ReadFile(fsys, "index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if _, err := w.Write(index); err != nil {
			slog.Debug("spa: write index", "err", err)
		}
	})
}
