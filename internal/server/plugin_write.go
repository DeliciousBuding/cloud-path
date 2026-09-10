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
	"github.com/DeliciousBuding/cloud-path/internal/pluginhost"
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
	maxPluginConfigValueLen = 4096 // structured app config/bindings; total request remains bounded to 8 KiB
	maxPluginSecretRefs     = 16
	maxPluginInstanceIDLen  = 64
	maxPluginIDLen          = 128
	maxPluginVersionLen     = 64
)

// 插件写面的稳定码复用 api canonical 注册表。
const (
	pluginErrStoreUnavailable = api.PluginErrStoreUnavailable
	pluginErrKindUnavailable  = api.PluginErrKindUnavailable
	pluginErrHostMismatch     = api.PluginErrHostMismatch
	pluginErrKindUnsupported  = api.PluginErrKindUnsupported
)

// credentialKeyWords 是「键名形似凭据」的判定词表：这些键的值必须是 secret://<name>
// handle，明文一律拒绝（tenant-security-policy §2.2：Server 永不接收明文）。
var credentialKeyWords = []string{
	"password", "passwd", "pwd", "token", "secret", "credential",
	"apikey", "api_key", "api-key", "accesskey", "access_key",
	"privatekey", "private_key", "authorization", "cookie",
	"sessionid", "session_id",
}

// urlCredentialPattern 命中 URL 内联凭据（userinfo:password@host 形态，scheme 三件套打头）。
var urlCredentialPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.\-]*://[^/\s@:]+:[^/\s@]+@`)

// pluginWriteError 是一次写失败：稳定错误码 + HTTP 状态 + 人类可读消息。
// 前端按 code 呈现，绝不解析 message。
type pluginWriteError struct {
	code    string
	status  int
	message string
	params  map[string]any
}

func newPluginWriteError(code string, status int, format string, args ...any) *pluginWriteError {
	return &pluginWriteError{code: code, status: status, message: fmt.Sprintf(format, args...)}
}

func newPluginWriteErrorWithParams(code string, status int, params map[string]any, format string, args ...any) *pluginWriteError {
	return &pluginWriteError{code: code, status: status, message: fmt.Sprintf(format, args...), params: params}
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
		return ctx, &pluginWriteError{code: api.APIErrAuthenticationRequired, status: http.StatusUnauthorized,
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
	writeAPIErrorResponse(w, e.status, e.code, e.message, ctx.requestID, e.params)
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
		s.writePluginError(w, r, ctx, newPluginWriteErrorWithParams(api.PluginErrInvalidConfig,
			http.StatusBadRequest, map[string]any{"field": "body"}, "body must be a JSON PluginInstanceCreateRequest"))
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
		s.writePluginError(w, r, ctx, newPluginWriteErrorWithParams(api.PluginErrInvalidConfig,
			http.StatusBadRequest, map[string]any{"field": "edge_id"}, "edge_id is invalid"))
		return
	}
	if !validPluginSegment(instanceID, maxPluginInstanceIDLen) {
		s.writePluginError(w, r, ctx, newPluginWriteErrorWithParams(api.PluginErrInvalidConfig,
			http.StatusBadRequest, map[string]any{"field": "instance_id", "max_len": maxPluginInstanceIDLen}, "instance_id is invalid"))
		return
	}
	if !validPluginSegment(pluginID, maxPluginIDLen) {
		s.writePluginError(w, r, ctx, newPluginWriteErrorWithParams(api.PluginErrInvalidConfig,
			http.StatusBadRequest, map[string]any{"field": "plugin_id", "max_len": maxPluginIDLen}, "plugin_id is invalid"))
		return
	}
	if version == "" || len(version) > maxPluginVersionLen || strings.ContainsAny(version, "\r\n\x00") {
		s.writePluginError(w, r, ctx, newPluginWriteErrorWithParams(api.PluginErrInvalidConfig,
			http.StatusBadRequest, map[string]any{"field": "version", "max_len": maxPluginVersionLen}, "version is invalid"))
		return
	}
	isolation := strings.TrimSpace(req.Isolation)
	if isolation == "" {
		isolation = plugincontrol.IsolationShared
	}
	if _, err := plugincontrol.ParseIsolation(isolation); err != nil {
		s.writePluginError(w, r, ctx, newPluginWriteErrorWithParams(api.PluginErrInvalidConfig,
			http.StatusBadRequest, map[string]any{"field": "isolation", "allowed": []string{plugincontrol.IsolationShared, plugincontrol.IsolationPerInstance}}, "isolation is invalid"))
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
			status: http.StatusNotFound, message: "plugin instance not found", params: map[string]any{"instance_id": instanceID}})
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
		s.writePluginError(w, r, ctx, newPluginWriteErrorWithParams(api.PluginErrConflict,
			http.StatusConflict, map[string]any{"instance_id": instanceID, "edge_id": dupEdge}, "instance_id already exists"))
		return
	}
	if werr := s.validatePluginInstanceHost(ctx.tenantID, ctx.tenantSlug, edgeID, pluginID); werr != nil {
		s.writePluginError(w, r, ctx, werr)
		return
	}
	if reasons := createEscalationReasons(refs); len(reasons) > 0 &&
		!req.ConfirmPermissions && !ctx.isAdmin {
		s.writePluginError(w, r, ctx, newPluginWriteErrorWithParams(api.PluginErrPermissionConfirm,
			http.StatusForbidden, map[string]any{"reasons": reasons}, "permission escalation requires explicit confirmation"))
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
		s.writePluginError(w, r, ctx, newPluginWriteErrorWithParams(api.PluginErrInvalidConfig,
			http.StatusBadRequest, map[string]any{"field": "body"}, "body must be a JSON PluginInstanceUpdateRequest"))
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
			status: http.StatusNotFound, message: "plugin instance not found", params: map[string]any{"instance_id": id}})
		return
	}
	if werr := s.validatePluginInstanceHost(ctx.tenantID, ctx.tenantSlug, row.EdgeID, row.PluginID); werr != nil {
		s.writePluginError(w, r, ctx, werr)
		return
	}
	next := row
	if req.Version != nil {
		v := strings.TrimSpace(*req.Version)
		if v == "" || len(v) > maxPluginVersionLen || strings.ContainsAny(v, "\r\n\x00") {
			s.writePluginError(w, r, ctx, newPluginWriteErrorWithParams(api.PluginErrInvalidConfig,
				http.StatusBadRequest, map[string]any{"field": "version", "max_len": maxPluginVersionLen}, "version is invalid"))
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
			s.writePluginError(w, r, ctx, newPluginWriteErrorWithParams(api.PluginErrInvalidConfig,
				http.StatusBadRequest, map[string]any{"field": "isolation", "allowed": []string{plugincontrol.IsolationShared, plugincontrol.IsolationPerInstance}}, "isolation is invalid"))
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
		s.writePluginError(w, r, ctx, newPluginWriteErrorWithParams(api.PluginErrPermissionConfirm,
			http.StatusForbidden, map[string]any{"reasons": reasons}, "permission escalation requires explicit confirmation"))
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

// handleDeletePluginInstance 删除期望态实例：默认保留该实例私有数据（observed 投影标 stale），
// purge=true 才在同一 Store 写事务中清除 observed 投影、领域记录和定时任务，
// 且 purge 要求 admin（契约无 confirm 字段）。
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
			status: http.StatusNotFound, message: "plugin instance not found", params: map[string]any{"instance_id": id}})
		return
	}
	if purge && !ctx.isAdmin {
		s.writePluginError(w, r, ctx, newPluginWriteErrorWithParams(api.PluginErrPermissionConfirm,
			http.StatusForbidden, map[string]any{"action": "purge", "required_role": "admin"}, "purge requires admin"))
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
			s.writePluginError(w, r, ctx, newPluginWriteErrorWithParams(api.PluginErrInvalidConfig,
				http.StatusBadRequest, map[string]any{"field": "body"}, "body must be a JSON PluginInstanceActionRequest"))
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
			status: http.StatusNotFound, message: "plugin instance not found", params: map[string]any{"instance_id": id}})
		return
	}
	rev, err := p.store.PluginDesiredRevision(ctx.tenantID, row.EdgeID)
	if err != nil {
		rev = row.Revision
	}
	if !s.edgeOnlineFor(ctx.tenantID, row.EdgeID) {
		s.writePluginError(w, r, ctx, newPluginWriteErrorWithParams(api.PluginErrEdgeOffline,
			http.StatusConflict, map[string]any{"edge_id": row.EdgeID}, "edge is offline"))
		return
	}
	if !s.pushPluginDesired(ctx.tenantID, row.EdgeID) {
		s.writePluginError(w, r, ctx, newPluginWriteErrorWithParams(api.PluginErrEdgeOffline,
			http.StatusConflict, map[string]any{"edge_id": row.EdgeID, "reason": "send_queue_full"}, "edge send queue is full"))
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
	e := newPluginWriteErrorWithParams(api.PluginErrQuota, http.StatusTooManyRequests,
		map[string]any{"limit": limit, "usage": usage}, "plugin instance quota exceeded")
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
		return newPluginWriteErrorWithParams(api.PluginErrConflict, http.StatusConflict,
			map[string]any{"instance_id": id}, "plugin instance conflicts with an existing identity")
	case errors.Is(err, storeport.ErrQuota):
		return newPluginWriteError(api.PluginErrQuota, http.StatusTooManyRequests,
			"plugin instance quota exceeded")
	case errors.Is(err, storeport.ErrNotFound):
		return &pluginWriteError{code: api.PluginErrNotFound, status: http.StatusNotFound,
			message: "plugin instance not found", params: map[string]any{"instance_id": id}}
	case errors.Is(err, storeport.ErrTenantMismatch):
		// 跨租户行 fail-closed，按不存在回应（不泄漏他人实例存在性）。
		return &pluginWriteError{code: api.PluginErrNotFound, status: http.StatusNotFound,
			message: "plugin instance not found", params: map[string]any{"instance_id": id}}
	default:
		slog.Warn("plugin store write failed", "err", err, "instance", id)
		return &pluginWriteError{code: pluginErrStoreUnavailable,
			status: http.StatusInternalServerError, message: "plugin store write failed"}
	}
}

// validatePluginInstanceHost 解析目标插件 manifest kind，并按当前运行时宿主规则
// fail-closed。Driver 只允许真实 Edge，Application 只允许伪 edge server；
// Connector 尚无运行时，一律拒绝。任何 kind 无法解析的情况也不放行。
func (s *Server) validatePluginInstanceHost(tenantID int64, tenantSlug, edgeID, pluginID string) *pluginWriteError {
	kind, werr := s.resolvePluginInstanceKind(tenantID, tenantSlug, edgeID, pluginID)
	if werr != nil {
		return werr
	}
	if err := plugincontrol.ValidateInstanceHost(kind, edgeID == AppHostEdgeID); err != nil {
		if errors.Is(err, pluginhost.ErrConnectorUnsupported) {
			return newPluginWriteErrorWithParams(pluginErrKindUnsupported, http.StatusConflict,
				map[string]any{"plugin_id": pluginID, "kind": kind.String()}, "plugin kind is unsupported")
		}
		return newPluginWriteErrorWithParams(pluginErrHostMismatch, http.StatusConflict,
			map[string]any{"plugin_id": pluginID, "kind": kind.String(), "edge_id": edgeID}, "plugin kind does not match host")
	}
	return nil
}

// resolvePluginInstanceKind 只使用现有事实源：真实 Edge 用 plugin_status 的安装物
// 投影（其 Kind 来自 manifest）；Server 用 AppHost 的 plugins.lock + plugin.yaml。
func (s *Server) resolvePluginInstanceKind(tenantID int64, tenantSlug, edgeID, pluginID string) (pluginhost.Kind, *pluginWriteError) {
	if edgeID == AppHostEdgeID {
		if s.appHost == nil || !s.appHost.cfg.Enabled {
			return 0, newPluginWriteErrorWithParams(pluginErrKindUnavailable, http.StatusConflict,
				map[string]any{"plugin_id": pluginID, "edge_id": edgeID}, "plugin kind is unavailable")
		}
		kind, err := plugincontrol.InstalledPluginKind(s.appHost.cfg.PluginsDir, s.appHost.cfg.LockPath, pluginID)
		if err != nil {
			slog.Debug("plugin instance: server manifest kind unavailable", "plugin_id", pluginID)
			return 0, newPluginWriteErrorWithParams(pluginErrKindUnavailable, http.StatusConflict,
				map[string]any{"plugin_id": pluginID, "edge_id": edgeID}, "plugin kind is unavailable")
		}
		return kind, nil
	}

	p := s.plugin
	p.mu.Lock()
	defer p.mu.Unlock()
	t, err := p.ensureLoadedLocked(tenantID, tenantSlug)
	if err != nil || t == nil {
		return 0, &pluginWriteError{code: pluginErrStoreUnavailable,
			status: http.StatusInternalServerError, message: "plugin store unavailable"}
	}
	ep := t.edges[edgeID]
	if ep == nil {
		return 0, newPluginWriteErrorWithParams(pluginErrKindUnavailable, http.StatusConflict,
			map[string]any{"edge_id": edgeID, "plugin_id": pluginID}, "plugin kind is unavailable")
	}
	in, ok := ep.installations[pluginID]
	if !ok {
		return 0, newPluginWriteErrorWithParams(pluginErrKindUnavailable, http.StatusConflict,
			map[string]any{"edge_id": edgeID, "plugin_id": pluginID}, "plugin kind is unavailable")
	}
	kind, err := pluginhost.ParseKind(in.Kind)
	if err != nil {
		slog.Debug("plugin instance: edge manifest kind unavailable", "plugin_id", pluginID, "edge", edgeID)
		return 0, newPluginWriteErrorWithParams(pluginErrKindUnavailable, http.StatusConflict,
			map[string]any{"edge_id": edgeID, "plugin_id": pluginID}, "plugin kind is unavailable")
	}
	return kind, nil
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
		// An applied ack cannot override a known enabled-version mismatch.
		if pi.Enabled && pi.Version != "" && o.Version != "" && pi.Version != o.Version {
			pi.Drift = true
		}
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
//   - 内联 URL 凭据（userinfo:password@host 形态）→ 拒绝；
//   - 显式 secret_refs 与配置里的 handle 合并去重（refs 为 nil 表示沿用既有值）。
func normalizePluginConfig(cfg map[string]string, refs, prevRefs []string) (map[string]string, []string, *pluginWriteError) {
	if len(cfg) > maxPluginConfigKeys {
		return nil, nil, newPluginWriteErrorWithParams(api.PluginErrInvalidConfig, http.StatusBadRequest,
			map[string]any{"field": "config", "max_keys": maxPluginConfigKeys}, "too many config keys")
	}
	out := make(map[string]string, len(cfg))
	derived := make([]string, 0, len(cfg))
	for k, v := range cfg {
		kt := strings.TrimSpace(k)
		if kt == "" || len(kt) > maxPluginConfigKeyLen || strings.ContainsAny(kt, "\r\n\x00\t") {
			return nil, nil, newPluginWriteErrorWithParams(api.PluginErrInvalidConfig, http.StatusBadRequest,
				map[string]any{"field": "config", "max_key_len": maxPluginConfigKeyLen}, "config key is invalid")
		}
		if len(v) > maxPluginConfigValueLen || strings.ContainsAny(v, "\r\n\x00") {
			return nil, nil, newPluginWriteErrorWithParams(api.PluginErrInvalidConfig, http.StatusBadRequest,
				map[string]any{"field": "config", "max_value_len": maxPluginConfigValueLen}, "config value is invalid")
		}
		if werr := validateStructuredPluginConfigValue(kt, v); werr != nil {
			return nil, nil, werr
		}
		if strings.HasPrefix(v, secrethandle.Scheme) {
			h, err := secrethandle.Parse(v)
			if err != nil {
				return nil, nil, newPluginWriteErrorWithParams(api.PluginErrInvalidConfig, http.StatusBadRequest,
					map[string]any{"field": kt}, "secret handle is invalid")
			}
			derived = append(derived, h.Name())
			out[kt] = h.String()
			continue
		}
		if credentialLikeKey(kt) || urlCredentialPattern.MatchString(v) {
			return nil, nil, newPluginWriteErrorWithParams(api.PluginErrSecretForbidden, http.StatusForbidden,
				map[string]any{"field": kt}, "plaintext credentials are forbidden")
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
		return nil, nil, newPluginWriteErrorWithParams(api.PluginErrInvalidConfig, http.StatusBadRequest,
			map[string]any{"field": "secret_refs", "max_items": maxPluginSecretRefs}, "too many secret_refs")
	}
	return out, merged, nil
}

// validateStructuredPluginConfigValue rejects malformed application config at the
// write boundary. The plugin remains authoritative for business fields, but a
// value that is not even a JSON object/array must never enter desired state.
func validateStructuredPluginConfigValue(key, value string) *pluginWriteError {
	switch key {
	case appConfigKey:
		var object map[string]json.RawMessage
		if err := json.Unmarshal([]byte(value), &object); err != nil || object == nil {
			return newPluginWriteErrorWithParams(api.PluginErrInvalidConfig, http.StatusBadRequest,
				map[string]any{"field": key}, "app_config must be a JSON object")
		}
	case appBindingsKey:
		var bindings []json.RawMessage
		if err := json.Unmarshal([]byte(value), &bindings); err != nil || bindings == nil || len(bindings) > 64 {
			return newPluginWriteErrorWithParams(api.PluginErrInvalidConfig, http.StatusBadRequest,
				map[string]any{"field": key, "max_items": 64}, "app_bindings must be a JSON array with at most 64 items")
		}
	}
	return nil
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
			return nil, newPluginWriteErrorWithParams(api.PluginErrInvalidConfig, http.StatusBadRequest,
				map[string]any{"field": "secret_refs"}, "secret_refs contains an invalid handle")
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
			return newPluginWriteErrorWithParams(api.PluginErrSecretForbidden, http.StatusForbidden,
				map[string]any{"secret_ref": name, "plugin_id": pluginID}, "secret is not declared by plugin")
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
