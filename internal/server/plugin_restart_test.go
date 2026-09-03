package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	_ "github.com/DeliciousBuding/cloud-path/examples/stcb"
	"github.com/DeliciousBuding/cloud-path/internal/api"
)

// TestPluginPlaneRecoversAfterRestart 锁定 control-plane-sync §8「Server 重启」：
// 新进程从持久化端口恢复 desired / revision / observed 投影，读面不丢事实，
// 重连的 Edge 拿到当前完整快照（而不是空快照或中间态）。
func TestPluginPlaneRecoversAfterRestart(t *testing.T) {
	st, srv, ts, mem, a, _ := setupPluginSync(t)
	admin := issueTenantToken(t, st, a, `["admin"]`)
	edgeTok := issueTenantToken(t, st, a, `["edge"]`)

	// 1) 离线期间两次修改期望态（revision 1 → 2）。
	if rev := createInstance(t, ts, admin, "e1", "box1"); rev != 1 {
		t.Fatalf("revision = %d, want 1", rev)
	}
	resp := pluginREST(t, ts, admin, http.MethodPatch, "/api/plugin-instances/box1", `{"version":"0.2.0"}`)
	raw := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch = %d body=%s", resp.StatusCode, raw)
	}

	// 2) Edge 接入、上报 observed 并 ack applied（真实推进 applied_revision）。
	ews := dialEdgeHello(t, ts, "e1", edgeTok, api.DeviceMeta{ID: "d1", Adapter: "stcb"})
	ch := edgeReader(ews)
	waitEdgeLink(t, srv, "e1", a)
	env, ok := waitEnv(t, ch, api.MsgPluginDesired, 30*time.Second)
	if !ok {
		t.Fatal("未收到 desired")
	}
	var desired api.PluginDesiredData
	if err := json.Unmarshal(env.Data, &desired); err != nil {
		t.Fatal(err)
	}
	if desired.Revision != 2 {
		t.Fatalf("desired revision = %d, want 2", desired.Revision)
	}
	writeEnv(t, ews, api.Envelope{V: api.Version, Type: api.MsgPluginStatus, Ts: time.Now().Unix(),
		Data: rawData(t, api.PluginStatusData{BootID: "boot-1", Sequence: 1,
			Installations: []api.PluginInstallationStatusData{{
				PluginID: "io.github.acme.driver", Version: "0.2.0", Kind: "Driver", Protocol: 1,
			}},
			ObservedInstances: []api.PluginObservedInstanceData{{
				InstanceID: "box1", PluginID: "io.github.acme.driver", Version: "0.2.0",
				HostOnline: true, State: "HEALTHY", Health: "HEALTHY",
			}}})})
	writeEnv(t, ews, api.Envelope{V: api.Version, Type: api.MsgPluginAck, Ts: time.Now().Unix(),
		Data: rawData(t, api.PluginAckData{Revision: 2, SnapshotDigest: desired.SnapshotDigest,
			Status:  api.PluginAckApplied,
			Results: []api.PluginApplyResultData{{InstanceID: "box1", Status: "applied"}}})})
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		row, err := mem.GetPluginEdgeRevision(a, "e1")
		if err == nil && row.AppliedRevision == 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if row, err := mem.GetPluginEdgeRevision(a, "e1"); err != nil || row.AppliedRevision != 2 {
		t.Fatalf("applied_revision 未持久化: %+v err=%v", row, err)
	}
	ews.CloseNow()
	waitEdgeOffline(t, srv, "e1")

	// 3) 「重启」：同一持久化端口 + 同一 SQLite，全新 Server 进程映像（缓存为空）。
	srv2 := New(Config{Store: st, Version: "test", RequireAuth: true, PluginStore: mem})
	ts2 := httptest.NewServer(srv2.Routes())
	t.Cleanup(func() { ts2.Close(); srv2.CloseAll() })

	list := listInstancesHTTP(t, ts2, admin)
	if len(list.Instances) != 1 {
		t.Fatalf("重启后读面丢失期望态: %+v", list.Instances)
	}
	got := list.Instances[0]
	if got.Desired.Version != "0.2.0" || got.DesiredRevision != 2 || got.AppliedRevision != 2 || got.Drift {
		t.Fatalf("重启后 desired/revision 未恢复: %+v", got)
	}
	if !got.HasObserved || got.Observed == nil || got.Observed.State != "HEALTHY" {
		t.Fatalf("重启后 observed 投影未恢复: %+v", got)
	}
	plugins := struct {
		Plugins []struct {
			ID      string `json:"id"`
			Version string `json:"version"`
		} `json:"plugins"`
	}{}
	r2 := doJSON(t, http.MethodGet, ts2.URL+"/api/plugins", "", bearerJSON(admin), nil)
	body := readBody(t, r2)
	if err := json.Unmarshal([]byte(body), &plugins); err != nil {
		t.Fatalf("decode plugins: %v (%s)", err, body)
	}
	if len(plugins.Plugins) != 1 || plugins.Plugins[0].ID != "io.github.acme.driver" {
		t.Fatalf("重启后安装物投影未恢复: %s", body)
	}

	// 4) Edge 重连到新进程：拿到当前完整快照（revision 2，最终 payload）。
	ews2 := dialEdgeHello(t, ts2, "e1", edgeTok, api.DeviceMeta{ID: "d1", Adapter: "stcb"})
	defer ews2.CloseNow()
	ch2 := edgeReader(ews2)
	waitEdgeLink(t, srv2, "e1", a)
	env2, ok := waitEnv(t, ch2, api.MsgPluginDesired, 30*time.Second)
	if !ok {
		t.Fatal("重启后重连未收到 desired")
	}
	var desired2 api.PluginDesiredData
	if err := json.Unmarshal(env2.Data, &desired2); err != nil {
		t.Fatal(err)
	}
	if desired2.Revision != 2 || desired2.SnapshotDigest != desired.SnapshotDigest ||
		len(desired2.Instances) != 1 || desired2.Instances[0].Version != "0.2.0" {
		t.Fatalf("重启后下发的快照与重启前不一致: %+v", desired2)
	}
}
