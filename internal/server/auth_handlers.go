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
	"github.com/DeliciousBuding/cloud-path/internal/auth"
	"github.com/DeliciousBuding/cloud-path/internal/store"
)

// sessionTouchInterval 是会话 last_seen_at 的最小刷新间隔（避免每请求一次写库）。
const sessionTouchInterval = 60 * time.Second

// userView 把 store 查询结果转契约视图（docs/api.md §2.2 {user}）。
func userView(u store.AuthUser) api.UserView {
	return api.UserView{
		ID: u.ID, Username: u.Username, Name: u.Name, Role: u.Role,
		TenantID: u.TenantID, TenantSlug: u.TenantSlug,
	}
}

// handleAuthSetup 首装引导：仅当用户数为 0（原子判定），创建 default 租户 + 首个 admin。
func (s *Server) handleAuthSetup(w http.ResponseWriter, r *http.Request) {
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
		writeJSON(w, http.StatusConflict, map[string]string{"error": "already set up"})
		return
	}
	s.authForced.Store(true) // 首个用户落库后立即进入账号模式（全鉴权）
	writeJSON(w, http.StatusOK, map[string]any{"user": userView(u)})
}

// handleAuthLogin 用户名密码登录：成功创建新会话并 set-cookie；错 401；超限 429。
func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store unavailable"})
		return
	}
	if ok, retry := s.loginLimiter.Allow(auth.ClientIP(r)); !ok {
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
	badCreds := errors.Is(err, sql.ErrNoRows)
	if err == nil {
		badCreds = u.Disabled || !auth.VerifyPassword(u.PasswordHash, body.Password)
	}
	if badCreds {
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
	auth.SetSessionCookie(w, r, sid, int(ttl.Seconds()))
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

// currentPrincipal 解析请求身份：优先服务令牌（等价 admin），其次会话 cookie。
// 全部路径无磁盘 I/O 依赖；会话查询失败一律视为未认证（fail-closed）。
func (s *Server) currentPrincipal(r *http.Request) *auth.Principal {
	if s.cfg.Token != "" && auth.TokenOK(r, s.cfg.Token) {
		p := &auth.Principal{
			Username: "token", Name: "服务令牌", Role: string(api.RoleAdmin),
			Token: true, TenantSlug: "default",
		}
		if s.cfg.Store != nil {
			if t, err := s.cfg.Store.GetTenantBySlug("default"); err == nil {
				p.TenantID, p.TenantSlug = t.ID, t.Slug
			}
		}
		return p
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
