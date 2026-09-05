package server

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/auth"
	"github.com/DeliciousBuding/cloud-path/internal/plugincatalog"
	"github.com/DeliciousBuding/cloud-path/internal/store"
)

// 聚合读面的长度约束：列表只回最近若干条（一次拉全量会拖垮首屏），
// 计数在 overviewCountLimit 内为真实值（保留期清理保证该窗口足够覆盖）。
const (
	overviewEventLimit  = 20
	overviewFailedLimit = 20
	overviewCountLimit  = 1000
)

// handleOverview GET /api/overview：WebUI 首屏的一次性聚合读面。
//
// 全部字段来自真实在线态与 DB，按 principal 租户过滤；禁止任何占位/假数据。
// Store 为 nil（API-only 形态）时事件/命令列表为空、计数只反映真实内存态——
// 这仍是真实事实，不做任何编造填充。
func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	tenant := ""
	var tenantID int64
	if p != nil {
		tenant, tenantID = p.TenantSlug, p.TenantID
	}
	s.primePluginTenant(r)

	s.mu.RLock()
	devices := s.deviceViewsFor(tenant)
	edges := s.edgeViewsFor(tenant)
	s.mu.RUnlock()

	view := api.OverviewView{
		DevicesTotal:   len(devices),
		EdgesTotal:     len(edges),
		RecentEvents:   []api.EventView{},
		OfflineDevices: []api.DeviceView{},
		FailedCommands: []api.CommandView{},
		ServerTime:     time.Now().Unix(),
	}
	for _, d := range devices {
		if d.Online {
			view.DevicesOnline++
			continue
		}
		view.OfflineDevices = append(view.OfflineDevices, d)
	}
	for _, e := range edges {
		if e.Online {
			view.EdgesOnline++
		}
	}

	// 插件计数：PluginsDesired = 期望**启用**的实例数（Server 权威期望态）；
	// PluginsActive = 运行宿主实际运行、投影未过期的实例数。
	// 「期望启用」绝不计入 active（不变量 5：desired≠observed）。
	if s.plugin.enabled() {
		instances, err := plugincatalog.InstanceViews(pluginProjection{s}, tenant)
		if err != nil {
			slog.Warn("overview: plugin instances unavailable", "err", err)
		} else {
			for _, in := range instances {
				if in.Desired.Enabled {
					view.PluginsDesired++
				}
				if in.HasObserved && !in.Stale && pluginObservedActive(in) {
					view.PluginsActive++
				}
			}
		}
	}

	if s.cfg.Store != nil {
		view.RecentEvents = s.overviewEvents(tenantID, p)
		failed, total := s.overviewFailedCommands(tenantID, p)
		view.FailedCommands, view.CommandsFailed = failed, total
	}
	writeJSON(w, http.StatusOK, view)
}

// pluginObservedActive 判定一条 observed 投影是否算「活跃」：实例已在运行
// （Edge pluginhost 状态 HEALTHY/DEGRADED、健康探测为 HEALTHY，或中心 AppHost 状态 running）才算；
// STOPPED/CRASHED/BACKOFF/DISABLED/STARTING 与「无上报」一律不计入，
// 绝不因为 desired enabled 就算活跃。
func pluginObservedActive(in api.PluginInstanceView) bool {
	if in.Observed == nil {
		return false
	}
	// AppHost 的本地运行投影使用 running，不伪造 Edge 的健康探测结果。
	if in.EdgeID == "server" && in.Observed.State == "running" {
		return true
	}
	switch in.Observed.State {
	case "HEALTHY", "DEGRADED":
		return true
	}
	return in.Observed.Health == "HEALTHY"
}

// overviewEvents 取本租户最近事件（真实 DB 行）。
func (s *Server) overviewEvents(tenantID int64, p *auth.Principal) []api.EventView {
	var rows []store.EventRow
	var err error
	if p != nil {
		rows, err = s.cfg.Store.ListEventsTenant(tenantID, "", 0, overviewEventLimit)
	} else {
		rows, err = s.cfg.Store.ListEvents("", 0, overviewEventLimit)
	}
	if err != nil {
		return []api.EventView{}
	}
	out := make([]api.EventView, 0, len(rows))
	for _, e := range rows {
		out = append(out, api.EventView{ID: e.ID, DeviceID: e.DeviceID, Ts: e.Ts,
			Type: e.Type, Payload: e.Payload})
	}
	return out
}

// overviewFailedCommands 取本租户 failed/timeout 命令：列表用于 UI，计数用于卡片。
func (s *Server) overviewFailedCommands(tenantID int64, p *auth.Principal) ([]api.CommandView, int) {
	list := func(status string, limit int) []store.CommandRow {
		var rows []store.CommandRow
		var err error
		if p != nil {
			rows, err = s.cfg.Store.ListCommandsTenant(tenantID, "", status, limit)
		} else {
			rows, err = s.cfg.Store.ListCommands("", status, limit)
		}
		if err != nil {
			return nil
		}
		return rows
	}
	failed := list("failed", overviewCountLimit)
	timeout := list("timeout", overviewCountLimit)
	out := make([]api.CommandView, 0, len(failed)+len(timeout))
	appendRows := func(rows []store.CommandRow) {
		for _, c := range rows {
			cv := api.CommandView{ID: c.ID, DeviceID: c.DeviceID, Cmd: c.Cmd, Args: c.Args,
				Status: c.Status, CreatedAt: c.CreatedAt, Result: c.Result}
			if c.AckedAt.Valid {
				cv.AckedAt = c.AckedAt.Int64
			}
			out = append(out, cv)
		}
	}
	appendRows(capped(failed, overviewFailedLimit))
	appendRows(capped(timeout, overviewFailedLimit))
	return out, len(failed) + len(timeout)
}

// capped 裁剪列表到响应上限（计数仍用完整长度）。
func capped[T any](in []T, max int) []T {
	if len(in) > max {
		return in[:max]
	}
	return in
}
