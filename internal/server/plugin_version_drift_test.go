package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/server/storeport"
)

// Exercise the REST read views and the projection used by write responses from
// persisted desired/observed facts, including legacy rows with missing versions.
func TestPluginVersionDriftProjection(t *testing.T) {
	cases := []struct {
		name, edgeID, desiredVersion, observedVersion string
		enabled, hasObserved                          bool
		appliedRevision                               uint64
		wantDrift                                     bool
	}{
		{name: "matching", desiredVersion: "0.2.2", observedVersion: "0.2.2", enabled: true, hasObserved: true, appliedRevision: 1},
		{name: "mismatching", desiredVersion: "0.2.2", observedVersion: "0.2.1", enabled: true, hasObserved: true, appliedRevision: 1, wantDrift: true},
		{name: "no_observation", desiredVersion: "0.2.2", enabled: true, appliedRevision: 1},
		{name: "missing_observed_version", desiredVersion: "0.2.2", enabled: true, hasObserved: true, appliedRevision: 1},
		{name: "missing_desired_version", observedVersion: "0.2.1", enabled: true, hasObserved: true, appliedRevision: 1},
		{name: "both_versions_missing", enabled: true, hasObserved: true, appliedRevision: 1},
		{name: "disabled_old_version", desiredVersion: "0.2.2", observedVersion: "0.2.1", hasObserved: true, appliedRevision: 1},
		{name: "pending_matching_version", desiredVersion: "0.2.2", observedVersion: "0.2.2", enabled: true, hasObserved: true, wantDrift: true},
		{name: "pending_no_observation", desiredVersion: "0.2.2", enabled: true, wantDrift: true},
		{name: "pending_missing_version", desiredVersion: "0.2.2", enabled: true, hasObserved: true, wantDrift: true},
		{name: "pending_disabled", desiredVersion: "0.2.2", observedVersion: "0.2.1", hasObserved: true, wantDrift: true},
		{name: "apphost_matching", edgeID: AppHostEdgeID, desiredVersion: "0.2.2", observedVersion: "0.2.2", enabled: true, hasObserved: true, appliedRevision: 1},
		{name: "apphost_missing_version", edgeID: AppHostEdgeID, desiredVersion: "0.2.2", enabled: true, hasObserved: true, appliedRevision: 1},
		{name: "apphost_mismatching", edgeID: AppHostEdgeID, desiredVersion: "0.2.2", observedVersion: "0.2.1", enabled: true, hasObserved: true, appliedRevision: 1, wantDrift: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _, _, mem, tenantID, _ := setupPluginPlane(t)
			edgeID := tc.edgeID
			if edgeID == "" {
				edgeID = "e1"
			}
			revision, err := mem.CreatePluginInstance(storeport.PluginInstanceRow{
				TenantID: tenantID, EdgeID: edgeID, InstanceID: "driver1",
				PluginID: "io.github.acme.plugin", Version: tc.desiredVersion, Enabled: tc.enabled,
			})
			if err != nil || revision != 1 {
				t.Fatalf("create fixture revision=%d err=%v", revision, err)
			}
			if err := mem.SetPluginEdgeApplied(tenantID, edgeID, "boot-1", 1, tc.appliedRevision, 10); err != nil {
				t.Fatal(err)
			}
			state := "HEALTHY"
			if edgeID == AppHostEdgeID {
				state = "RUNNING"
			}
			if tc.hasObserved {
				seedObservations(t, srv, mem, tenantID, edgeID, []api.PluginObservedInstanceData{{
					InstanceID: "driver1", PluginID: "io.github.acme.plugin", Version: tc.observedVersion,
					HostOnline: true, State: state, Health: "HEALTHY",
				}}, 10)
			}
			list := listInstances(t, srv, tenantID, "tenant-a", string(api.RoleViewer))
			if len(list.Instances) != 1 {
				t.Fatalf("list instances=%+v", list.Instances)
			}
			rec := servePlugin(t, srv, http.MethodGet, "/api/plugin-instances/driver1", "", tenantID, "tenant-a", string(api.RoleViewer))
			if rec.Code != http.StatusOK {
				t.Fatalf("detail status=%d body=%s", rec.Code, rec.Body.String())
			}
			var detail api.PluginInstanceView
			decodeJSON(t, rec, &detail)
			for name, view := range map[string]api.PluginInstanceView{
				"list": list.Instances[0], "detail": detail,
				"write_projection": srv.pluginInstanceView(tenantID, "tenant-a", edgeID, "driver1"),
			} {
				t.Run(name, func(t *testing.T) {
					if view.Drift != tc.wantDrift {
						t.Errorf("drift=%v, want %v (desired=%q observed=%q revisions=%d/%d)",
							view.Drift, tc.wantDrift, tc.desiredVersion, tc.observedVersion, view.DesiredRevision, view.AppliedRevision)
					}
					if view.TenantID != tenantID || view.EdgeID != edgeID || view.ID != "driver1" ||
						view.Desired.Version != tc.desiredVersion || view.Desired.Enabled != tc.enabled ||
						view.DesiredRevision != revision || view.AppliedRevision != tc.appliedRevision {
						t.Errorf("desired/revision/identity facts changed: %+v", view)
					}
					if view.HasObserved != tc.hasObserved || (view.Observed != nil) != tc.hasObserved {
						t.Fatalf("observed presence changed: %+v", view)
					}
					if tc.hasObserved && (view.Observed.State != state || view.Observed.Health != "HEALTHY") {
						t.Errorf("observed state/health changed: %+v", view.Observed)
					}
					if tc.observedVersion != "" && view.Observed.Version != tc.observedVersion {
						t.Errorf("observed version changed: %+v", view.Observed)
					}
					if view.EdgeOnline || !view.Stale {
						t.Errorf("offline/stale facts changed: %+v", view)
					}
				})
			}
		})
	}
}

// A valid applied ack must not hide an old running version after an API update.
// Use real HTTP/auth and WS messages; do not manufacture the plane's drift flag.
func TestPluginVersionDriftAfterAppliedAck(t *testing.T) {
	st, srv, ts, mem, tenantID, otherTenant := setupPluginSync(t)
	admin := issueTenantToken(t, st, tenantID, `["admin"]`)
	otherAdmin := issueTenantToken(t, st, otherTenant, `["admin"]`)
	edgeToken := issueTenantToken(t, st, tenantID, `["edge"]`)
	for _, body := range []string{
		`{"edge_id":"e1","instance_id":"driver1","plugin_id":"io.github.acme.driver","version":"0.2.1","enabled":true}`,
		`{"edge_id":"e1","instance_id":"stable","plugin_id":"io.github.acme.driver","version":"0.2.2","enabled":true}`,
	} {
		resp := pluginREST(t, ts, admin, http.MethodPost, "/api/plugin-instances", body)
		raw := readBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("create status=%d body=%s", resp.StatusCode, raw)
		}
	}
	edge := dialEdgeHello(t, ts, "e1", edgeToken, api.DeviceMeta{ID: "d1", Adapter: "demo"})
	defer func() {
		edge.CloseNow()
		waitEdgeOffline(t, srv, "e1")
	}()
	ch := edgeReader(edge)
	waitEdgeLink(t, srv, "e1", tenantID)

	report := func(sequence uint64, version string) {
		t.Helper()
		writeEnv(t, edge, api.Envelope{V: api.Version, Type: api.MsgPluginStatus, Ts: time.Now().Unix(),
			Data: rawData(t, api.PluginStatusData{BootID: "version-drift-boot", Sequence: sequence,
				ObservedInstances: []api.PluginObservedInstanceData{
					{InstanceID: "driver1", PluginID: "io.github.acme.driver", Version: version, HostOnline: true, State: "HEALTHY", Health: "HEALTHY"},
					{InstanceID: "stable", PluginID: "io.github.acme.driver", Version: "0.2.2", HostOnline: true, State: "HEALTHY", Health: "HEALTHY"},
				}})})
	}
	apply := func(revision uint64) {
		t.Helper()
		env, ok := waitEnv(t, ch, api.MsgPluginDesired, 30*time.Second)
		if !ok {
			t.Fatal("missing desired snapshot")
		}
		var desired api.PluginDesiredData
		if err := json.Unmarshal(env.Data, &desired); err != nil || desired.Revision != revision {
			t.Fatalf("desired=%+v err=%v, want revision %d", desired, err, revision)
		}
		writeEnv(t, edge, api.Envelope{V: api.Version, Type: api.MsgPluginAck, Ts: time.Now().Unix(),
			Data: rawData(t, api.PluginAckData{Revision: revision, SnapshotDigest: desired.SnapshotDigest, Status: api.PluginAckApplied})})
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			row, err := mem.GetPluginEdgeRevision(tenantID, "e1")
			if err != nil {
				t.Fatal(err)
			}
			if row.AppliedRevision == revision {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("applied revision did not reach %d", revision)
	}
	check := func(revision uint64, desiredVersion, observedVersion string, wantDrift bool) {
		t.Helper()
		list := listInstancesHTTP(t, ts, admin)
		if len(list.Instances) != 2 {
			t.Fatalf("instances=%+v", list.Instances)
		}
		for _, view := range list.Instances {
			resp := pluginREST(t, ts, admin, http.MethodGet, "/api/plugin-instances/"+view.ID, "")
			raw := readBody(t, resp)
			var detail api.PluginInstanceView
			if err := json.Unmarshal([]byte(raw), &detail); err != nil || resp.StatusCode != http.StatusOK {
				t.Fatalf("detail status=%d body=%s err=%v", resp.StatusCode, raw, err)
			}
			for _, got := range []api.PluginInstanceView{view, detail} {
				wantDesired, wantObserved, drift := desiredVersion, observedVersion, wantDrift
				if got.ID == "stable" {
					wantDesired, wantObserved, drift = "0.2.2", "0.2.2", false
				} else if got.ID != "driver1" {
					t.Fatalf("unexpected instance: %+v", got)
				}
				if got.Drift != drift {
					t.Errorf("%s drift=%v, want %v after applied ack; desired=%+v observed=%+v",
						got.ID, got.Drift, drift, got.Desired, got.Observed)
				}
				if got.Desired.Version != wantDesired || !got.Desired.Enabled || got.DesiredRevision != revision || got.AppliedRevision != revision ||
					!got.HasObserved || got.Observed == nil || got.Observed.Version != wantObserved || got.Observed.State != "HEALTHY" || got.Observed.Health != "HEALTHY" ||
					!got.EdgeOnline || got.Stale || got.TenantID != tenantID {
					t.Errorf("projection facts changed: %+v observed=%+v", got, got.Observed)
				}
			}
		}
	}

	report(1, "0.2.1")
	apply(2)
	check(2, "0.2.1", "0.2.1", false)

	resp := pluginREST(t, ts, admin, http.MethodPatch, "/api/plugin-instances/driver1", `{"version":"0.2.2"}`)
	raw := readBody(t, resp)
	var updated api.PluginInstanceWriteResponse
	if err := json.Unmarshal([]byte(raw), &updated); err != nil || resp.StatusCode != http.StatusOK || updated.Revision != 3 {
		t.Fatalf("update status=%d body=%s err=%v", resp.StatusCode, raw, err)
	}
	report(2, "0.2.1") // Legacy Edge still runs the old version, but claims applied.
	apply(3)
	check(3, "0.2.2", "0.2.1", true)

	if other := listInstancesHTTP(t, ts, otherAdmin); len(other.Instances) != 0 {
		t.Fatalf("cross-tenant list leaked: %+v", other.Instances)
	}
	resp = pluginREST(t, ts, otherAdmin, http.MethodGet, "/api/plugin-instances/driver1", "")
	raw = readBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-tenant detail status=%d body=%s", resp.StatusCode, raw)
	}

	report(3, "0.2.2") // Only new observed evidence clears the version mismatch.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		row, err := mem.GetPluginEdgeRevision(tenantID, "e1")
		if err != nil {
			t.Fatal(err)
		}
		if row.LastSequence == 3 {
			check(3, "0.2.2", "0.2.2", false)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("matching version report was not processed")
}
