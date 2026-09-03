package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/audit"
	"github.com/DeliciousBuding/cloud-path/internal/auth"
	"github.com/DeliciousBuding/cloud-path/internal/plugincatalog"
	"github.com/DeliciousBuding/cloud-path/internal/plugincontrol"
	"github.com/DeliciousBuding/cloud-path/internal/secrethandle"
	"github.com/DeliciousBuding/cloud-path/internal/server/storeport"
	"github.com/DeliciousBuding/cloud-path/internal/tenantpolicy"
)

// 本文件是插件实例管理写 API（docs/architecture/control-plane-sync.md §6）。
//
// 写路径固定顺序：RBAC → 输入/secret 校验 → 权限扩大确认 → 配额 → 事务写入
// （desired 更新 + revision +1）→ 审计 → 通知在线 Edge。任何一步失败都不产生半状态：
// 不增 revision、不留「成功」审计、失败必须记真实审计。

const (
	pluginWriteBodyLimit    = 8 << 10
	maxPluginConfigKeys     = 32
	maxPluginConfigKeyLen   = 64
	maxPluginConfigValueLen = 512
	maxPluginSecretRefs     = 16
	maxPluginInstanceIDLen  = 64
	maxPluginIDLen          = 128
	maxPluginVersionLen     = 64
)

// pluginErrStoreUnavailable 是 PluginStore 未接线时的稳定码（不在契约错误码表内，
// 因为它表示部署缺陷而非用户错误；前端按 message 呈现即可）。
const pluginErrStoreUnavailable = "plugin_store_unavailable"

// credentialKeyWords 是「键名形似凭据」的判定词表：这些键的值必须是 secret://<name>
// handle，明文一律拒绝（tenant-security-policy §2.2：Server 永不接收明文）。
var credentialKeyWords = []string{
	"password", "passwd", "pwd", "token", "secret", "credential",
	"apikey", "api_key", "api-key", "accesskey", "access_key",
	"privatekey", "private_key", "authorization", "cookie",
	"sessionid", "session_id",
}

// urlCredentialPattern 命中形如 scheme://user:pass@host 的内联凭据。
var urlCredentialPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.\-]*://[^/\s@:]+:[^/\s@]+@`)

// pluginWriteError 是一次写失败：稳定错误码 + HTTP 状态 + 人类可读消息。
// 前端按 code 呈现，绝不解析 message。
type pluginWriteError struct {
	code    string
	status  int
	message string
}

func newPluginWriteError(code string, status int, format string, args ...any) *pluginWriteError {
	return &pluginWriteError{code: code, status: status, message: fmt.Sprintf(format, args...)}
}

// pluginWriteContext 是一次写请求的已鉴权上下文。
type pluginWriteContext struct {
	principal  *auth.Principal
	tenantID   int64
	tenantSlug string
	requestID  string
	role       string
	isAdmin    bool
}

// pluginWriteContext 解析写请求身份。非账号模式（回环开发态）等价 default 租户 admin，
// 与 authWrite 语义一致；账号模式下缺租户 id 一律 fail-closed
// （tenant-security-policy §6 不变量 1：缺 tenant principal 不得进入多租户写路径）。
func (s *Server) resolvePluginWriteContext(r *http.Request) (pluginWriteContext, *pluginWriteError) {
	ctx := pluginWriteContext{requestID: audit.RequestID(r.Context())}
	p := auth.FromContext(r.Context())
	if p == nil {
		ctx.tenantID = s.auditTenantID(nil)
		ctx.tenantSlug = defaultTenantSlug
		ctx.role = string(api.RoleAdmin)
		ctx.isAdmin = true
		return ctx, nil
	}
	if p.TenantID <= 0 || strings.TrimSpace(p.TenantSlug) == "" {
		return ctx, &pluginWriteError{code: "authentication_required", status: http.StatusUnauthorized,
			message: "authentication required"}
	}
	ctx.principal = p
	ctx.tenantID = p.TenantID
	ctx.tenantSlug = p.TenantSlug
	ctx.role = p.Role
	ctx.isAdmin = auth.RoleAllows(p.Role, string(api.RoleAdmin))
	return ctx, nil
}

// writePluginError 输出稳定错误码响应并记失败审计（审计必须记录真实失败）。
func (s *Server) writePluginError(w http.ResponseWriter, r *http.Request, ctx pluginWriteContext, e *pluginWriteError) {
	s.writePluginErrorOnly(w, ctx, e)
	s.auditPluginWrite(r, ctx, actionForPath(r), pluginTargetID(r), audit.OutcomeFailure, e.code, nil)
}

// writePluginErrorOnly 只输出响应不审计（配额拒绝已单独做节流审计时使用）。
func (s *Server) writePluginErrorOnly(w http.ResponseWriter, ctx pluginWriteContext, e *pluginWriteError) {
	writeJSON(w, e.status, map[string]any{
		"error": e.code, "code": e.code, "message": e.message, "request_id": ctx.requestID,
	})
}

// actionForPath 把请求路径映射为审计动作（写审计的统一入口用）。
func actionForPath(r *http.Request) string {
	switch {
	case strings.HasSuffix(r.URL.Path, "/reconcile"):
		return actionPluginReconcile
	case r.Method == http.MethodPost:
		return actionPluginCreate
	case r.Method == http.MethodPatch:
		return actionPluginUpdate
	case r.Method == http.MethodDelete:
		return actionPluginDelete
	default:
		return actionPluginUpdate
	}
}

// pluginTargetID 返回审计目标 id（instance id 优先，创建时可能只在 body 里）。
func pluginTargetID(r *http.Request) string {
	if id := chi.URLParam(r, "id"); id != "" {
		return id
	}
	return r.URL.Path
}

// auditPluginWrite 记一条插件写审计。metadata 只含非敏感事实（code/revision/edge/purge），
// 绝不含配置值、secret 明文或本机路径。
func (s *Server) auditPluginWrite(r *http.Request, ctx pluginWriteContext, action, targetID, outcome, reason string, extra *audit.Metadata) {
	meta := extra
	if meta == nil {
		meta = audit.NewMetadata()
	}
	if reason != "" {
		meta.String("reason", reason)
	}
	actorType, actorID, actorName := auditActor(ctx.principal)
	ev := audit.Event{
		TenantID: ctx.tenantID, ActorType: actorType, ActorID: actorID, ActorName: actorName,
		Action: action, TargetType: targetPluginInstance, TargetID: targetID,
		Outcome: outcome, RequestID: ctx.requestID, Metadata: meta.Map(),
	}
	if r != nil {
		ev.RemoteIP = auth.ClientIP(r, s.trustedProxies)
	}
	s.recordAudit(ev)
}

// ---------- POST /api/plugin-instances ----------

// handleCreatePluginInstance 创建期望态实例：operator 可写，绑定 secret 或扩大权限
// 需 admin 或显式 confirm_permissions；配额超限不增 revision 并记失败审计。
func (s *Server) handleCreatePluginInstance(w http.ResponseWriter, r *http.Request) {
	ctx, werr := s.resolvePluginWriteContext(r)
	if werr != nil {
		s.writePluginError(w, r, ctx, werr)
		return
	}
	p := s.plugin
	if !p.enabled() {
		s.writePluginError(w, r, ctx, &pluginWriteError{code: pluginErrStoreUnavailable,
			status: http.StatusServiceUnavailable, message: "store unavailable"})
		return
	}
	var req api.PluginInstanceCreateRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, pluginWriteBodyLimit)).Decode(&req); err != nil {
		s.writePluginError(w, r, ctx, newPluginWriteError(api.PluginErrInvalidConfig,
			http.StatusBadRequest, "body 需为合法 JSON 的 PluginInstanceCreateRequest"))
		return
	}
	edgeID := strings.TrimSpace(req.EdgeID)
	instanceID := strings.TrimSpace(req.InstanceID)
	pluginID := strings.TrimSpace(req.PluginID)
	version := strings.TrimSpace(req.Version)
	target := instanceID
	if target == "" {
		target = pluginTargetID(r)
	}
	if !validEdgeID(edgeID) {
		s.writePluginError(w, r, ctx, newPluginWriteError(api.PluginErrInvalidConfig,
			http.StatusBadRequest, "edge_id 非法"))
		return
	}
	if !validPluginSegment(instanceID, maxPluginInstanceIDLen) {
		s.writePluginError(w, r, ctx, newPluginWriteError(api.PluginErrInvalidConfig,
			http.StatusBadRequest, "instance_id 非法（允许字母/数字/._-，长度 1..%d）", maxPluginInstanceIDLen))
		return
	}
	if !validPluginSegment(pluginID, maxPluginIDLen) {
		s.writePluginError(w, r, ctx, newPluginWriteError(api.PluginErrInvalidConfig,
			http.StatusBadRequest, "plugin_id 非法"))
		return
	}
	if version == "" || len(version) > maxPluginVersionLen || strings.ContainsAny(version, "\r\n\x00") {
		s.writePluginError(w, r, ctx, newPluginWriteError(api.PluginErrInvalidConfig,
			http.StatusBadRequest, "version 必填且长度 <=%d", maxPluginVersionLen))
		return
	}
	isolation := strings.TrimSpace(req.Isolation)
	if isolation == "" {
		isolation = plugincontrol.IsolationShared
	}
	if _, err := plugincontrol.ParseIsolation(isolation); err != nil {
		s.writePluginError(w, r, ctx, newPluginWriteError(api.PluginErrInvalidConfig,
			http.StatusBadRequest, "isolation 只支持 %q 或 %q",
			plugincontrol.IsolationShared, plugincontrol.IsolationPerInstance))
		return
	}
	cfg, refs, werr := normalizePluginConfig(req.Config, req.SecretRefs, nil)
	if werr != nil {
		s.writePluginError(w, r, ctx, werr)
		return
	}
	// 跨租户 edge 一律 fail-closed，并按「不存在」语义回应（不泄漏他人 edge 是否存在）。
	if owner := s.edgeOwnerSlug(edgeID); owner != "" && owner != ctx.tenantSlug {
		s.writePluginError(w, r, ctx, &pluginWriteError{code: api.PluginErrNotFound,
			status: http.StatusNotFound, message: "plugin instance not found"})
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	if werr := s.checkSecretDeclared(ctx.tenantID, edgeID, pluginID, refs); werr != nil {
		s.writePluginError(w, r, ctx, werr)
		return
	}

	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	if _, err := p.ensureLoaded(ctx.tenantID, ctx.tenantSlug); err != nil {
		slog.Warn("plugin create: load tenant plane", "err", err, "tenant_id", ctx.tenantID)
		// 存储不可用是部署/IO 缺陷，不是用户冲突：用独立稳定码而不是占用 conflict。
		s.writePluginError(w, r, ctx, &pluginWriteError{code: pluginErrStoreUnavailable,
			status: http.StatusInternalServerError, message: "plugin store unavailable"})
		return
	}
	if dupEdge, dup := p.findInstanceLocked(ctx.tenantID, instanceID); dup {
		s.writePluginError(w, r, ctx, newPluginWriteError(api.PluginErrConflict,
			http.StatusConflict, "instance_id %q 已存在于本租户 edge %q", instanceID, dupEdge))
		return
	}
	if reasons := createEscalationReasons(refs); len(reasons) > 0 &&
		!req.ConfirmPermissions && !ctx.isAdmin {
		s.writePluginError(w, r, ctx, newPluginWriteError(api.PluginErrPermissionConfirm,
			http.StatusForbidden, "需要显式确认权限扩大：%s", strings.Join(reasons, ",")))
		return
	}
	if err := p.admitQuota(ctx.tenantID); err != nil {
		s.rejectQuota(w, r, ctx, target, err)
		return
	}
	now := time.Now().Unix()
	row := storeport.PluginInstanceRow{
		TenantID: ctx.tenantID, EdgeID: edgeID, InstanceID: instanceID,
		PluginID: pluginID, Version: version, Enabled: enabled, Isolation: isolation,
		ConfigJSON: encodeConfigJSON(cfg), SecretRefs: encodeSecretRefs(refs),
		CreatedAt: now, UpdatedAt: now,
	}
	rev, err := p.store.CreatePluginInstance(row)
	if err != nil {
		s.writePluginError(w, r, ctx, mapStoreError(err, instanceID))
		return
	}
	row.Revision = rev
	row.CreatedAt, row.UpdatedAt = now, now
	p.remember(row)
	s.auditPluginWrite(r, ctx, actionPluginCreate, instanceID, audit.OutcomeSuccess, "",
		audit.NewMetadata().String("edge_id", edgeID).String("plugin_id", pluginID).
			Int("revision", int64(rev)).Bool("enabled", enabled))
	view := s.pluginInstanceView(ctx.tenantID, ctx.tenantSlug, edgeID, instanceID)
	s.pushPluginDesired(ctx.tenantID, edgeID)
	writePluginSuccess(w, ctx, rev, view)
}

// ---------- PATCH /api/plugin-instances/{id} ----------

// handleUpdatePluginInstance 修改期望态实例：只更新出现的字段；isolation 降级、
// secret binding 变更需 admin 或显式确认；成功才 +1 revision。
func (s *Server) handleUpdatePluginInstance(w http.ResponseWriter, r *http.Request) {
	ctx, werr := s.resolvePluginWriteContext(r)
	if werr != nil {
		s.writePluginError(w, r, ctx, werr)
		return
	}
	p := s.plugin
	if !p.enabled() {
		s.writePluginError(w, r, ctx, &pluginWriteError{code: pluginErrStoreUnavailable,
			status: http.StatusServiceUnavailable, message: "store unavailable"})
		return
	}
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	var req api.PluginInstanceUpdateRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, pluginWriteBodyLimit)).Decode(&req); err != nil {
		s.writePluginError(w, r, ctx, newPluginWriteError(api.PluginErrInvalidConfig,
			http.StatusBadRequest, "body 需为合法 JSON 的 PluginInstanceUpdateRequest"))
		return
	}

	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	if _, err := p.ensureLoaded(ctx.tenantID, ctx.tenantSlug); err != nil {
		// 存储不可用是部署/IO 缺陷，不是用户冲突：用独立稳定码而不是占用 conflict。
		s.writePluginError(w, r, ctx, &pluginWriteError{code: pluginErrStoreUnavailable,
			status: http.StatusInternalServerError, message: "plugin store unavailable"})
		return
	}
	row, ok := p.getInstanceLocked(ctx.tenantID, id)
	if !ok {
		s.writePluginError(w, r, ctx, &pluginWriteError{code: api.PluginErrNotFound,
			status: http.StatusNotFound, message: "plugin instance not found"})
		return
	}
	next := row
	if req.Version != nil {
		v := strings.TrimSpace(*req.Version)
		if v == "" || len(v) > maxPluginVersionLen || strings.ContainsAny(v, "\r\n\x00") {
			s.writePluginError(w, r, ctx, newPluginWriteError(api.PluginErrInvalidConfig,
				http.StatusBadRequest, "version 非法"))
			return
		}
		next.Version = v
	}
	if req.Enabled != nil {
		next.Enabled = *req.Enabled
	}
	beforeIsolation := row.Isolation
	if req.Isolation != nil {
		iso := strings.TrimSpace(*req.Isolation)
		if _, err := plugincontrol.ParseIsolation(iso); err != nil || iso == "" {
			s.writePluginError(w, r, ctx, newPluginWriteError(api.PluginErrInvalidConfig,
				http.StatusBadRequest, "isolation 只支持 %q 或 %q",
				plugincontrol.IsolationShared, plugincontrol.IsolationPerInstance))
			return
		}
		next.Isolation = iso
	}
	var prevRefs []string
	if req.SecretRefs == nil {
		prevRefs = decodeSecretRefs(row.SecretRefs)
	}
	baseCfg := decodeConfigJSON(row.ConfigJSON)
	if req.Config != nil {
		baseCfg = req.Config
	}
	cfg, refs, werr := normalizePluginConfig(baseCfg, req.SecretRefs, prevRefs)
	if werr != nil {
		s.writePluginError(w, r, ctx, werr)
		return
	}
	next.ConfigJSON, next.SecretRefs = encodeConfigJSON(cfg), encodeSecretRefs(refs)
	if werr := s.checkSecretDeclared(ctx.tenantID, row.EdgeID, next.PluginID, refs); werr != nil {
		s.writePluginError(w, r, ctx, werr)
		return
	}
	reasons := updateEscalationReasons(beforeIsolation, next.Isolation,
		decodeSecretRefs(row.SecretRefs), refs)
	if len(reasons) > 0 && !req.ConfirmPermissions && !ctx.isAdmin {
		s.writePluginError(w, r, ctx, newPluginWriteError(api.PluginErrPermissionConfirm,
			http.StatusForbidden, "需要显式确认权限扩大：%s", strings.Join(reasons, ",")))
		return
	}
	next.UpdatedAt = time.Now().Unix()
	rev, err := p.store.UpdatePluginInstance(next)
	if err != nil {
		s.writePluginError(w, r, ctx, mapStoreError(err, id))
		return
	}
	next.Revision = rev
	p.remember(next)
	meta := audit.NewMetadata().String("edge_id", row.EdgeID).String("plugin_id", next.PluginID).
		Int("revision", int64(rev)).Bool("enabled", next.Enabled)
	if len(reasons) > 0 {
		meta.String("confirmed_escalation", strings.Join(reasons, ","))
	}
	s.auditPluginWrite(r, ctx, actionPluginUpdate, id, audit.OutcomeSuccess, "", meta)
	view := s.pluginInstanceView(ctx.tenantID, ctx.tenantSlug, row.EdgeID, id)
	s.pushPluginDesired(ctx.tenantID, row.EdgeID)
	writePluginSuccess(w, ctx, rev, view)
}

// ---------- DELETE /api/plugin-instances/{id} ----------

// handleDeletePluginInstance 删除期望态实例：默认保留插件数据（observed 投影标 stale），
// purge=true 才删投影，且 purge 要求 admin（契约无 confirm 字段）。
func (s *Server) handleDeletePluginInstance(w http.ResponseWriter, r *http.Request) {
	ctx, werr := s.resolvePluginWriteContext(r)
	if werr != nil {
		s.writePluginError(w, r, ctx, werr)
		return
	}
	p := s.plugin
	if !p.enabled() {
		s.writePluginError(w, r, ctx, &pluginWriteError{code: pluginErrStoreUnavailable,
			status: http.StatusServiceUnavailable, message: "store unavailable"})
		return
	}
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	purge := false
	if q := strings.TrimSpace(r.URL.Query().Get("purge")); q != "" {
		purge = q == "1" || strings.EqualFold(q, "true")
	}
	if r.Body != nil && r.ContentLength != 0 {
		var req api.PluginInstanceDeleteRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, pluginWriteBodyLimit)).Decode(&req); err == nil {
			purge = purge || req.Purge
		}
	}

	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	if _, err := p.ensureLoaded(ctx.tenantID, ctx.tenantSlug); err != nil {
		// 存储不可用是部署/IO 缺陷，不是用户冲突：用独立稳定码而不是占用 conflict。
		s.writePluginError(w, r, ctx, &pluginWriteError{code: pluginErrStoreUnavailable,
			status: http.StatusInternalServerError, message: "plugin store unavailable"})
		return
	}
	row, ok := p.getInstanceLocked(ctx.tenantID, id)
	if !ok {
		s.writePluginError(w, r, ctx, &pluginWriteError{code: api.PluginErrNotFound,
			status: http.StatusNotFound, message: "plugin instance not found"})
		return
	}
	if purge && !ctx.isAdmin {
		s.writePluginError(w, r, ctx, newPluginWriteError(api.PluginErrPermissionConfirm,
			http.StatusForbidden, "purge 删除插件数据需要 admin 权限"))
		return
	}
	rev, err := p.store.DeletePluginInstance(ctx.tenantID, row.EdgeID, id, purge)
	if err != nil {
		s.writePluginError(w, r, ctx, mapStoreError(err, id))
		return
	}
	p.forget(ctx.tenantID, row.EdgeID, id, rev)
	if purge {
		p.forgetObserved(ctx.tenantID, row.EdgeID, id)
	}
	s.auditPluginWrite(r, ctx, actionPluginDelete, id, audit.OutcomeSuccess, "",
		audit.NewMetadata().String("edge_id", row.EdgeID).String("plugin_id", row.PluginID).
			Int("revision", int64(rev)).Bool("purge", purge))
	view := s.pluginInstanceView(ctx.tenantID, ctx.tenantSlug, row.EdgeID, id)
	s.pushPluginDesired(ctx.tenantID, row.EdgeID)
	writePluginSuccess(w, ctx, rev, view)
}

// ---------- POST /api/plugin-instances/{id}/reconcile ----------

// handleReconcilePluginInstance 触发一次收敛：把当前完整期望态快照重新下发给在线 Edge。
// reconcile 不改期望态，因此**不增加 revision**；Edge 离线明确失败（不伪装成功）。
func (s *Server) handleReconcilePluginInstance(w http.ResponseWriter, r *http.Request) {
	ctx, werr := s.resolvePluginWriteContext(r)
	if werr != nil {
		s.writePluginError(w, r, ctx, werr)
		return
	}
	p := s.plugin
	if !p.enabled() {
		s.writePluginError(w, r, ctx, &pluginWriteError{code: pluginErrStoreUnavailable,
			status: http.StatusServiceUnavailable, message: "store unavailable"})
		return
	}
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	var req api.PluginInstanceActionRequest
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, pluginWriteBodyLimit)).Decode(&req); err != nil {
			s.writePluginError(w, r, ctx, newPluginWriteError(api.PluginErrInvalidConfig,
				http.StatusBadRequest, "body 需为合法 JSON 的 PluginInstanceActionRequest"))
			return
		}
	}
	if _, err := p.ensureLoaded(ctx.tenantID, ctx.tenantSlug); err != nil {
		// 存储不可用是部署/IO 缺陷，不是用户冲突：用独立稳定码而不是占用 conflict。
		s.writePluginError(w, r, ctx, &pluginWriteError{code: pluginErrStoreUnavailable,
			status: http.StatusInternalServerError, message: "plugin store unavailable"})
		return
	}
	row, ok := p.getInstanceLocked(ctx.tenantID, id)
	if !ok {
		s.writePluginError(w, r, ctx, &pluginWriteError{code: api.PluginErrNotFound,
			status: http.StatusNotFound, message: "plugin instance not found"})
		return
	}
	rev, err := p.store.PluginDesiredRevision(ctx.tenantID, row.EdgeID)
	if err != nil {
		rev = row.Revision
	}
	if !s.edgeOnlineFor(ctx.tenantID, row.EdgeID) {
		s.writePluginError(w, r, ctx, newPluginWriteError(api.PluginErrEdgeOffline,
			http.StatusConflict, "edge %q 离线，无法立即收敛（期望态已保存，重连后自动下发）", row.EdgeID))
		return
	}
	if !s.pushPluginDesired(ctx.tenantID, row.EdgeID) {
		s.writePluginError(w, r, ctx, newPluginWriteError(api.PluginErrEdgeOffline,
			http.StatusConflict, "edge %q 发送队列满，请稍后重试", row.EdgeID))
		return
	}
	s.auditPluginWrite(r, ctx, actionPluginReconcile, id, audit.OutcomeSuccess, "",
		audit.NewMetadata().String("edge_id", row.EdgeID).Int("revision", int64(rev)).
			Bool("force", req.Force))
	view := s.pluginInstanceView(ctx.tenantID, ctx.tenantSlug, row.EdgeID, id)
	writePluginSuccess(w, ctx, rev, view)
}

// ---------- 共享辅助 ----------

func writePluginSuccess(w http.ResponseWriter, ctx pluginWriteContext, revision uint64, view api.PluginInstanceView) {
	writeJSON(w, http.StatusOK, api.PluginInstanceWriteResponse{
		ID: view.ID, Revision: revision, RequestID: ctx.requestID, Instance: view,
	})
}

// rejectQuota 处理配额拒绝：不增 revision、不留成功审计，按 §4.3 节流失败审计。
func (s *Server) rejectQuota(w http.ResponseWriter, r *http.Request, ctx pluginWriteContext, target string, err error) {
	var qe *tenantpolicy.QuotaError
	limit, usage := 0, 0
	if errors.As(err, &qe) {
		limit, usage = qe.Limit, qe.Usage
	}
	e := newPluginWriteError(api.PluginErrQuota, http.StatusTooManyRequests,
		"插件实例配额已满（limit=%d usage=%d）", limit, usage)
	if !s.plugin.allowQuotaAudit(ctx.tenantID) {
		s.writePluginErrorOnly(w, ctx, e)
		return
	}
	s.writePluginError(w, r, ctx, e)
	s.recordAudit(audit.Event{
		TenantID: ctx.tenantID, ActorType: audit.ActorSystem, ActorName: "quota",
		Action: actionPluginQuota, TargetType: targetPluginInstance, TargetID: target,
		Outcome: audit.OutcomeFailure, RequestID: ctx.requestID,
		Metadata: audit.NewMetadata().
			String("resource", string(tenantpolicy.ResourcePluginInstances)).
			Int("limit", int64(limit)).Int("usage", int64(usage)).Map(),
	})
}

// mapStoreError 把端口错误映射为稳定 HTTP 错误。
func mapStoreError(err error, id string) *pluginWriteError {
	switch {
	case errors.Is(err, storeport.ErrConflict):
		return newPluginWriteError(api.PluginErrConflict, http.StatusConflict,
			"plugin instance %q conflicts with an existing identity", id)
	case errors.Is(err, storeport.ErrQuota):
		return newPluginWriteError(api.PluginErrQuota, http.StatusTooManyRequests,
			"plugin instance quota exceeded")
	case errors.Is(err, storeport.ErrNotFound):
		return &pluginWriteError{code: api.PluginErrNotFound, status: http.StatusNotFound,
			message: "plugin instance not found"}
	case errors.Is(err, storeport.ErrTenantMismatch):
		// 跨租户行 fail-closed，按不存在回应（不泄漏他人实例存在性）。
		return &pluginWriteError{code: api.PluginErrNotFound, status: http.StatusNotFound,
			message: "plugin instance not found"}
	default:
		slog.Warn("plugin store write failed", "err", err, "instance", id)
		return &pluginWriteError{code: pluginErrStoreUnavailable,
			status: http.StatusInternalServerError, message: "plugin store write failed"}
	}
}

// edgeOwnerSlug 返回 edge 已绑定的租户 slug（在线连接优先，其次内存 sticky 绑定）；
// 未知返回空串（允许为尚未接入的 edge 预置期望态）。
func (s *Server) edgeOwnerSlug(edgeID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if l, ok := s.edges[edgeID]; ok && l != nil {
		return l.tenant
	}
	return s.edgeTenants[edgeID]
}

// findInstanceLocked 按 instance id 在租户内查找 edge（调用方持 p.writeMu/p.mu 之一）。
func (p *pluginPlane) findInstanceLocked(tenantID int64, instanceID string) (string, bool) {
	t := p.tenants[tenantID]
	if t == nil {
		return "", false
	}
	edges := make([]string, 0, 2)
	for k := range t.instances {
		if k.instanceID == instanceID {
			edges = append(edges, k.edgeID)
		}
	}
	if len(edges) == 0 {
		return "", false
	}
	sort.Strings(edges)
	return edges[0], true
}

// getInstanceLocked 返回租户内该 instance id 的期望态行。
func (p *pluginPlane) getInstanceLocked(tenantID int64, instanceID string) (storeport.PluginInstanceRow, bool) {
	t := p.tenants[tenantID]
	if t == nil {
		return storeport.PluginInstanceRow{}, false
	}
	keys := make([]pluginInstKey, 0, len(t.instances))
	for k := range t.instances {
		if k.instanceID == instanceID {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return storeport.PluginInstanceRow{}, false
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].edgeID < keys[j].edgeID })
	return t.instances[keys[0]], true
}

// pluginInstanceView 构造契约视图（与 GET /api/plugin-instances 同一份计算逻辑）。
func (s *Server) pluginInstanceView(tenantID int64, slug, edgeID, instanceID string) api.PluginInstanceView {
	p := s.plugin
	if !p.enabled() {
		return api.PluginInstanceView{ID: instanceID, EdgeID: edgeID}
	}
	online := s.pluginEdgeOnline()
	now := p.now().Unix()
	staleAfter := int64(p.staleAfter / time.Second)

	p.mu.Lock()
	defer p.mu.Unlock()
	t, err := p.ensureLoadedLocked(tenantID, slug)
	if err != nil || t == nil {
		return api.PluginInstanceView{ID: instanceID, EdgeID: edgeID}
	}
	ep := p.edgePlaneLocked(t, edgeID)
	edgeOnline := online[pluginEdgeKey{tenantID: tenantID, edgeID: edgeID}]
	pi := plugincatalog.ProjectionInstance{
		TenantID: tenantID, Tenant: t.slug, EdgeID: edgeID, InstanceID: instanceID,
		EdgeOnline: edgeOnline, DesiredRevision: ep.desiredRevision,
		AppliedRevision: ep.appliedRevision, Drift: ep.desiredRevision != ep.appliedRevision,
		Stale: !edgeOnline || (now-ep.lastReportAt) > staleAfter, LastAckAt: ep.lastAckAt,
	}
	if row, ok := t.instances[pluginInstKey{edgeID: edgeID, instanceID: instanceID}]; ok {
		pi.PluginID, pi.Version, pi.Enabled, pi.Isolation = row.PluginID, row.Version, row.Enabled, row.Isolation
		pi.SecretRefs = decodeSecretRefs(row.SecretRefs)
		pi.Config = decodeConfigJSON(row.ConfigJSON)
		pi.ConfigPresent = len(pi.Config) > 0
		pi.UpdatedAt, pi.RowRevision = row.UpdatedAt, row.Revision
	}
	if o, ok := ep.observed[instanceID]; ok {
		pi.HasObserved = true
		pi.ObservedVersion = o.Version
		pi.State, pi.Health, pi.Detail = o.State, o.Health, o.Detail
		pi.RestartCount, pi.LastHealthy, pi.MessageRate = o.RestartCount, o.LastHealthy, o.MessageRate
		pi.ReportedAt = ep.lastReportAt
	}
	return plugincatalog.APIInstanceView(pi)
}

// ---------- 输入校验与 secret 边界 ----------

// validPluginSegment 校验 instance/plugin id 形状：稳定、可跨边界传递、无路径逃逸。
func validPluginSegment(s string, maxLen int) bool {
	if s == "" || len(s) > maxLen || s == "." || s == ".." {
		return false
	}
	for i, r := range s {
		if r > 127 {
			return false
		}
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '.' || r == '_' || r == '-'
		if !ok {
			return false
		}
		if i == 0 && (r == '.' || r == '-') {
			return false
		}
	}
	return !strings.ContainsAny(s, `/\:`)
}

// normalizePluginConfig 校验并规范化实例配置：
//   - secret://<name> 值只做结构校验并抽取 handle 名（Server 永不解析明文）；
//   - 键名形似凭据却给明文值 → 拒绝（PluginErrSecretForbidden）；
//   - 内联 URL 凭据（scheme://user:pass@host）→ 拒绝；
//   - 显式 secret_refs 与配置里的 handle 合并去重（refs 为 nil 表示沿用既有值）。
func normalizePluginConfig(cfg map[string]string, refs, prevRefs []string) (map[string]string, []string, *pluginWriteError) {
	if len(cfg) > maxPluginConfigKeys {
		return nil, nil, newPluginWriteError(api.PluginErrInvalidConfig, http.StatusBadRequest,
			"config 键数量上限 %d", maxPluginConfigKeys)
	}
	out := make(map[string]string, len(cfg))
	derived := make([]string, 0, len(cfg))
	for k, v := range cfg {
		kt := strings.TrimSpace(k)
		if kt == "" || len(kt) > maxPluginConfigKeyLen || strings.ContainsAny(kt, "\r\n\x00\t") {
			return nil, nil, newPluginWriteError(api.PluginErrInvalidConfig, http.StatusBadRequest,
				"config 键非法（长度 1..%d，不含控制字符）", maxPluginConfigKeyLen)
		}
		if len(v) > maxPluginConfigValueLen || strings.ContainsAny(v, "\r\n\x00") {
			return nil, nil, newPluginWriteError(api.PluginErrInvalidConfig, http.StatusBadRequest,
				"config 值非法（长度 <=%d，不含换行/NUL）", maxPluginConfigValueLen)
		}
		if strings.HasPrefix(v, secrethandle.Scheme) {
			h, err := secrethandle.Parse(v)
			if err != nil {
				return nil, nil, newPluginWriteError(api.PluginErrInvalidConfig, http.StatusBadRequest,
					"config 键 %q 的 secret handle 非法（期望 secret://<name>）", kt)
			}
			derived = append(derived, h.Name())
			out[kt] = h.String()
			continue
		}
		if credentialLikeKey(kt) || urlCredentialPattern.MatchString(v) {
			return nil, nil, newPluginWriteError(api.PluginErrSecretForbidden, http.StatusForbidden,
				"config 键 %q 看起来是凭据：Server 只接受 secret://<name> handle，明文一律拒绝", kt)
		}
		out[kt] = v
	}
	base := refs
	if base == nil {
		base = prevRefs
	}
	names, werr := normalizeSecretNames(base)
	if werr != nil {
		return nil, nil, werr
	}
	merged := mergeUnique(append(names, derived...))
	if len(merged) > maxPluginSecretRefs {
		return nil, nil, newPluginWriteError(api.PluginErrInvalidConfig, http.StatusBadRequest,
			"secret_refs 数量上限 %d", maxPluginSecretRefs)
	}
	return out, merged, nil
}

// normalizeSecretNames 接受裸名或完整 handle，统一归一为 handle 名。
func normalizeSecretNames(in []string) ([]string, *pluginWriteError) {
	out := make([]string, 0, len(in))
	for _, raw := range in {
		v := strings.TrimSpace(raw)
		if v == "" {
			continue
		}
		if !strings.HasPrefix(v, secrethandle.Scheme) {
			v = secrethandle.Scheme + v
		}
		h, err := secrethandle.Parse(v)
		if err != nil {
			return nil, newPluginWriteError(api.PluginErrInvalidConfig, http.StatusBadRequest,
				"secret_refs 含非法 handle 名")
		}
		out = append(out, h.Name())
	}
	return mergeUnique(out), nil
}

func mergeUnique(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil
	}
	return out
}

func credentialLikeKey(key string) bool {
	k := strings.ToLower(key)
	for _, w := range credentialKeyWords {
		if strings.Contains(k, w) {
			return true
		}
	}
	return false
}

// checkSecretDeclared 落实 secret 双重授权（tenant-security-policy §2.3）的服务端一半：
// 若已有该插件的安装物投影，handle 名必须在 manifest permissions.secrets 中声明；
// 投影未知（Edge 尚未上报）时放行——Edge 侧解析仍 fail-closed，且它是 manifest 的权威源。
func (s *Server) checkSecretDeclared(tenantID int64, edgeID, pluginID string, refs []string) *pluginWriteError {
	if len(refs) == 0 || !s.plugin.enabled() {
		return nil
	}
	p := s.plugin
	p.mu.Lock()
	t, err := p.ensureLoadedLocked(tenantID, "")
	var declared []string
	known := false
	if err == nil && t != nil {
		if ep, ok := t.edges[edgeID]; ok {
			if in, ok := ep.installations[pluginID]; ok {
				known = true
				declared = in.Permissions.Secrets
			}
		}
	}
	p.mu.Unlock()
	if !known {
		return nil
	}
	for _, name := range refs {
		h, herr := secrethandle.Parse(secrethandle.Scheme + name)
		if herr != nil || secrethandle.ValidateDeclared(h, declared) != nil {
			return newPluginWriteError(api.PluginErrSecretForbidden, http.StatusForbidden,
				"secret %q 未在插件 %q 的 permissions.secrets 中声明（fail-closed）", name, pluginID)
		}
	}
	return nil
}

// createEscalationReasons 判定创建是否属于需要显式确认的权限扩大。
// 绑定 secret = 授予插件新的凭据访问权，因此需要 admin 或 confirm_permissions。
func createEscalationReasons(refs []string) []string {
	var out []string
	if len(refs) > 0 {
		out = append(out, "secret_binding")
	}
	return out
}

// updateEscalationReasons 判定修改是否属于需要显式确认的权限扩大：
// isolation 由 per-instance 降级到 shared，或 secret binding 发生任何变更。
func updateEscalationReasons(beforeIsolation, afterIsolation string, beforeRefs, afterRefs []string) []string {
	var out []string
	if isolationRank(beforeIsolation) > isolationRank(afterIsolation) {
		out = append(out, "isolation_weakened")
	}
	if !sameStringSet(beforeRefs, afterRefs) {
		out = append(out, "secret_binding")
	}
	return out
}

// isolationRank 是隔离强度序：per-instance 强于 shared（plugincontrol 的稳定名）。
func isolationRank(v string) int {
	switch v {
	case plugincontrol.IsolationPerInstance:
		return 2
	case plugincontrol.IsolationShared:
		return 1
	default:
		return 0
	}
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]int, len(a))
	for _, s := range a {
		seen[s]++
	}
	for _, s := range b {
		seen[s]--
		if seen[s] < 0 {
			return false
		}
	}
	return true
}
