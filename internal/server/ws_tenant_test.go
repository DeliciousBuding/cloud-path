package server

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/DeliciousBuding/cloud-path/examples/stcb"
	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/model"
	"github.com/DeliciousBuding/cloud-path/internal/store"
)

// drainEnvelopes 非阻塞排空浏览器发送队列，返回收到的信封（测试用）。
func drainEnvelopes(t *testing.T, ch chan []byte) []api.Envelope {
	t.Helper()
	var out []api.Envelope
	for {
		select {
		case data := <-ch:
			var env api.Envelope
			if err := json.Unmarshal(data, &env); err != nil {
				t.Fatalf("bad envelope: %v", err)
			}
			out = append(out, env)
		default:
			return out
		}
	}
}

func hasType(envs []api.Envelope, typ api.MsgType, device string) bool {
	for _, e := range envs {
		if e.Type == typ && e.Device == device {
			return true
		}
	}
	return false
}

// TestBrowserWSTenantIsolation 锁定 WS 租户隔离：
// snapshot 按浏览器租户过滤 devices/edges/descriptors，实时 descriptor/state 只 fan-out 到同租户。
func TestBrowserWSTenantIsolation(t *testing.T) {
	srv := New(Config{Version: "test"})
	entities := func() []model.Entity {
		return []model.Entity{{
			EntityID: "clock", UniqueKey: "clock", Category: model.EntitySensor,
			Capabilities: []string{"cloudpath.dev/capability/clock@1"},
		}}
	}
	bA := &browserConn{send: make(chan []byte, 16), tenant: "tenant-a"}
	bB := &browserConn{send: make(chan []byte, 16), tenant: "tenant-b"}

	srv.mu.Lock()
	srv.devices["a/d1"] = &api.DeviceView{ID: "a/d1", EdgeID: "a", Adapter: "stcb", Online: true, State: map[string]any{}}
	srv.devices["b/d2"] = &api.DeviceView{ID: "b/d2", EdgeID: "b", Adapter: "stcb", Online: true, State: map[string]any{}}
	srv.deviceTenants = map[string]string{"a/d1": "tenant-a", "b/d2": "tenant-b"}
	srv.descriptors = map[string]model.Descriptor{
		"a/d1": {DeviceID: "a/d1", ExternalID: "d1", Status: model.DeviceOnline, Entities: entities()},
		"b/d2": {DeviceID: "b/d2", ExternalID: "d2", Status: model.DeviceOnline, Entities: entities()},
	}
	srv.edges["a"] = &edgeLink{edgeID: "a", tenant: "tenant-a", devices: []string{"a/d1"}}
	srv.edges["b"] = &edgeLink{edgeID: "b", tenant: "tenant-b", devices: []string{"b/d2"}}
	srv.browsers[bA] = struct{}{}
	srv.browsers[bB] = struct{}{}
	srv.mu.Unlock()

	// 1) snapshot 只含 tenant-a 的 devices/edges/descriptors
	srv.mu.RLock()
	snap := srv.snapshotFor("tenant-a")
	srv.mu.RUnlock()
	var sd api.SnapshotData
	if err := json.Unmarshal(snap.Data, &sd); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(sd.Devices) != 1 || sd.Devices[0].ID != "a/d1" {
		t.Fatalf("snapshot devices leaked: %+v", sd.Devices)
	}
	if len(sd.Edges) != 1 || sd.Edges[0].EdgeID != "a" {
		t.Fatalf("snapshot edges leaked: %+v", sd.Edges)
	}
	if len(sd.Descriptors) != 1 || sd.Descriptors[0].DeviceID != "a/d1" {
		t.Fatalf("snapshot descriptors leaked: %+v", sd.Descriptors)
	}

	// 2) 实时 descriptor：a/d1 只投递给 tenant-a
	srv.broadcast(api.Envelope{V: api.Version, Type: api.MsgDescriptor, Device: "a/d1",
		Ts: time.Now().Unix(), Data: rawData(t, model.Descriptor{DeviceID: "a/d1", ExternalID: "d1",
			Status: model.DeviceOnline, Entities: entities()})})
	if got := drainEnvelopes(t, bA.send); !hasType(got, api.MsgDescriptor, "a/d1") {
		t.Fatalf("tenant-a 未收到 a/d1 descriptor: %+v", got)
	}
	if got := drainEnvelopes(t, bB.send); hasType(got, api.MsgDescriptor, "a/d1") {
		t.Fatalf("tenant-b 泄漏收到 a/d1 descriptor: %+v", got)
	}

	// 3) 实时 state：b/d2 只投递给 tenant-b（反向验证）
	srv.broadcast(api.Envelope{V: api.Version, Type: api.MsgState, Device: "b/d2",
		Ts: time.Now().Unix(), Data: rawData(t, api.StateData{Online: true, Raw: map[string]any{"x": 1}})})
	if got := drainEnvelopes(t, bA.send); hasType(got, api.MsgState, "b/d2") {
		t.Fatalf("tenant-a 泄漏收到 b/d2 state: %+v", got)
	}
	if got := drainEnvelopes(t, bB.send); !hasType(got, api.MsgState, "b/d2") {
		t.Fatalf("tenant-b 未收到 b/d2 state: %+v", got)
	}
}

// TestBrowserWSTenantCapture 锁定 browserConn 记录身份租户：L1 服务令牌 = default。
func TestBrowserWSTenantCapture(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv := New(Config{Store: st, Version: "test", Token: "sekret"})
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()
	defer srv.CloseAll()

	dial(t, wsURL(ts.URL, "/ws?token=sekret"))
	deadline := time.Now().Add(30 * time.Second)
	var tenant string
	for time.Now().Before(deadline) {
		srv.mu.RLock()
		for bc := range srv.browsers {
			tenant = bc.tenant
		}
		n := len(srv.browsers)
		srv.mu.RUnlock()
		if n > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if tenant != "default" {
		t.Fatalf("browser tenant = %q, want default", tenant)
	}
}
