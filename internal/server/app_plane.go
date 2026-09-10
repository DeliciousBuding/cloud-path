package server

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/application"
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
	if s.cfg.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store unavailable"})
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

	// The response must report the same effective limit used by the store.
	if limit == 0 {
		limit = 100
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
	view, _ := s.appHost.InstanceBindings(tenantID, instanceID)
	if !view.Running {
		view = s.fallbackBindingOptions(tenantID, instanceID)
	}
	if view.Bindings == nil {
		view.Bindings = []api.AppBindingView{}
	}
	if view.Requirements == nil {
		view.Requirements = []api.AppBindingRequirementView{}
	}
	if view.Candidates == nil {
		view.Candidates = []api.AppBindingCandidateView{}
	}
	writeJSON(w, http.StatusOK, view)
}

// fallbackBindingOptions keeps the target editor usable while an application is
// stopped or failed. It derives only requirement ids/capabilities from the saved
// explicit binding and current device descriptors; it never pretends the
// instance is running.
func (s *Server) fallbackBindingOptions(tenantID int64, instanceID string) api.AppBindingsView {
	view := api.AppBindingsView{InstanceID: instanceID, Running: false}
	if s.cfg.Store == nil {
		return view
	}
	row, ok, err := s.cfg.Store.GetPluginInstance(tenantID, AppHostEdgeID, instanceID)
	if err != nil || !ok {
		return view
	}
	var config map[string]string
	if json.Unmarshal([]byte(row.ConfigJSON), &config) != nil || config[appBindingsKey] == "" {
		return view
	}
	var saved []application.Binding
	if json.Unmarshal([]byte(config[appBindingsKey]), &saved) != nil || len(saved) == 0 {
		return view
	}
	candidates := s.appCandidates(tenantID)
	reqCap := map[string]string{}
	for _, binding := range saved {
		capability := ""
		for _, candidate := range candidates {
			if candidate.EntityID != binding.EntityID {
				continue
			}
			if binding.DeviceID != "" && candidate.DeviceID != binding.DeviceID {
				continue
			}
			if len(candidate.Capabilities) == 1 {
				capability = candidate.Capabilities[0]
				break
			}
		}
		if capability == "" {
			continue
		}
		reqCap[binding.RequirementID] = capability
		deviceID := binding.DeviceID
		if deviceID == "" {
			matches := 0
			for _, candidate := range candidates {
				if candidate.EntityID != binding.EntityID || !containsString(candidate.Capabilities, capability) {
					continue
				}
				deviceID = candidate.DeviceID
				matches++
			}
			if matches != 1 {
				deviceID = ""
			}
		}
		view.Bindings = append(view.Bindings, api.AppBindingView{
			RequirementID: binding.RequirementID, Capability: capability,
			EntityID: binding.EntityID, DeviceID: deviceID,
		})
	}
	counts := map[string]int{}
	for _, binding := range view.Bindings {
		counts[binding.RequirementID]++
	}
	requirementIDs := make([]string, 0, len(reqCap))
	for requirementID := range reqCap {
		requirementIDs = append(requirementIDs, requirementID)
	}
	sort.Strings(requirementIDs)
	for _, requirementID := range requirementIDs {
		cardinality := "one"
		minItems := 0
		if counts[requirementID] > 1 {
			cardinality = "one-or-more"
			minItems = counts[requirementID]
		}
		view.Requirements = append(view.Requirements, api.AppBindingRequirementView{
			ID: requirementID, Capability: reqCap[requirementID], Cardinality: cardinality, MinItems: minItems,
		})
	}
	for _, candidate := range candidates {
		for _, capability := range candidate.Capabilities {
			if !hasCapability(stringMapValues(reqCap), capability) {
				continue
			}
			view.Candidates = append(view.Candidates, api.AppBindingCandidateView{
				EntityID: candidate.EntityID, DeviceID: candidate.DeviceID, Name: candidate.Name,
				Capabilities: append([]string(nil), candidate.Capabilities...),
			})
			break
		}
	}
	return view
}

func stringMapValues(in map[string]string) []string {
	out := make([]string, 0, len(in))
	for _, value := range in {
		out = append(out, value)
	}
	return out
}

func hasCapability(capabilities []string, want string) bool {
	return containsString(capabilities, want)
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// handlePluginInstanceJobs GET /api/plugin-instances/{id}/jobs
func (s *Server) handlePluginInstanceJobs(w http.ResponseWriter, r *http.Request) {
	instanceID := chi.URLParam(r, "id")
	tenantID, ok := appPlaneTenant(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	if s.cfg.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store unavailable"})
		return
	}
	rows, err := s.cfg.Store.ListScheduledJobs(tenantID, instanceID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "store unavailable"})
		return
	}
	jobs, running := s.appHost.InstanceJobs(tenantID, instanceID)
	if jobs == nil {
		jobs = []string{}
	}
	scheduled := make([]api.AppScheduledJobView, 0, len(rows))
	for _, row := range rows {
		scheduled = append(scheduled, api.AppScheduledJobView{
			ScheduleID:   row.ScheduleID,
			Cron:         row.Cron,
			Timezone:     row.Timezone,
			MissedPolicy: row.MissedPolicy,
			NextRunAt:    row.NextRunAt,
			LastRunAt:    row.LastRunAt,
			State:        row.State,
			Revision:     row.Revision,
		})
	}
	writeJSON(w, http.StatusOK, api.AppJobsView{
		InstanceID: instanceID, Running: running, Jobs: jobs, Scheduled: scheduled,
		JobDescriptors: s.appHost.InstanceJobDescriptors(tenantID, instanceID),
	})
}
