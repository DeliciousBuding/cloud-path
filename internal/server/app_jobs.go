package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/audit"
	sdkapplication "github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/application"
	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/status"
	"github.com/go-chi/chi/v5"
)

func (run *appInstanceRun) automaticJobs() []string {
	manual := map[string]bool{}
	for _, job := range run.jobDescriptors {
		manual[job.ID] = job.ManualOnly
	}
	var out []string
	for _, id := range run.jobIDs {
		if !manual[id] {
			out = append(out, id)
		}
	}
	return out
}

// InstanceJobDescriptors is tenant-scoped and returns a detached projection.
func (h *AppHost) InstanceJobDescriptors(tenantID int64, instanceID string) []api.AppJobView {
	out := []api.AppJobView{}
	if h == nil {
		return out
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if run := h.running[appInstKey{tenantID, instanceID}]; run != nil {
		for _, job := range run.jobDescriptors {
			out = append(out, api.AppJobView{ID: job.ID, Title: job.Title, InputSchemaJSON: job.InputSchemaJSON, ManualOnly: job.ManualOnly})
		}
	}
	return out
}

// handleRunApplicationJob exposes only explicit manual jobs. Plugin validation
// remains authoritative; Core never invents job names or business arguments.
func (s *Server) handleRunApplicationJob(w http.ResponseWriter, r *http.Request) {
	instanceID, jobID := chi.URLParam(r, "id"), chi.URLParam(r, "job")
	ctx, ctxErr := s.resolvePluginWriteContext(r)
	fail := func(code string, httpStatus int, message string, params map[string]any) {
		s.writePluginErrorOnly(w, ctx, newPluginWriteErrorWithParams(code, httpStatus, params, "%s", message))
		s.auditPluginWrite(r, ctx, "application.job.run", instanceID, audit.OutcomeFailure, code, audit.NewMetadata().String("job", jobID))
	}
	if ctxErr != nil {
		fail(ctxErr.code, ctxErr.status, ctxErr.message, ctxErr.params)
		return
	}
	if s.appHost == nil {
		fail(api.APIErrApplicationUnavailable, http.StatusServiceUnavailable, "application host is not enabled", map[string]any{"instance_id": instanceID})
		return
	}
	declared := false
	for _, job := range s.appHost.InstanceJobDescriptors(ctx.tenantID, instanceID) {
		if job.ID == jobID && job.ManualOnly {
			declared = true
			break
		}
	}
	if !declared {
		fail(api.APIErrJobNotFound, http.StatusNotFound, "running application has no such manual job", map[string]any{"instance_id": instanceID, "job_id": jobID})
		return
	}
	var body api.AppJobRunRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, pluginWriteBodyLimit))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		fail(api.APIErrInvalidRequest, http.StatusBadRequest, "expected args_json and idempotency_key", map[string]any{"field": "body"})
		return
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		fail(api.APIErrInvalidRequest, http.StatusBadRequest, "expected one JSON request object", map[string]any{"field": "body"})
		return
	}
	key := body.IdempotencyKey
	if key == "" || len(key) > 128 || strings.TrimSpace(key) != key || strings.ContainsAny(key, "\r\n\x00\t") {
		fail(api.APIErrInvalidIdempotencyKey, http.StatusBadRequest, "idempotency_key must be a nonempty single-line value of at most 128 bytes", map[string]any{"field": "idempotency_key", "max_bytes": 128})
		return
	}
	if strings.TrimSpace(body.ArgsJSON) == "" {
		body.ArgsJSON = "{}"
	}
	var args map[string]json.RawMessage
	if len(body.ArgsJSON) > 4096 || json.Unmarshal([]byte(body.ArgsJSON), &args) != nil || args == nil {
		fail(api.APIErrInvalidArgs, http.StatusBadRequest, "args_json must be a JSON object of at most 4096 bytes", map[string]any{"field": "args_json", "max_bytes": 4096})
		return
	}
	if !s.allowCommand("app-job:" + ctx.tenantSlug + ":" + instanceID) {
		fail(api.APIErrRateLimited, http.StatusTooManyRequests, "too many application actions", map[string]any{"scope": "application_job", "instance_id": instanceID})
		return
	}
	callCtx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	response, err := s.appHost.rt.RunJob(callCtx, appInstKey{ctx.tenantID, instanceID}.tenantStr(), instanceID, &sdkapplication.RunJobRequest{
		PluginInstanceID: instanceID, JobID: jobID, JobType: "manual", ArgsJSON: body.ArgsJSON,
		IdempotencyKey: key, Deadline: time.Now().Add(10 * time.Second).UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		var rejection *status.Status
		if errors.As(err, &rejection) && rejection != nil && !rejection.IsOK() {
			fail(api.APIErrApplicationRejected, applicationStatusHTTP(rejection.Code), rejection.Message, map[string]any{"plugin_status": status.CodeString(rejection.Code)})
			return
		}
		fail(api.APIErrApplicationUnavailable, http.StatusServiceUnavailable, "application action did not complete; reconcile its records before retrying with the same key", map[string]any{"instance_id": instanceID})
		return
	}
	if response == nil || response.Status == nil {
		fail(api.APIErrInvalidPluginResponse, http.StatusBadGateway, "application did not return an explicit status", map[string]any{"instance_id": instanceID, "job_id": jobID})
		return
	}
	if !response.Status.IsOK() {
		httpStatus := applicationStatusHTTP(response.Status.Code)
		fail(api.APIErrApplicationRejected, httpStatus, response.Status.Message, map[string]any{"plugin_status": status.CodeString(response.Status.Code)})
		return
	}
	s.auditPluginWrite(r, ctx, "application.job.run", instanceID, audit.OutcomeSuccess, "", audit.NewMetadata().String("job", jobID))
	writeJSON(w, http.StatusOK, api.AppJobRunView{InstanceID: instanceID, JobID: jobID, ResultJSON: response.ResultJSON})
}

// Plugins may reject via an RPC status error or an explicit response status;
// both are the same contract, not a transport outage.
func applicationStatusHTTP(code status.Code) int {
	switch code {
	case status.CodeInvalidArgument, status.CodeOutOfRange:
		return http.StatusBadRequest
	case status.CodeNotFound:
		return http.StatusNotFound
	case status.CodeAlreadyExists, status.CodeFailedPrecondition, status.CodeAborted:
		return http.StatusConflict
	case status.CodeResourceExhausted:
		return http.StatusTooManyRequests
	case status.CodePermissionDenied, status.CodeUnauthenticated:
		return http.StatusForbidden
	case status.CodeUnavailable, status.CodeDeadlineExceeded:
		return http.StatusServiceUnavailable
	default:
		return http.StatusBadGateway
	}
}
