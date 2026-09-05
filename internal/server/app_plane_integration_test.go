package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/appruntime"
	"github.com/DeliciousBuding/cloud-path/internal/auth"
	"github.com/DeliciousBuding/cloud-path/internal/store"
	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
)

func TestAppRecordsZeroLimitReportsEffectiveDefault(t *testing.T) {
	_, ts, st, _ := setupAppPlane(t)
	cookie := appPlaneCookie(t, ts, "admin")
	if err := st.UpsertAppDomainRecord(1, "sample", "reading", "latest", "{}", "1", 1700000000); err != nil {
		t.Fatal(err)
	}
	resp := doJSON(t, http.MethodGet, ts.URL+"/api/plugin-instances/sample/records?limit=0", "", nil, cookie)
	view := decodeAppPlane[api.AppDomainRecordsView](t, resp)
	if resp.StatusCode != http.StatusOK || view.Limit != 100 || len(view.Records) != 1 {
		t.Fatalf("zero limit must report the effective default: status=%d view=%+v", resp.StatusCode, view)
	}
}

// Exercise handlers with an already authenticated request: a read failure after
// authentication must not masquerade as an empty, healthy application.
func TestAppPlaneUnavailableStore(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		name := "closed"
		want := http.StatusInternalServerError
		if disabled {
			name, want = "disabled", http.StatusServiceUnavailable
		}
		t.Run(name, func(t *testing.T) {
			srv, _, st, _ := setupAppPlane(t)
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			if disabled {
				srv.cfg.Store = nil
			}
			router := chi.NewRouter()
			router.Get("/api/plugin-instances/{id}/records", srv.handlePluginInstanceRecords)
			router.Get("/api/plugin-instances/{id}/jobs", srv.handlePluginInstanceJobs)
			for _, endpoint := range []string{"records", "jobs"} {
				t.Run(endpoint, func(t *testing.T) {
					req := httptest.NewRequest(http.MethodGet, "/api/plugin-instances/sample/"+endpoint, nil)
					req = req.WithContext(auth.WithPrincipal(req.Context(), &auth.Principal{TenantID: 1, Role: "viewer"}))
					rec := httptest.NewRecorder()
					router.ServeHTTP(rec, req)
					if rec.Code != want {
						t.Fatalf("unavailable store: status=%d body=%s, want %d", rec.Code, rec.Body.String(), want)
					}
				})
			}
		})
	}
}

// Use real cookie-authenticated sockets, not injected fan-out channels. The same
// application/record key in two tenants must never cross the browser boundary.
func TestAppPlaneBrowserRecordLifecycle(t *testing.T) {
	srv, ts, st, _ := setupAppPlane(t)
	foreignID, err := st.CreateTenant("other", "Other")
	if err != nil {
		t.Fatal(err)
	}
	hash, err := auth.HashPassword("secret123")
	if err != nil {
		t.Fatal(err)
	}
	for username, tenantID := range map[string]int64{"reader": 1, "other-reader": foreignID} {
		if _, err := st.CreateUser(tenantID, username, username, "viewer", hash); err != nil {
			t.Fatal(err)
		}
	}
	ownerCookies := appPlaneCookie(t, ts, "reader")
	foreignCookies := appPlaneCookie(t, ts, "other-reader")
	connect := func(cookies []*http.Cookie) *websocket.Conn {
		t.Helper()
		req := &http.Request{Header: make(http.Header)}
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ws, _, err := websocket.Dial(ctx, wsURL(ts.URL, "/ws"), &websocket.DialOptions{HTTPHeader: req.Header})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { ws.CloseNow() })
		if _, err := readEnvUntil(ctx, ws, api.MsgSnapshot); err != nil {
			t.Fatalf("initial snapshot: %v", err)
		}
		return ws
	}
	owner := connect(ownerCookies)
	foreign := connect(foreignCookies)
	executor := &appEffectExecutor{host: &AppHost{srv: srv, logger: slog.Default()}}
	writeRecord := func(tenantID int64, value int) {
		t.Helper()
		data, err := json.Marshal(map[string]int{"value": value})
		if err != nil {
			t.Fatal(err)
		}
		effect := appruntime.Effect{
			TenantID: strconv.FormatInt(tenantID, 10), PluginInstanceID: "sample",
			Kind: appruntime.EffectCreateDomainRecord,
			CreateDomainRecord: &appruntime.CreateDomainRecord{
				RecordType: "reading", RecordID: "latest", DataJSON: string(data), Version: strconv.Itoa(value),
			},
		}
		if err := executor.Execute(context.Background(), effect); err != nil {
			t.Fatal(err)
		}
	}
	expectRecord := func(ws *websocket.Conn, value int, created bool) api.DomainRecordData {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		env, err := readEnvUntil(ctx, ws, api.MsgDomainRecord)
		if err != nil {
			t.Fatal(err)
		}
		var record api.DomainRecordData
		if err := json.Unmarshal(env.Data, &record); err != nil {
			t.Fatal(err)
		}
		var data map[string]int
		if err := json.Unmarshal([]byte(record.DataJSON), &data); err != nil {
			t.Fatal(err)
		}
		if env.Device != "" || env.Ts != record.UpdatedAt || record.InstanceID != "sample" ||
			record.RecordType != "reading" || record.RecordID != "latest" ||
			data["value"] != value || record.Version != strconv.Itoa(value) || record.Created != created {
			t.Fatalf("unexpected record projection: envelope=%+v record=%+v", env, record)
		}
		return record
	}
	expectREST := func(cookies []*http.Cookie, want api.DomainRecordData) {
		t.Helper()
		resp := doJSON(t, http.MethodGet, ts.URL+"/api/plugin-instances/sample/records", "", nil, cookies)
		view := decodeAppPlane[api.AppDomainRecordsView](t, resp)
		if resp.StatusCode != http.StatusOK || len(view.Records) != 1 {
			t.Fatalf("record read: status=%d view=%+v", resp.StatusCode, view)
		}
		row := view.Records[0]
		if row.RecordType != want.RecordType || row.RecordID != want.RecordID ||
			row.DataJSON != want.DataJSON || row.Version != want.Version || row.UpdatedAt != want.UpdatedAt {
			t.Fatalf("REST/WS disagree: REST=%+v WS=%+v", row, want)
		}
	}

	writeRecord(1, 1)
	expectRecord(owner, 1, true)
	writeRecord(foreignID, 99)
	other := expectRecord(foreign, 99, true)
	writeRecord(1, 2)
	latest := expectRecord(owner, 2, false)
	expectREST(ownerCookies, latest)
	expectREST(foreignCookies, other)

	// Viewer has the complete read plane, but cannot change desired state.
	denied := doJSON(t, http.MethodPost, ts.URL+"/api/plugin-instances/sample/reconcile", "{}", jsonHeaders(), ownerCookies)
	if denied.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer write = %d", denied.StatusCode)
	}

	// WS is a live projection, not a replay log. REST recovers the disconnected
	// interval; the next socket must resume live updates without stale state.
	owner.CloseNow()
	writeRecord(1, 3)
	owner = connect(ownerCookies)
	resp := doJSON(t, http.MethodGet, ts.URL+"/api/plugin-instances/sample/records", "", nil, ownerCookies)
	view := decodeAppPlane[api.AppDomainRecordsView](t, resp)
	if len(view.Records) != 1 || view.Records[0].Version != "3" {
		t.Fatalf("reconnect backfill = %+v", view)
	}
	writeRecord(1, 4)
	latest = expectRecord(owner, 4, false)
	expectREST(ownerCookies, latest)
}

func TestAppJobsPersistentProjectionSurvivesStop(t *testing.T) {
	srv, ts, st, _ := setupAppPlane(t)
	cookies := appPlaneCookie(t, ts, "admin")
	foreignID, err := st.CreateTenant("other", "Other")
	if err != nil {
		t.Fatal(err)
	}
	for _, tenantID := range []int64{1, foreignID} {
		if _, err := st.UpsertScheduledJob(store.ScheduledJobRow{
			TenantID: tenantID, InstanceID: "sample", ScheduleID: "heartbeat",
			Cron: "* * * * *", Timezone: "UTC", PayloadJSON: "{}", MissedPolicy: "skip", NextRunAt: 1700000060,
		}); err != nil {
			t.Fatal(err)
		}
	}
	h := &AppHost{running: map[string]*appInstanceRun{"sample": {
		row: store.PluginInstanceRow{TenantID: 1, InstanceID: "sample"}, jobIDs: []string{"minute-tick"},
	}}}
	srv.SetAppHost(h)
	get := func() api.AppJobsView {
		t.Helper()
		resp := doJSON(t, http.MethodGet, ts.URL+"/api/plugin-instances/sample/jobs", "", nil, cookies)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("jobs status = %d", resp.StatusCode)
		}
		return decodeAppPlane[api.AppJobsView](t, resp)
	}
	active := get()
	if !active.Running || len(active.Jobs) != 1 || len(active.Scheduled) != 1 || active.Scheduled[0].State != "active" {
		t.Fatalf("active projection = %+v", active)
	}
	h.mu.Lock()
	delete(h.running, "sample")
	h.mu.Unlock()
	stopped := get()
	if stopped.Running || len(stopped.Jobs) != 0 || len(stopped.Scheduled) != 1 || stopped.Scheduled[0].NextRunAt != 1700000060 {
		t.Fatalf("stopped projection must retain durable schedule without claiming execution: %+v", stopped)
	}
	if err := st.CancelScheduledJob(1, "sample", "heartbeat"); err != nil {
		t.Fatal(err)
	}
	cancelled := get()
	if len(cancelled.Scheduled) != 1 || cancelled.Scheduled[0].State != "cancelled" {
		t.Fatalf("cancelled projection = %+v", cancelled)
	}
	foreignRows, err := st.ListScheduledJobs(foreignID, "sample")
	if err != nil || len(foreignRows) != 1 || foreignRows[0].State != "active" {
		t.Fatalf("cross-tenant cancellation: rows=%+v err=%v", foreignRows, err)
	}
}
