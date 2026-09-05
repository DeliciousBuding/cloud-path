package server

import (
	"net/http"
	"strconv"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/auth"

	"github.com/go-chi/chi/v5"
)

// Application Data Plane（Milestone D1）：设备无关、业务无关的应用读面。
//
// 设计约束（ROADMAP.md Gate D1）：
//   - app_domain_records 是 Application 数据 SSOT，这里只读不写、不建第二套 store；
//   - 不创建任何业务特例 API（无 /api/pillbox/*）；
//   - 租户隔离：记录查询按 (tenant, instance) 复合键过滤，跨租户实例与
//     「实例不存在」同形返回空列表——探测得不到存在性信息；
//   - bindings/jobs 是运行态投影，只在实例运行期间存在（AppHost 未启用或
//     实例未运行 → running=false + 空数组，不伪造持久态）。

// appPlaneTenant 从已鉴权请求解出租户 id。读面全部要求账号身份（路由挂在
// requireAPIAuth + viewer 组内），此处失败只剩理论可能。
func appPlaneTenant(r *http.Request) (int64, bool) {
	p := auth.FromContext(r.Context())
	if p == nil || p.TenantID <= 0 {
		return 0, false
	}
	return p.TenantID, true
}

// handlePluginInstanceRecords GET /api/plugin-instances/{id}/records
// 查询参数：record_type（可选过滤）、limit（默认 100，上限 1000）、offset（默认 0）。
// limit/offset 非法值一律 400（显式拒绝比静默钳制更可诊断）。
func (s *Server) handlePluginInstanceRecords(w http.ResponseWriter, r *http.Request) {
	instanceID := chi.URLParam(r, "id")
	tenantID, ok := appPlaneTenant(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	q := r.URL.Query()

	recordType := q.Get("record_type")
	if recordType != "" && !validPluginSegment(recordType, 64) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid record_type"})
		return
	}

	parseNonNeg := func(name string) (int, bool) {
		raw := q.Get(name)
		if raw == "" {
			if name == "limit" {
				return 100, true
			}
			return 0, true
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return 0, false
		}
		return n, true
	}
	limit, okLimit := parseNonNeg("limit")
	offset, okOffset := parseNonNeg("offset")
	if !okLimit || !okOffset || limit > 1000 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid limit/offset"})
		return
	}

	rows, err := s.cfg.Store.ListAppDomainRecordsFiltered(tenantID, instanceID, recordType, limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "store unavailable"})
		return
	}
	view := api.AppDomainRecordsView{
		InstanceID: instanceID,
		Records:    make([]api.AppDomainRecordView, 0, len(rows)),
		RecordType: recordType,
		Limit:      limit,
		Offset:     offset,
	}
	for _, row := range rows {
		view.Records = append(view.Records, api.AppDomainRecordView{
			RecordType: row.RecordType,
			RecordID:   row.RecordID,
			DataJSON:   row.DataJSON,
			Version:    row.Version,
			UpdatedAt:  row.UpdatedAt,
		})
	}
	writeJSON(w, http.StatusOK, view)
}

// handlePluginInstanceBindings GET /api/plugin-instances/{id}/bindings
func (s *Server) handlePluginInstanceBindings(w http.ResponseWriter, r *http.Request) {
	instanceID := chi.URLParam(r, "id")
	tenantID, ok := appPlaneTenant(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	bindings, running := s.appHost.InstanceBindings(tenantID, instanceID)
	if bindings == nil {
		bindings = []api.AppBindingView{}
	}
	writeJSON(w, http.StatusOK, api.AppBindingsView{
		InstanceID: instanceID, Running: running, Bindings: bindings,
	})
}

// handlePluginInstanceJobs GET /api/plugin-instances/{id}/jobs
func (s *Server) handlePluginInstanceJobs(w http.ResponseWriter, r *http.Request) {
	instanceID := chi.URLParam(r, "id")
	tenantID, ok := appPlaneTenant(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	jobs, running := s.appHost.InstanceJobs(tenantID, instanceID)
	if jobs == nil {
		jobs = []string{}
	}
	writeJSON(w, http.StatusOK, api.AppJobsView{
		InstanceID: instanceID, Running: running, Jobs: jobs,
	})
}
