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

// 聚合读面只返回有界列表；失败计数由同一时间窗的完整匹配集聚合，不受列表上限影响。
const (
	overviewEventLimit   = 20
	overviewFailedLimit  = 20
	overviewFailedWindow = 24 * time.Hour
)

// handleOverview GET /api/overview：WebUI 首屏的一次性聚合读面。
//
// 全部字段来自真实在线态与 DB，按 principal 租户过滤；禁止任何占位/假数据。
// Store 为 nil（API-only 形态）时事件/命令列表为空、计数只反映真实内存态——
// 这仍是真实事实，不做任何编造填充。
func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	sampledAt := time.Now().Unix()
	p := auth.FromContext(r.Context())
	tenant := ""
	var tenantID int64
	var commandTenant *int64
	if p != nil {
		tenant, tenantID = p.TenantSlug, p.TenantID
		commandTenant = &tenantID
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
		ServerTime:     sampledAt,
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
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "overview unavailable"})
			return
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
		events, err := s.overviewEvents(tenantID, p)
		if err != nil {
			slog.Warn("overview: recent events unavailable", "err", err)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "overview unavailable"})
			return
		}
		failed, total, err := s.overviewFailedCommands(commandTenant, sampledAt)
		if err != nil {
			slog.Warn("overview: failed commands unavailable", "err", err)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "overview unavailable"})
			return
		}
		view.RecentEvents, view.FailedCommands, view.CommandsFailed = events, failed, total
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
	if in.EdgeID == AppHostEdgeID && in.Observed.State == "running" {
		return true
	}
	switch in.Observed.State {
	case "HEALTHY", "DEGRADED":
		return true
	}
	return in.Observed.Health == "HEALTHY"
}

// overviewEvents 取本租户最近事件（真实 DB 行）。
func (s *Server) overviewEvents(tenantID int64, p *auth.Principal) ([]api.EventView, error) {
	var rows []store.EventRow
	var err error
	if p != nil {
		rows, err = s.cfg.Store.ListEventsTenant(tenantID, "", 0, overviewEventLimit)
	} else {
		rows, err = s.cfg.Store.ListEvents("", 0, overviewEventLimit)
	}
	if err != nil {
		return nil, err
	}
	out := make([]api.EventView, 0, len(rows))
	for _, e := range rows {
		out = append(out, api.EventView{ID: e.ID, DeviceID: e.DeviceID, Ts: e.Ts,
			Type: e.Type, Payload: e.Payload})
	}
	return out, nil
}

// overviewFailedCommands 取本租户滚动近24小时的 failed/timeout 命令。
// 以 server_time 为同一次采样的上界，两端都包含；失败时间优先 acked_at，缺失才回退 created_at。
func (s *Server) overviewFailedCommands(tenantID *int64, sampledAt int64) ([]api.CommandView, int, error) {
	since := sampledAt - int64(overviewFailedWindow/time.Second)
	rows, total, err := s.cfg.Store.FailedCommandsWindow(tenantID, since, sampledAt, overviewFailedLimit)
	if err != nil {
		return nil, 0, err
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
	return out, total, nil
}
