package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/audit"
	"github.com/DeliciousBuding/cloud-path/internal/auth"
	"github.com/DeliciousBuding/cloud-path/internal/store"
)

// sessionTouchInterval 是会话 last_seen_at 的最小刷新间隔（避免每请求一次写库）。
const sessionTouchInterval = 60 * time.Second

// setupTokenHeader 是一次性首装令牌的请求头名（非回环来源 setup 必带）。
const setupTokenHeader = "X-Cloudpath-Setup-Token"

// userView 把 store 查询结果转契约视图（docs/api.md §2.2 {user}）。
func userView(u store.AuthUser) api.UserView {
	return api.UserView{
		ID: u.ID, Username: u.Username, Name: u.Name, Role: u.Role,
		TenantID: u.TenantID, TenantSlug: u.TenantSlug, Disabled: u.Disabled,
	}
}

// forwardedHeaderNames 是「请求经过了某个代理」的 IP 转发头信号。
var forwardedHeaderNames = []string{"X-Forwarded-For", "X-Real-IP", "Forwarded"}

// setupRejectAuditInterval 是被拒首装尝试的审计节流窗口（秒）：公网探测不得打爆
// 审计表（与 tenant-security-policy 的 quota 审计节流同理）。被节流只影响重复审计，
// 不影响每次请求的实际拒绝。
const setupRejectAuditInterval = int64(60)

// setupAuthorized 判定首装请求是否放行：本机真实客户端直连放行；
// 其余来源必须携带未消费的一次性 setup token（恒时比较）。
func (s *Server) setupAuthorized(r *http.Request) bool {
	if s.setupFromLocalClient(r) {
		return true
	}
	if s.cfg.SetupToken == "" || s.setupTokenUsed.Load() {
		return false
	}
	return auth.ConstantTimeEqual(r.Header.Get(setupTokenHeader), s.cfg.SetupToken)
}

// setupFromLocalClient 判定首装请求是否来自**本机真实客户端**（D4 修复）。
//
// 旧判据用 TCP 对端地址（auth.IsLoopbackRemote），在同机反代形态下是致命漏洞：
// 生产是 nginx 与 server 同机（proxy_pass http://127.0.0.1:<port>），任何公网访客
// 到达本进程时对端都是 127.0.0.1，于是「回环放行」等于向整个公网开放首装，
// 已完成初始化的实例面临被重置/被抢注管理员的风险。
//
// 现在的判据 fail-closed：
//   - 请求携带任何 IP 转发头 → 说明经过了代理，真实客户端无法确证
//     （代理可能如实追加，也可能把客户端伪造的 XFF 原样透传），一律不按本机放行，
//     只接受未消费的一次性 setup token；
//   - 无转发头时用 trusted-proxy 感知的 auth.ClientIP 解析真实客户端 IP，
//     必须是标准回环地址；解析不出 IP 一律视为非本机。
func (s *Server) setupFromLocalClient(r *http.Request) bool {
	for _, h := range forwardedHeaderNames {
		if strings.TrimSpace(r.Header.Get(h)) != "" {
			return false
		}
	}
	return isLoopbackIPString(auth.ClientIP(r, s.trustedProxies))
}

// isLoopbackIPString 只认标准回环地址；空串/非 IP/其它网段一律 false。
func isLoopbackIPString(raw string) bool {
	ip := net.ParseIP(strings.TrimSpace(raw))
	return ip != nil && ip.IsLoopback()
}

// auditSetupRejected 记录被拒的首装尝试（节流后仍保留首条，绝不静默丢弃）。
func (s *Server) auditSetupRejected(r *http.Request, reason string) {
	now := time.Now().Unix()
	last := s.setupRejectAuditAt.Load()
	if now-last < setupRejectAuditInterval {
		return
	}
	if !s.setupRejectAuditAt.CompareAndSwap(last, now) {
		return
	}
	s.audit(r, audit.Event{
		TenantID: s.auditTenantID(nil), ActorType: audit.ActorSystem, ActorName: "setup",
		Action: audit.ActionSetup, TargetType: audit.TargetTenant, TargetID: defaultTenantSlug,
		Outcome:  audit.OutcomeFailure,
		Metadata: audit.NewMetadata().String("reason", reason).Map(),
	})
}

// handleAuthSetup 首装引导：仅当用户数为 0（原子判定），创建 default 租户 + 首个 admin。
func (s *Server) handleAuthSetup(w http.ResponseWriter, r *http.Request) {
	if !s.setupAuthorized(r) {
		s.auditSetupRejected(r, "not_local_client")
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "setup 需要本机直连来源或一次性 setup token"})
		return
	}
	if s.cfg.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store unavailable"})
		return
	}
	// 已初始化 → 恒定拒绝（幂等，与来源无关）：本机来源也不能重置或抢注管理员。
	// 放在鉴权之后，未授权来源只看到 403，连「是否已初始化」都探测不到。
	users, err := s.cfg.Store.CountUsers()
	if err != nil {
		slog.Warn("setup: count users", "err", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store unavailable"})
		return
	}
	if users > 0 {
		s.audit(r, audit.Event{
			TenantID: s.auditTenantID(nil), ActorType: audit.ActorSystem, ActorName: "setup",
			Action: audit.ActionSetup, TargetType: audit.TargetTenant, TargetID: defaultTenantSlug,
			Outcome:  audit.OutcomeFailure,
			Metadata: audit.NewMetadata().String("reason", "already_initialized").Map(),
		})
		writeJSON(w, http.StatusConflict, map[string]string{"error": "already set up"})
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Name     string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": `body 需为 {"username":"...","password":"..."}`})
		return
	}
	username := strings.TrimSpace(body.Username)
	if username == "" || body.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "username 与 password 必填"})
		return
	}
	if len(username) > 64 || len(body.Password) > 256 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "username <=64 / password <=256"})
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = username
	}
	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "hash password"})
		return
	}
	u, created, err := s.cfg.Store.CreateInitialAdmin(username, name, hash)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if !created {
		s.audit(r, audit.Event{
			TenantID: s.auditTenantID(nil), ActorType: audit.ActorSystem, ActorName: "system",
			Action: audit.ActionSetup, TargetType: audit.TargetTenant, TargetID: defaultTenantSlug,
			Outcome: audit.OutcomeFailure,
		})
		writeJSON(w, http.StatusConflict, map[string]string{"error": "already set up"})
		return
	}
	s.authForced.Store(true)     // 首个用户落库后立即进入账号模式（全鉴权）
	s.setupTokenUsed.Store(true) // 一次性 setup token 已消费，后续复用失效
	s.audit(r, audit.Event{
		TenantID: u.TenantID, ActorType: audit.ActorSystem, ActorName: "system",
		Action: audit.ActionSetup, TargetType: audit.TargetTenant, TargetID: u.TenantSlug,
		Outcome: audit.OutcomeSuccess,
	})
	writeJSON(w, http.StatusOK, map[string]any{"user": userView(u)})
}

// handleAuthLogin 用户名密码登录：成功创建新会话并 set-cookie；错 401；超限 429。
func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store unavailable"})
		return
	}
	if ok, retry := s.loginLimiter.Allow(auth.ClientIP(r, s.trustedProxies)); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "登录尝试过多，请稍后再试"})
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": `body 需为 {"username":"...","password":"..."}`})
		return
	}
	username := strings.TrimSpace(body.Username)
	u, err := s.cfg.Store.GetUserByUsername(username)
	known := err == nil
	badCreds := false
	if known {
		ok := s.verifyPassword(u.PasswordHash, body.Password)
		badCreds = u.Disabled || !ok
	} else if errors.Is(err, sql.ErrNoRows) {
		// 未知用户同样执行一次固定 Argon2id 派生，收敛用户名枚举时序。
		s.dummyVerify(body.Password)
		badCreds = true
	}
	if badCreds {
		tenantID := s.auditTenantID(nil)
		actorID := int64(0)
		targetSlug := defaultTenantSlug
		if known {
			tenantID = u.TenantID
			actorID = u.ID
			targetSlug = u.TenantSlug
		}
		s.audit(r, audit.Event{
			TenantID: tenantID, ActorType: audit.ActorUser, ActorID: actorID, ActorName: username,
			Action: audit.ActionLogin, TargetType: audit.TargetTenant, TargetID: targetSlug,
			Outcome: audit.OutcomeFailure,
		})
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "用户名或密码错误"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	sid, err := auth.NewSessionID()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create session"})
		return
	}
	ttl := s.cfg.sessionTTL()
	if err := s.cfg.Store.CreateSession(sid, u.ID, time.Now().Add(ttl).Unix()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	auth.SetSessionCookie(w, r, sid, int(ttl.Seconds()), s.trustedProxies)
	s.audit(r, audit.Event{
		TenantID: u.TenantID, ActorType: audit.ActorUser, ActorID: u.ID, ActorName: u.Username,
		Action: audit.ActionLogin, TargetType: audit.TargetTenant, TargetID: u.TenantSlug,
		Outcome: audit.OutcomeSuccess,
	})
	writeJSON(w, http.StatusOK, map[string]any{"user": userView(u)})
}

// handleAuthLogout 登出：删服务端会话并清 cookie；无会话 → 401。
func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(auth.SessionCookieName)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	if s.cfg.Store != nil {
		if u, uerr := s.cfg.Store.UserBySession(cookie.Value, time.Now().Unix()); uerr == nil {
			s.audit(r, audit.Event{
				TenantID: u.TenantID, ActorType: audit.ActorUser, ActorID: u.ID, ActorName: u.Username,
				Action: audit.ActionLogout, TargetType: audit.TargetTenant, TargetID: u.TenantSlug,
				Outcome: audit.OutcomeSuccess,
			})
		}
		if err := s.cfg.Store.DeleteSession(cookie.Value); err != nil {
			slog.Warn("logout: delete session", "err", err)
		}
	}
	auth.ClearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// handleAuthMe 返回当前身份：会话或服务令牌；无 → 401。
func (s *Server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	p := s.currentPrincipal(r)
	if p == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": api.UserView{
		ID: p.UserID, Username: p.Username, Name: p.Name, Role: p.Role,
		TenantID: p.TenantID, TenantSlug: p.TenantSlug,
	}})
}

// currentPrincipal 解析请求身份：优先 legacy 服务令牌（等价 default admin），
// 其次租户服务令牌（仅 Bearer header），最后会话 cookie。
// 会话查询/令牌查询失败一律视为未认证（fail-closed）。
func (s *Server) currentPrincipal(r *http.Request) *auth.Principal {
	if s.cfg.Token != "" && auth.TokenOK(r, s.cfg.Token) {
		p := &auth.Principal{
			Username: "token", Name: "服务令牌", Role: string(api.RoleAdmin),
			Token: true, Legacy: true, TenantSlug: "default",
		}
		if s.cfg.Store != nil {
			if t, err := s.cfg.Store.GetTenantBySlug("default"); err == nil {
				p.TenantID, p.TenantSlug = t.ID, t.Slug
			}
		}
		return p
	}
	if bearer := auth.BearerToken(r); auth.IsTenantToken(bearer) {
		// 租户令牌只走 header；解析失败即未认证（不回落 cookie）。
		if p := s.tenantTokenPrincipal(bearer); p != nil {
			return p
		}
		return nil
	}
	cookie, err := r.Cookie(auth.SessionCookieName)
	if err != nil || s.cfg.Store == nil {
		return nil
	}
	now := time.Now().Unix()
	u, err := s.cfg.Store.UserBySession(cookie.Value, now)
	if err != nil {
		return nil
	}
	p := &auth.Principal{
		UserID: u.ID, Username: u.Username, Name: u.Name, Role: u.Role,
		TenantID: u.TenantID, TenantSlug: u.TenantSlug,
	}
	// 节流刷新 last_seen_at（会话存在即算活跃，但不为每请求写库）
	if u.SessionLastSeen == 0 || now-u.SessionLastSeen > int64(sessionTouchInterval.Seconds()) {
		if err := s.cfg.Store.TouchSession(cookie.Value, now); err != nil {
			slog.Debug("touch session", "err", err)
		}
	}
	return p
}
