package server

import (
	"encoding/json"
	"log/slog"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/audit"
	"github.com/DeliciousBuding/cloud-path/internal/plugincatalog"
)

// 本文件是插件控制面的 WS 上下行接缝（docs/architecture/control-plane-sync.md §4）。
//
// 身份不变量：tenant/edge 身份**只来自已鉴权的 edgeLink**，绝不信任 payload 自报；
// api.PluginStatusData / PluginAckData 也不携带任何身份字段，从结构上排除伪造。
//
// 消息幂等：同 boot_id 下 sequence 必须单调，重复/倒序忽略；新 boot_id 可从 1 开始；
// 旧 boot 的迟到消息（无论走旧连接还是混进当前连接）一律忽略。

// currentEdgeLink 返回该 edge 当前注册的连接；不是当前连接的 link 一律视为失效。
// 这是「旧连接迟到消息不得写入投影」的传输层闸门（暗卷 2）。
func (s *Server) currentEdgeLink(edgeID string) *edgeLink {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.edges[edgeID]
}

// sendPluginDesiredToLink 向刚注册成功的连接下发当前完整期望态快照。
// hello 成功后必须调用：Edge 重连只收敛这一份最终快照，不回放离线期间的中间副作用。
func (s *Server) sendPluginDesiredToLink(link *edgeLink) bool {
	if link == nil || !s.plugin.enabled() {
		return false
	}
	snap, ok := s.plugin.desiredSnapshot(link.tenantID, link.edgeID)
	if !ok {
		return false
	}
	return s.writePluginEnvelope(link, api.MsgPluginDesired, snap)
}

// pushPluginDesired 在期望态变更后通知在线 Edge；离线 Edge 在下次重连时取完整快照。
// 返回是否真的投递到了在线连接（离线不算失败：desired 已是权威事实）。
func (s *Server) pushPluginDesired(tenantID int64, edgeID string) bool {
	if !s.plugin.enabled() {
		return false
	}
	s.mu.RLock()
	link := s.edges[edgeID]
	ok := link != nil && link.tenantID == tenantID
	s.mu.RUnlock()
	if !ok {
		return false
	}
	return s.sendPluginDesiredToLink(link)
}

// edgeOnlineFor 报告 (tenant, edge) 是否有在线连接（写 API 的 reconcile 判定用）。
func (s *Server) edgeOnlineFor(tenantID int64, edgeID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	l := s.edges[edgeID]
	return l != nil && l.tenantID == tenantID
}

// writePluginEnvelope 非阻塞投递插件控制面消息。信封不带 Device：
// plugin_* 是 tenant/edge 级消息，身份由已鉴权连接隐含，避免被误解析成设备键。
func (s *Server) writePluginEnvelope(link *edgeLink, typ api.MsgType, data any) bool {
	raw, err := json.Marshal(data)
	if err != nil {
		slog.Warn("plugin: marshal envelope", "err", err, "type", typ, "edge", link.edgeID)
		return false
	}
	env := api.Envelope{V: api.Version, Type: typ, Ts: time.Now().Unix(), Data: raw}
	payload, err := json.Marshal(env)
	if err != nil {
		slog.Warn("plugin: marshal message", "err", err, "type", typ, "edge", link.edgeID)
		return false
	}
	select {
	case link.send <- payload:
		return true
	default:
		slog.Warn("plugin: edge send queue full, dropping control message",
			"type", typ, "edge", link.edgeID, "tenant_id", link.tenantID)
		return false
	}
}

// handlePluginStatusMsg 解析并应用 Edge 的 plugin_status 全量实际态快照。
// 解析失败/被忽略都**不断开连接**（向后兼容 + 幂等语义）。
func (s *Server) handlePluginStatusMsg(link *edgeLink, msg *api.Envelope) {
	var data api.PluginStatusData
	if err := json.Unmarshal(msg.Data, &data); err != nil {
		slog.Warn("plugin_status: bad payload", "err", err, "edge", link.edgeID)
		return
	}
	accepted, reason := s.applyPluginStatus(link, data)
	if !accepted {
		slog.Debug("plugin_status ignored", "reason", reason, "edge", link.edgeID,
			"tenant_id", link.tenantID, "boot_id", data.BootID, "sequence", data.Sequence)
		return
	}
	slog.Info("plugin_status applied", "edge", link.edgeID, "tenant_id", link.tenantID,
		"boot_id", data.BootID, "sequence", data.Sequence,
		"installations", len(data.Installations), "instances", len(data.ObservedInstances))
}

// applyPluginStatus 是 plugin_status 的幂等应用核心。
// 返回 (accepted, reason)；reason 只含稳定机器码，不含任何配置值或 secret。
func (s *Server) applyPluginStatus(link *edgeLink, data api.PluginStatusData) (bool, string) {
	p := s.plugin
	if !p.enabled() {
		return false, "plugin_store_unavailable"
	}
	if data.BootID == "" {
		return false, "missing_boot_id"
	}
	if s.currentEdgeLink(link.edgeID) != link {
		return false, "stale_link"
	}
	now := p.now().Unix()

	p.mu.Lock()
	defer p.mu.Unlock()
	t, err := p.ensureLoadedLocked(link.tenantID, link.tenant)
	if err != nil || t == nil {
		slog.Warn("plugin_status: load tenant plane", "err", err, "tenant_id", link.tenantID)
		return false, "store_error"
	}
	ep := p.edgePlaneLocked(t, link.edgeID)

	switch {
	case link.pluginBootID == "":
		// 本连接首次上报：确立 boot。同 boot 的重复/倒序仍要挡掉
		// （WS 只重连不换进程时 Edge 会延续同一 boot_id 与 sequence）。
		if ep.bootID == data.BootID && data.Sequence <= ep.lastSequence {
			return false, "stale_sequence"
		}
		link.pluginBootID = data.BootID
	case link.pluginBootID != data.BootID:
		// 一个进程只有一个 boot id：当前连接上出现别的 boot id = 旧 boot 迟到消息。
		return false, "stale_boot"
	case data.Sequence <= ep.lastSequence:
		return false, "stale_sequence"
	}

	ep.bootID = data.BootID
	ep.lastSequence = data.Sequence
	ep.lastReportAt = now
	ep.observed = make(map[string]api.PluginObservedInstanceData, len(data.ObservedInstances))
	for _, o := range data.ObservedInstances {
		if o.InstanceID == "" {
			continue
		}
		o.Detail = plugincatalog.SanitizeDetail(o.Detail)
		ep.observed[o.InstanceID] = o
	}
	ep.installations = make(map[string]api.PluginInstallationStatusData, len(data.Installations))
	for _, in := range data.Installations {
		if in.PluginID == "" {
			continue
		}
		ep.installations[in.PluginID] = in
	}

	// applied_revision 只由 plugin_ack(applied) 推进；status 里的自报值只用于
	// 发现「Edge 声称已应用但从未 ack」这类协议异常（不变量：desired≠observed）。
	if data.AppliedRevision > ep.appliedRevision {
		slog.Debug("plugin_status reports applied revision ahead of any ack",
			"edge", link.edgeID, "tenant_id", link.tenantID,
			"reported", data.AppliedRevision, "acked", ep.appliedRevision)
	}

	if err := p.store.SetPluginEdgeReport(link.tenantID, link.edgeID, data.BootID, data.Sequence, now); err != nil {
		slog.Warn("plugin_status: persist report", "err", err, "edge", link.edgeID)
	}
	if err := p.store.UpsertPluginObservations(link.tenantID, link.edgeID, data.ObservedInstances, now); err != nil {
		slog.Warn("plugin_status: persist observations", "err", err, "edge", link.edgeID)
	}
	if err := p.store.UpsertPluginInstallations(link.tenantID, link.edgeID, data.Installations); err != nil {
		slog.Warn("plugin_status: persist installations", "err", err, "edge", link.edgeID)
	}
	return true, "accepted"
}

// handlePluginAckMsg 解析并应用 Edge 的 plugin_ack。
func (s *Server) handlePluginAckMsg(link *edgeLink, msg *api.Envelope) {
	var data api.PluginAckData
	if err := json.Unmarshal(msg.Data, &data); err != nil {
		slog.Warn("plugin_ack: bad payload", "err", err, "edge", link.edgeID)
		return
	}
	advanced, reason := s.applyPluginAck(link, data)
	slog.Info("plugin_ack processed", "edge", link.edgeID, "tenant_id", link.tenantID,
		"revision", data.Revision, "status", data.Status, "advanced", advanced, "reason", reason)
}

// applyPluginAck 是 plugin_ack 的应用核心：
// 只有 status=applied 才推进 applied_revision；rejected/failed 保持上一完整 revision，
// 并逐实例记录结果与失败审计（audit 必须记录真实失败，不得静默丢弃）。
func (s *Server) applyPluginAck(link *edgeLink, data api.PluginAckData) (bool, string) {
	p := s.plugin
	if !p.enabled() {
		return false, "plugin_store_unavailable"
	}
	if s.currentEdgeLink(link.edgeID) != link {
		return false, "stale_link"
	}
	switch data.Status {
	case api.PluginAckApplied, api.PluginAckRejected, api.PluginAckFailed:
	default:
		return false, "bad_status"
	}
	now := p.now().Unix()

	p.mu.Lock()
	defer p.mu.Unlock()
	t, err := p.ensureLoadedLocked(link.tenantID, link.tenant)
	if err != nil || t == nil {
		return false, "store_error"
	}
	ep := p.edgePlaneLocked(t, link.edgeID)
	bootID := link.pluginBootID
	if bootID == "" {
		bootID = ep.bootID
	}
	results := sanitizeApplyResults(data.Results)

	if data.Revision > ep.desiredRevision {
		s.auditPluginProtocol(link, "unknown_revision", data.Revision)
		return false, "unknown_revision"
	}
	if data.Status != api.PluginAckApplied {
		ep.lastAckAt, ep.lastAckStatus, ep.lastResults = now, data.Status, results
		if err := p.store.SetPluginEdgeApplied(link.tenantID, link.edgeID, bootID,
			ep.lastSequence, ep.appliedRevision, now); err != nil {
			slog.Warn("plugin_ack: persist rejection", "err", err, "edge", link.edgeID)
		}
		s.auditPluginAckFailure(link, data, results)
		return false, data.Status
	}
	// 同 revision 不同 payload：协议异常，拒绝推进（control-plane-sync §2 不变量 3）。
	if want := p.desiredSnapshotLocked(t, link.edgeID).SnapshotDigest; data.SnapshotDigest != "" &&
		data.Revision == ep.desiredRevision && data.SnapshotDigest != want {
		s.auditPluginProtocol(link, "digest_mismatch", data.Revision)
		return false, "digest_mismatch"
	}
	if data.Revision <= ep.appliedRevision {
		ep.lastAckAt, ep.lastAckStatus, ep.lastResults = now, data.Status, results
		return false, "duplicate_ack"
	}
	ep.appliedRevision = data.Revision
	ep.lastAckAt, ep.lastAckStatus, ep.lastResults = now, data.Status, results
	if err := p.store.SetPluginEdgeApplied(link.tenantID, link.edgeID, bootID,
		ep.lastSequence, data.Revision, now); err != nil {
		slog.Warn("plugin_ack: persist applied", "err", err, "edge", link.edgeID)
	}
	s.recordAudit(audit.Event{
		TenantID: link.tenantID, ActorType: audit.ActorEdge, ActorName: link.edgeID,
		Action: actionPluginApplied, TargetType: targetPluginEdge, TargetID: link.edgeID,
		Outcome: audit.OutcomeSuccess,
		Metadata: audit.NewMetadata().
			Int("revision", int64(data.Revision)).
			Int("results", int64(len(results))).
			String("boot_id", bootID).Map(),
	})
	return true, "applied"
}

// auditPluginAckFailure 记录 rejected/failed 的真实失败（逐实例结果，脱敏后有界）。
func (s *Server) auditPluginAckFailure(link *edgeLink, data api.PluginAckData, results []api.PluginApplyResultData) {
	raw, err := json.Marshal(results)
	detail := "[]"
	if err == nil {
		detail = string(raw)
	}
	s.recordAudit(audit.Event{
		TenantID: link.tenantID, ActorType: audit.ActorEdge, ActorName: link.edgeID,
		Action: actionPluginRejected, TargetType: targetPluginEdge, TargetID: link.edgeID,
		Outcome: audit.OutcomeFailure,
		Metadata: audit.NewMetadata().
			Int("revision", int64(data.Revision)).
			String("status", data.Status).
			String("results", detail).Map(),
	})
}

// auditPluginProtocol 记录协议异常（未知 revision / 同 revision 不同摘要）。
func (s *Server) auditPluginProtocol(link *edgeLink, reason string, revision uint64) {
	s.recordAudit(audit.Event{
		TenantID: link.tenantID, ActorType: audit.ActorEdge, ActorName: link.edgeID,
		Action: actionPluginProtocol, TargetType: targetPluginEdge, TargetID: link.edgeID,
		Outcome:  audit.OutcomeFailure,
		Metadata: audit.NewMetadata().String("reason", reason).Int("revision", int64(revision)).Map(),
	})
	slog.Warn("plugin protocol anomaly", "reason", reason, "edge", link.edgeID,
		"tenant_id", link.tenantID, "revision", revision)
}

// maxAckResults 限制单次 ack 保留/审计的逐实例结果条数。
const maxAckResults = 32

// sanitizeApplyResults 对逐实例结果做长度与敏感信息脱敏（Detail 可能来自插件错误）。
func sanitizeApplyResults(in []api.PluginApplyResultData) []api.PluginApplyResultData {
	if len(in) == 0 {
		return nil
	}
	if len(in) > maxAckResults {
		in = in[:maxAckResults]
	}
	out := make([]api.PluginApplyResultData, 0, len(in))
	for _, r := range in {
		out = append(out, api.PluginApplyResultData{
			InstanceID: r.InstanceID, Status: r.Status,
			Detail: plugincatalog.SanitizeDetail(r.Detail),
		})
	}
	return out
}
