package server

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/DeliciousBuding/cloud-path/internal/audit"
	"github.com/DeliciousBuding/cloud-path/internal/auth"
	"github.com/DeliciousBuding/cloud-path/internal/store"
)

// auditWriteFunc 是审计落库函数：默认写 store；测试可注入失败/捕获实现。
type auditWriteFunc func(audit.Event) error

// defaultAuditWrite 返回默认审计落库实现。Store 为 nil 时直接成功（API-only 模式）。
func (s *Server) defaultAuditWrite() auditWriteFunc {
	return func(ev audit.Event) error {
		if s.cfg.Store == nil {
			return nil
		}
		return s.cfg.Store.InsertAuditEvent(storeEvent(ev))
	}
}

// storeEvent 把 audit.Event 映射为 store 行（metadata 在此序列化；禁止携带原 body/header）。
func storeEvent(ev audit.Event) store.AuditEvent {
	meta := "{}"
	if len(ev.Metadata) > 0 {
		if b, err := json.Marshal(ev.Metadata); err == nil {
			meta = string(b)
		}
	}
	return store.AuditEvent{
		TenantID: ev.TenantID, ActorType: ev.ActorType, ActorID: ev.ActorID, ActorName: ev.ActorName,
		Action: ev.Action, TargetType: ev.TargetType, TargetID: ev.TargetID, Outcome: ev.Outcome,
		RequestID: ev.RequestID, RemoteIP: ev.RemoteIP, MetadataJSON: meta,
	}
}

// recordAudit 落一条审计事件。审计写失败只 slog error，绝不改变业务结果。
func (s *Server) recordAudit(ev audit.Event) {
	if s.auditWrite == nil {
		return
	}
	if err := s.auditWrite(ev); err != nil {
		slog.Error("audit: write failed", "action", ev.Action, "err", err)
	}
}

// audit 从请求补齐 request id / remote ip 后落库。
func (s *Server) audit(r *http.Request, ev audit.Event) {
	if ev.RequestID == "" {
		ev.RequestID = audit.RequestID(r.Context())
	}
	if ev.RemoteIP == "" {
		ev.RemoteIP = auth.ClientIP(r, s.trustedProxies)
	}
	s.recordAudit(ev)
}

// auditActor 把请求身份映射为审计 actor。
func auditActor(p *auth.Principal) (string, int64, string) {
	if p == nil {
		return audit.ActorSystem, 0, "system"
	}
	if p.Token {
		return audit.ActorToken, p.UserID, p.Username
	}
	return audit.ActorUser, p.UserID, p.Username
}

// auditTenantID 解析审计事件归属租户：优先 principal 租户，否则 default。
func (s *Server) auditTenantID(p *auth.Principal) int64 {
	if p != nil && p.TenantID > 0 {
		return p.TenantID
	}
	if s.cfg.Store == nil {
		return 0
	}
	id, err := s.cfg.Store.EnsureDefaultTenant()
	if err != nil {
		slog.Warn("audit: resolve default tenant", "err", err)
		return 0
	}
	return id
}

// requestID 是请求 ID 中间件：接受合法 X-Request-ID（长度/字符上限），否则自生成；
// 响应头回传同 ID（TestRequestIDRoundtrip 锁定）。
func (s *Server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := audit.NormalizeRequestID(r.Header.Get("X-Request-ID"))
		if id == "" {
			id = audit.NewRequestID()
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(audit.WithRequestID(r.Context(), id)))
	})
}

// handleListAudit GET /api/audit?since=&action=&limit=：admin-only、本租户、limit 上限 1000。
func (s *Server) handleListAudit(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	if p == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	if s.cfg.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store unavailable"})
		return
	}
	since := int64(queryInt(r, "since", 0, 0))
	action := r.URL.Query().Get("action")
	limit := queryInt(r, "limit", 100, 1000)
	rows, err := s.cfg.Store.ListAuditEvents(p.TenantID, since, action, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out := make([]audit.Entry, 0, len(rows))
	for _, row := range rows {
		var meta map[string]any
		if err := json.Unmarshal([]byte(row.MetadataJSON), &meta); err != nil || meta == nil {
			meta = map[string]any{}
		}
		out = append(out, audit.Entry{
			ID: row.ID, TenantID: row.TenantID, ActorType: row.ActorType, ActorID: row.ActorID,
			ActorName: row.ActorName, Action: row.Action, TargetType: row.TargetType,
			TargetID: row.TargetID, Outcome: row.Outcome, RequestID: row.RequestID,
			RemoteIP: row.RemoteIP, Metadata: meta, CreatedAt: row.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": out})
}
