package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
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

// setupAuthorized 判定首装请求是否放行：真实 TCP loopback 永远放行；
// 非回环必须携带未消费的一次性 setup token（恒时比较）。
func (s *Server) setupAuthorized(r *http.Request) bool {
	if auth.IsLoopbackRemote(r) {
		return true
	}
	if s.cfg.SetupToken == "" || s.setupTokenUsed.Load() {
		return false
	}
	return auth.ConstantTimeEqual(r.Header.Get(setupTokenHeader), s.cfg.SetupToken)
}

// handleAuthSetup 首装引导：仅当用户数为 0（原子判定），创建 default 租户 + 首个 admin。
func (s *Server) handleAuthSetup(w http.ResponseWriter, r *http.Request) {
	if !s.setupAuthorized(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "setup 需要回环来源或一次性 setup token"})
		return
	}
	if s.cfg.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store unavailable"})
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
