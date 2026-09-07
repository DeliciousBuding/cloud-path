package server

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/auth"
	"github.com/DeliciousBuding/cloud-path/internal/model"
	"github.com/DeliciousBuding/cloud-path/internal/pluginhost"
	"github.com/DeliciousBuding/cloud-path/internal/store"
	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/application"
)

func startActionFixture(t *testing.T, srv *Server) (*AppHost, application.ApplicationClient) {
	t.Helper()
	paths := versionedFixtureBinaries(t, "0.1.0")
	h, err := NewAppHost(srv, AppHostConfig{Enabled: true, PluginsDir: t.TempDir(), LockPath: filepath.Join(t.TempDir(), "plugins.lock"), StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	srv.SetAppHost(h)
	if err := h.mgr.RegisterInstallation(pluginhost.Installation{PluginID: appRoutingPlugin, Version: "0.1.0", Path: paths["0.1.0"], Kind: pluginhost.KindApplication}); err != nil {
		t.Fatal(err)
	}
	if err := h.mgr.ReconcileInstance(context.Background(), pluginhost.InstanceSpec{Tenant: "1", ID: "sample", PluginID: appRoutingPlugin, Version: "0.1.0"}, true); err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(map[string]string{appConfigKey: `{"target":"sample","version":"0.1.0"}`})
	if err := h.startInstance(context.Background(), store.PluginInstanceRow{TenantID: 1, EdgeID: AppHostEdgeID, InstanceID: "sample", PluginID: appRoutingPlugin, Version: "0.1.0", Enabled: true, ConfigJSON: string(config), Revision: 1}); err != nil {
		t.Fatal(err)
	}
	cli, err := h.mgr.ApplicationClientForInstance("1", "sample")
	if err != nil {
		t.Fatal(err)
	}
	return h, cli
}

func fixtureReport(t *testing.T, cli application.ApplicationClient) appRoutingReport {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	health, err := cli.Health(ctx)
	if err != nil || health == nil || len(health.Instances) != 1 {
		t.Fatalf("fixture health=%+v err=%v", health, err)
	}
	var report appRoutingReport
	if err := json.Unmarshal([]byte(health.Instances[0].Detail), &report); err != nil {
		t.Fatal(err)
	}
	return report
}

func TestManualApplicationActionsAreExplicitScopedAndHonest(t *testing.T) {
	srv, ts, st, _ := setupAppPlane(t)
	h, cli := startActionFixture(t, srv)
	admin := appPlaneCookie(t, ts, "admin")
	hash, _ := auth.HashPassword("secret123")
	if _, err := st.CreateUser(1, "reader", "Reader", "viewer", hash); err != nil {
		t.Fatal(err)
	}
	reader := appPlaneCookie(t, ts, "reader")
	other, err := st.CreateTenant("another", "Another")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUser(other, "other", "Other", "admin", hash); err != nil {
		t.Fatal(err)
	}
	otherCookie := appPlaneCookie(t, ts, "other")
	valid := `{"args_json":"{}","idempotency_key":"operator-key"}`
	path := ts.URL + "/api/plugin-instances/sample/jobs/manual-update/run"
	for _, tc := range []struct {
		name, url, body string
		cookies         []*http.Cookie
		want            int
	}{
		{"anonymous", path, valid, nil, 401},
		{"viewer", path, valid, reader, 403},
		{"other tenant", path, valid, otherCookie, 404},
		{"unknown action", strings.Replace(path, "manual-update", "missing", 1), valid, admin, 404},
		{"background job", strings.Replace(path, "manual-update", "periodic-audit", 1), valid, admin, 404},
		{"no key", path, `{"args_json":"{}"}`, admin, 400},
		{"trailing request", path, valid + " {}", admin, 400},
		{"array arguments", path, `{"args_json":"[]","idempotency_key":"x"}`, admin, 400},
		{"plugin rejection", path, `{"args_json":"{\"reject\":true}","idempotency_key":"reject"}`, admin, 400},
		{"RPC status rejection", path, `{"args_json":"{\"rpc_reject\":true}","idempotency_key":"rpc-reject"}`, admin, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := doJSON(t, http.MethodPost, tc.url, tc.body, jsonHeaders(), tc.cookies)
			defer response.Body.Close()
			if response.StatusCode != tc.want {
				t.Fatalf("status=%d want=%d", response.StatusCode, tc.want)
			}
		})
	}
	if got := fixtureReport(t, cli).Jobs["sample"]; len(got) != 0 {
		t.Fatalf("rejected actions ran: %+v", got)
	}
	response := doJSON(t, http.MethodPost, path, valid, jsonHeaders(), admin)
	if response.StatusCode != 200 {
		t.Fatalf("valid action status=%d", response.StatusCode)
	}
	result := decodeAppPlane[api.AppJobRunView](t, response)
	response.Body.Close()
	if result.JobID != "manual-update" || !strings.Contains(result.ResultJSON, "operator-key") {
		t.Fatalf("result=%+v", result)
	}
	listed := doJSON(t, http.MethodGet, ts.URL+"/api/plugin-instances/sample/jobs", "", nil, admin)
	jobs := decodeAppPlane[api.AppJobsView](t, listed)
	listed.Body.Close()
	if len(jobs.JobDescriptors) != 2 || !jobs.JobDescriptors[0].ManualOnly {
		t.Fatalf("declarations=%+v", jobs)
	}
	// A real minute pass may run periodic jobs, never an operator action.
	h.minutePass(time.Now().UTC().Truncate(time.Minute))
	seen := fixtureReport(t, cli).Jobs["sample"]
	manual, background := 0, 0
	for _, job := range seen {
		if job.JobID == "manual-update" {
			manual++
			if job.JobType != "manual" || job.IdempotencyKey != "operator-key" {
				t.Fatalf("wrong invocation=%+v", job)
			}
		}
		if job.JobID == "periodic-audit" {
			background++
		}
	}
	if manual != 1 || background != 1 {
		t.Fatalf("manual=%d background=%d", manual, background)
	}
	h.mu.Lock()
	delete(h.running, appInstKey{1, "sample"})
	h.mu.Unlock()
	stopped := doJSON(t, http.MethodPost, path, valid, jsonHeaders(), admin)
	defer stopped.Body.Close()
	if stopped.StatusCode != 404 {
		t.Fatalf("stopped action status=%d", stopped.StatusCode)
	}
	if !auditContains(t, st, 1, "application.job.run") {
		t.Fatal("missing action audit")
	}
}

func TestWSPropertySampleReachesBoundApplicationProcess(t *testing.T) {
	srv, ts := setup(t)
	edge := registerEdge(t, ts, "sample-edge", api.DeviceMeta{ID: "sensor", Adapter: "test"})
	desc := model.Descriptor{DeviceID: "sample-edge/sensor", ExternalID: "sensor", Status: model.DeviceOnline, Entities: []model.Entity{{EntityID: "sample-entity", UniqueKey: "sample", Category: model.EntitySensor, Capabilities: []string{"example.test/capability/counter@1"}}}}
	writeEnv(t, edge, api.Envelope{V: api.Version, Type: api.MsgDescriptor, Device: desc.DeviceID, Data: rawData(t, desc)})
	deadline := time.Now().Add(3 * time.Second)
	for {
		srv.mu.RLock()
		_, ok := srv.descriptors[desc.DeviceID]
		srv.mu.RUnlock()
		if ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("descriptor not received")
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, cli := startActionFixture(t, srv)
	at := time.Now().UTC().Add(-time.Second)
	sample := model.Observation{Capability: "example.test/capability/counter@1", Property: "value", Value: 7.25, Quality: model.QualityBad, ObservedAt: at, ReceivedAt: at.Add(time.Second), Sequence: 3}
	state := api.StateData{Online: true, Raw: map[string]any{"counter.value": 7.25}, UpdatedAt: time.Now().Unix(), Observations: []api.EntityObservationSet{{EntityID: "sample-entity", Observations: map[string]model.Observation{"value": sample}}}}
	writeEnv(t, edge, api.Envelope{V: api.Version, Type: api.MsgState, Device: desc.DeviceID, Data: rawData(t, state)})
	deadline = time.Now().Add(3 * time.Second)
	for {
		events := fixtureReport(t, cli).Events["sample"]
		if len(events) > 0 {
			if len(events) != 1 || events[0].EventType != application.PropertyObservedEvent || events[0].RequirementID != "sample-input" {
				t.Fatalf("events=%+v", events)
			}
			var observed model.Observation
			if err := json.Unmarshal([]byte(events[0].PayloadJSON), &observed); err != nil {
				t.Fatal(err)
			}
			if observed.Value != 7.25 || observed.Quality != model.QualityBad || !observed.ObservedAt.Equal(at) {
				t.Fatalf("sample changed=%+v", observed)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("state sample never reached actual plugin process")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
