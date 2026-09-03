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

	"github.com/go-chi/chi/v5"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/auth"
	"github.com/DeliciousBuding/cloud-path/internal/store"
)

var tokenScopes = map[string]bool{"read": true, "write": true, "admin": true, "edge": true}

// requireRole 是 RBAC 角色门禁（docs/api.md §3.1）：admin>operator>viewer 层级。
// 非账号模式（无 principal）放行，保持单租户开发语义；角色不足 403。
func (s *Server) requireRole(need api.UserRole) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if p := auth.FromContext(r.Context()); p != nil && !auth.RoleAllows(p.Role, string(need)) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "permission denied"})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// requireAdmin 是用户/令牌管理门禁：始终解析身份（不依赖账号模式），
// 未认证 401、角色不足 403，并把 principal 注入 context（legacy token 亦可用）。
func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := s.currentPrincipal(r)
		if p == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
			return
		}
		if !auth.RoleAllows(p.Role, string(api.RoleAdmin)) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "permission denied"})
			return
		}
		next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), p)))
	})
}

// validTenantToken 校验租户服务令牌：存在、未吊销、未过期，可选要求 edge scope。
// 命中时按 60s 节流刷新 last_used_at。返回的 TokenRow 只含元数据（无明文）。
func (s *Server) validTenantToken(token string, needEdge bool) (store.TokenRow, bool) {
	if s.cfg.Store == nil {
		return store.TokenRow{}, false
	}
	row, err := s.cfg.Store.GetTenantTokenByHash(auth.HashToken(token))
	if err != nil {
		return store.TokenRow{}, false
	}
	if row.RevokedAt.Valid {
		return row, false
	}
	now := time.Now().Unix()
	if row.ExpiresAt.Valid && row.ExpiresAt.Int64 <= now {
		return row, false
	}
	var scopes []string
	if err := json.Unmarshal([]byte(row.Scopes), &scopes); err != nil {
		return row, false
	}
	if needEdge && !auth.HasScope(scopes, "edge") {
		return row, false
	}
	if !row.LastUsedAt.Valid || now-row.LastUsedAt.Int64 >= int64(sessionTouchInterval.Seconds()) {
		if err := s.cfg.Store.TouchTenantToken(row.ID, now); err != nil {
			slog.Debug("touch token", "err", err)
		}
	}
	row.Scopes = jsonArray(scopes)
	return row, true
}

// tenantTokenPrincipal 把租户令牌解析为请求身份；令牌无效返回 nil（fail-closed）。
func (s *Server) tenantTokenPrincipal(token string) *auth.Principal {
	row, ok := s.validTenantToken(token, false)
	if !ok {
		return nil
	}
	var scopes []string
	_ = json.Unmarshal([]byte(row.Scopes), &scopes)
	slug := defaultTenantSlug
	if t, err := s.cfg.Store.GetTenantByID(row.TenantID); err == nil {
		slug = t.Slug
	}
	name := row.Name
	if name == "" {
		name = "token"
	}
	return &auth.Principal{
		Username: name, Name: name, Role: auth.ScopeRole(scopes),
		TenantID: row.TenantID, TenantSlug: slug,
		Token: true, Scopes: scopes,
	}
}

func jsonArray(scopes []string) string {
	b, _ := json.Marshal(scopes)
	return string(b)
}

// tokenView 把 store.TokenRow 转契约视图（不含明文）。
func tokenView(row store.TokenRow) api.TokenView {
	var exp, last, rev *int64
	if row.ExpiresAt.Valid {
		v := row.ExpiresAt.Int64
		exp = &v
	}
	if row.LastUsedAt.Valid {
		v := row.LastUsedAt.Int64
		last = &v
	}
	if row.RevokedAt.Valid {
		v := row.RevokedAt.Int64
		rev = &v
	}
	var scopes []string
	_ = json.Unmarshal([]byte(row.Scopes), &scopes)
	return api.TokenView{
		ID: row.ID, Name: row.Name, Prefix: row.Prefix, Scopes: scopes,
		CreatedAt: row.CreatedAt, ExpiresAt: exp, LastUsedAt: last, RevokedAt: rev,
	}
}

// ---------- 用户管理（docs/api.md §3.2） ----------

// handleListUsers GET /api/users：本租户用户列表，永不返回 password hash。
func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	if s.cfg.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store unavailable"})
		return
	}
	users, err := s.cfg.Store.ListUsersTenant(p.TenantID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out := make([]api.UserView, 0, len(users))
	for _, u := range users {
		out = append(out, userView(u))
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out})
}

// handleCreateUser POST /api/users：{username,name,role,password}；username 租户内唯一。
func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	if s.cfg.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store unavailable"})
		return
	}
	var body struct {
		Username string `json:"username"`
		Name     string `json:"name"`
		Role     string `json:"role"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": `body 需为 {"username":"...","password":"...","role":"..."}`})
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
	role := strings.TrimSpace(body.Role)
	if role == "" {
		role = string(api.RoleOperator)
	}
	if role != string(api.RoleAdmin) && role != string(api.RoleOperator) && role != string(api.RoleViewer) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "role 必须为 admin|operator|viewer"})
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
	u, err := s.cfg.Store.CreateUser(p.TenantID, username, name, role, hash)
	if errors.Is(err, store.ErrUsernameTaken) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "username 已存在"})
		return
	}
	if errors.Is(err, store.ErrInvalidRole) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "role 必须为 admin|operator|viewer"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": userView(u)})
}

// handleUpdateUser PATCH /api/users/{id}：修改 name/role/disabled，可选重置 password。
func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	if s.cfg.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store unavailable"})
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}
	var body struct {
		Name     *string `json:"name"`
		Role     *string `json:"role"`
		Disabled *bool   `json:"disabled"`
		Password *string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": `body 需为 {"name":"...","role":"...","disabled":false,"password":"..."}`})
		return
	}
	patch := store.UserPatch{Name: body.Name, Role: body.Role, Disabled: body.Disabled}
	if body.Password != nil {
		if *body.Password == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password 不能为空"})
			return
		}
		hash, err := auth.HashPassword(*body.Password)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "hash password"})
			return
		}
		patch.Password = &hash
	}
	if body.Name != nil {
		if strings.TrimSpace(*body.Name) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name 不能为空"})
			return
		}
		trimmed := strings.TrimSpace(*body.Name)
		patch.Name = &trimmed
	}
	if body.Role != nil {
		role := strings.TrimSpace(*body.Role)
		if role != string(api.RoleAdmin) && role != string(api.RoleOperator) && role != string(api.RoleViewer) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "role 必须为 admin|operator|viewer"})
			return
		}
		patch.Role = &role
	}

	u, err := s.cfg.Store.UpdateUser(p.TenantID, id, patch)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}
	if errors.Is(err, store.ErrLastAdmin) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "不能禁用或降级最后一个可用 admin"})
		return
	}
	if errors.Is(err, store.ErrInvalidRole) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "role 必须为 admin|operator|viewer"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": userView(u)})
}

// ---------- 租户服务令牌（docs/api.md §3.3） ----------

// handleListTokens GET /api/tokens：列出本租户令牌元数据（无明文）。
func (s *Server) handleListTokens(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	if s.cfg.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store unavailable"})
		return
	}
	rows, err := s.cfg.Store.ListTenantTokens(p.TenantID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out := make([]api.TokenView, 0, len(rows))
	for _, row := range rows {
		out = append(out, tokenView(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": out})
}

// handleCreateToken POST /api/tokens：{name,scopes,expires_at?} → {token,...metadata}。
// 明文只在本次响应返回一次；数据库只存 hash/prefix。
func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	if s.cfg.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store unavailable"})
		return
	}
	var body struct {
		Name      string   `json:"name"`
		Scopes    []string `json:"scopes"`
		ExpiresAt *int64   `json:"expires_at"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": `body 需为 {"name":"...","scopes":["read","write","admin","edge"],"expires_at":...}`})
		return
	}
	scopes := normalizeScopes(body.Scopes)
	if len(scopes) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "scopes 必须为非空 read|write|admin|edge 子集"})
		return
	}
	for _, sc := range scopes {
		if !tokenScopes[sc] {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "scopes 必须为非空 read|write|admin|edge 子集"})
			return
		}
	}
	plain, hash, prefix, err := auth.NewTenantToken()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "generate token"})
		return
	}
	row, err := s.cfg.Store.CreateTenantToken(p.TenantID, strings.TrimSpace(body.Name), hash, prefix, jsonArray(scopes), body.ExpiresAt)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	view := tokenView(row)
	writeJSON(w, http.StatusOK, map[string]any{
		"token": plain, "id": view.ID, "name": view.Name, "prefix": view.Prefix,
		"scopes": view.Scopes, "created_at": view.CreatedAt, "expires_at": view.ExpiresAt,
		"last_used_at": view.LastUsedAt, "revoked_at": view.RevokedAt,
	})
}

// handleDeleteToken DELETE /api/tokens/{id}：吊销令牌，幂等返回 204；跨租户/不存在 404。
func (s *Server) handleDeleteToken(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	if s.cfg.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store unavailable"})
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "token not found"})
		return
	}
	hit, err := s.cfg.Store.RevokeTenantToken(id, p.TenantID)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "token not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if !hit {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "token not found"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func normalizeScopes(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
