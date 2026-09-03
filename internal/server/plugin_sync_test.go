package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	_ "github.com/DeliciousBuding/cloud-path/examples/stcb"
	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/server/storeport"
	"github.com/DeliciousBuding/cloud-path/internal/store"
)

// setupPluginSync 构造插件控制面 WS 测试底座：真实 SQLite（租户/审计）+ 真实 WS 路由
// + storeport 内存插件存储；返回 tenant-a / tenant-b 的真实 id。
func setupPluginSync(t *testing.T) (*store.Store, *Server, *httptest.Server, *storeport.Memory, int64, int64) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "pluginsync.db"))
	if err != nil {
		t.Fatal(err)
	}
	a := ensureTenantSlug(t, st, "tenant-a")
	b := ensureTenantSlug(t, st, "tenant-b")
	mem := storeport.NewMemory()
	srv := New(Config{Store: st, Version: "test", RequireAuth: true, PluginStore: mem})
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(func() { ts.Close(); srv.CloseAll(); time.Sleep(50 * time.Millisecond) })
	t.Cleanup(func() { st.Close() })
	return st, srv, ts, mem, a, b
}

func ensureTenantSlug(t *testing.T, st *store.Store, slug string) int64 {
	t.Helper()
	if row, err := st.GetTenantBySlug(slug); err == nil {
		return row.ID
	}
	id, err := st.CreateTenant(slug, slug)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// edgeReader 起独立读循环收集该连接收到的全部信封。
// 不在主 goroutine 上做带 deadline 的读，避免破坏真实 WS 状态（既有约定）。
func edgeReader(ws *websocket.Conn) <-chan api.Envelope {
	ch := make(chan api.Envelope, 128)
	go func() {
		defer close(ch)
		for {
			_, data, err := ws.Read(context.Background())
			if err != nil {
				return
			}
			var env api.Envelope
			if err := json.Unmarshal(data, &env); err != nil {
				continue
			}
			select {
			case ch <- env:
			default:
			}
		}
	}()
	return ch
}

// waitEnv 在窗口内等待指定类型的信封；超时返回 ok=false（用于「不得再收到」断言）。
func waitEnv(t *testing.T, ch <-chan api.Envelope, typ api.MsgType, within time.Duration) (api.Envelope, bool) {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case env, ok := <-ch:
			if !ok {
				return api.Envelope{}, false
			}
			if env.Type == typ {
				return env, true
			}
		case <-deadline:
			return api.Envelope{}, false
		}
	}
}

// pluginREST 用租户令牌调用插件写 API。
func pluginREST(t *testing.T, ts *httptest.Server, token, method, path, body string) *http.Response {
	t.Helper()
	return doJSON(t, method, ts.URL+path, body, bearerJSON(token), nil)
}

// createInstance 通过真实 REST 写面创建一个期望态实例，返回新 revision。
func createInstance(t *testing.T, ts *httptest.Server, token, edgeID, instanceID string) uint64 {
	t.Helper()
	resp := pluginREST(t, ts, token, http.MethodPost, "/api/plugin-instances",
		`{"edge_id":"`+edgeID+`","instance_id":"`+instanceID+`","plugin_id":"io.github.acme.driver","version":"0.1.0","enabled":true}`)
	raw := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create %s = %d body=%s", instanceID, resp.StatusCode, raw)
	}
	var out api.PluginInstanceWriteResponse
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("decode create: %v (%s)", err, raw)
	}
	return out.Revision
}

// TestPluginDesiredPushedOnHello 锁定 §4.2：hello 成功后立即下发当前完整期望态
// （revision + snapshot_digest + instances），Edge 离线期间的变更在此一次性收敛。
func TestPluginDesiredPushedOnHello(t *testing.T) {
	st, srv, ts, _, a, _ := setupPluginSync(t)
	admin := issueTenantToken(t, st, a, `["admin"]`)
	edgeTok := issueTenantToken(t, st, a, `["edge"]`)
	rev := createInstance(t, ts, admin, "e1", "box1")
	if rev != 1 {
		t.Fatalf("首个 revision = %d, want 1", rev)
	}

	ews := dialEdgeHello(t, ts, "e1", edgeTok, api.DeviceMeta{ID: "d1", Adapter: "stcb"})
	defer ews.CloseNow()
	ch := edgeReader(ews)
	env, ok := waitEnv(t, ch, api.MsgPluginDesired, 30*time.Second)
	if !ok {
		t.Fatal("hello 后未收到 plugin_desired")
	}
	if env.Device != "" {
		t.Fatalf("plugin_desired 不应携带设备键，收到 %q", env.Device)
	}
	var data api.PluginDesiredData
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("decode desired: %v", err)
	}
	if data.Revision != rev || !strings.HasPrefix(data.SnapshotDigest, "sha256:") {
		t.Fatalf("desired 快照错误: %+v", data)
	}
	if len(data.Instances) != 1 || data.Instances[0].InstanceID != "box1" || !data.Instances[0].Enabled {
		t.Fatalf("desired 实例错误: %+v", data.Instances)
	}
	waitEdgeLink(t, srv, "e1", a)
}

// TestOfflineDesiredChangesConvergeOnce 锁定暗卷 4：Edge 离线期间 Server 改 desired 两次，
// 重连后只收敛最终完整快照，不回放中间副作用（只有一条 plugin_desired）。
func TestOfflineDesiredChangesConvergeOnce(t *testing.T) {
	st, srv, ts, mem, a, _ := setupPluginSync(t)
	admin := issueTenantToken(t, st, a, `["admin"]`)
	edgeTok := issueTenantToken(t, st, a, `["edge"]`)

	if rev := createInstance(t, ts, admin, "e1", "box1"); rev != 1 {
		t.Fatalf("revision = %d, want 1", rev)
	}
	resp := pluginREST(t, ts, admin, http.MethodPatch, "/api/plugin-instances/box1",
		`{"enabled":false,"version":"0.2.0"}`)
	raw := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch = %d body=%s", resp.StatusCode, raw)
	}
	var patched api.PluginInstanceWriteResponse
	if err := json.Unmarshal([]byte(raw), &patched); err != nil {
		t.Fatal(err)
	}
	if patched.Revision != 2 {
		t.Fatalf("第二次修改后 revision = %d, want 2", patched.Revision)
	}
	if rev, err := mem.PluginDesiredRevision(a, "e1"); err != nil || rev != 2 {
		t.Fatalf("store revision = %d err=%v, want 2", rev, err)
	}

	// 离线期间不得有任何下发目标；重连后只收到一份最终快照。
	ews := dialEdgeHello(t, ts, "e1", edgeTok, api.DeviceMeta{ID: "d1", Adapter: "stcb"})
	defer ews.CloseNow()
	ch := edgeReader(ews)
	env, ok := waitEnv(t, ch, api.MsgPluginDesired, 30*time.Second)
	if !ok {
		t.Fatal("重连后未收到 plugin_desired")
	}
	var data api.PluginDesiredData
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.Revision != 2 || len(data.Instances) != 1 ||
		data.Instances[0].Version != "0.2.0" || data.Instances[0].Enabled {
		t.Fatalf("重连收敛到的不是最终快照: %+v", data)
	}
	if _, again := waitEnv(t, ch, api.MsgPluginDesired, 400*time.Millisecond); again {
		t.Fatal("重连后回放了多份中间快照")
	}
	waitEdgeLink(t, srv, "e1", a)
}

// TestPluginDesiredRepushedOnChange 锁定：在线 Edge 在 desired 变更后再次收到完整快照。
func TestPluginDesiredRepushedOnChange(t *testing.T) {
	st, srv, ts, _, a, _ := setupPluginSync(t)
	admin := issueTenantToken(t, st, a, `["admin"]`)
	edgeTok := issueTenantToken(t, st, a, `["edge"]`)

	ews := dialEdgeHello(t, ts, "e1", edgeTok, api.DeviceMeta{ID: "d1", Adapter: "stcb"})
	defer ews.CloseNow()
	ch := edgeReader(ews)
	waitEdgeLink(t, srv, "e1", a)
	if _, ok := waitEnv(t, ch, api.MsgPluginDesired, 30*time.Second); !ok {
		t.Fatal("hello 后未收到初始 plugin_desired")
	}
	if rev := createInstance(t, ts, admin, "e1", "box1"); rev != 1 {
		t.Fatalf("revision = %d, want 1", rev)
	}
	env, ok := waitEnv(t, ch, api.MsgPluginDesired, 30*time.Second)
	if !ok {
		t.Fatal("desired 变更后未再次下发")
	}
	var data api.PluginDesiredData
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.Revision != 1 || len(data.Instances) != 1 {
		t.Fatalf("重发快照错误: %+v", data)
	}
}

// TestPluginDesiredCarriesOnlySecretHandles 锁定 secret 边界：下发给 Edge 的 WS 字节里
// 只出现 secret://<name> handle，绝不出现明文（Server 也从未收到明文）。
func TestPluginDesiredCarriesOnlySecretHandles(t *testing.T) {
	st, srv, ts, _, a, _ := setupPluginSync(t)
	admin := issueTenantToken(t, st, a, `["admin"]`)
	edgeTok := issueTenantToken(t, st, a, `["edge"]`)
	resp := pluginREST(t, ts, admin, http.MethodPost, "/api/plugin-instances",
		`{"edge_id":"e1","instance_id":"box1","plugin_id":"p1","version":"1.0.0","config":{"api_token":"secret://api_token","mode":"quiet"}}`)
	raw := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create = %d body=%s", resp.StatusCode, raw)
	}

	ews := dialEdgeHello(t, ts, "e1", edgeTok, api.DeviceMeta{ID: "d1", Adapter: "stcb"})
	defer ews.CloseNow()
	ch := edgeReader(ews)
	env, ok := waitEnv(t, ch, api.MsgPluginDesired, 30*time.Second)
	if !ok {
		t.Fatal("未收到 plugin_desired")
	}
	if !strings.Contains(string(env.Data), `"secret://api_token"`) {
		t.Fatalf("desired 未以 handle 形式携带 secret: %s", env.Data)
	}
	if strings.Contains(string(env.Data), "api_token=") || strings.Contains(raw, "hunter") {
		t.Fatalf("WS/REST 出现疑似明文 secret: ws=%s rest=%s", env.Data, raw)
	}
	waitEdgeLink(t, srv, "e1", a)
}

// TestPluginStatusSequenceGating 锁定 §4.1 幂等：同 boot 重复/倒序忽略、新 boot 可从 1 开始、
// 旧 boot 的迟到消息（更大 sequence）必须忽略；未知消息类型不断连。
func TestPluginStatusSequenceGating(t *testing.T) {
	st, srv, ts, _, a, _ := setupPluginSync(t)
	admin := issueTenantToken(t, st, a, `["admin"]`)
	edgeTok := issueTenantToken(t, st, a, `["edge"]`)
	if rev := createInstance(t, ts, admin, "e1", "box1"); rev != 1 {
		t.Fatalf("revision = %d", rev)
	}

	ews := dialEdgeHello(t, ts, "e1", edgeTok, api.DeviceMeta{ID: "d1", Adapter: "stcb"})
	defer ews.CloseNow()
	ch := edgeReader(ews)
	waitEdgeLink(t, srv, "e1", a)
	if _, ok := waitEnv(t, ch, api.MsgPluginDesired, 30*time.Second); !ok {
		t.Fatal("未收到 plugin_desired")
	}

	send := func(boot string, seq uint64, state string, restarts int) {
		t.Helper()
		writeEnv(t, ews, api.Envelope{V: api.Version, Type: api.MsgPluginStatus, Ts: time.Now().Unix(),
			Data: rawData(t, api.PluginStatusData{
				BootID: boot, Sequence: seq, AppliedRevision: 0,
				Installations: []api.PluginInstallationStatusData{{
					PluginID: "p1", Version: "1.0.0", Kind: "Driver", Protocol: 1,
				}},
				ObservedInstances: []api.PluginObservedInstanceData{{
					InstanceID: "box1", PluginID: "p1", Version: "1.0.0", HostOnline: true,
					State: state, Health: "HEALTHY", RestartCount: restarts,
				}},
			})})
	}
	observedState := func() string {
		t.Helper()
		resp := pluginREST(t, ts, admin, http.MethodGet, "/api/plugin-instances", "")
		raw := readBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("list = %d", resp.StatusCode)
		}
		var list api.PluginInstanceListResponse
		if err := json.Unmarshal([]byte(raw), &list); err != nil {
			t.Fatal(err)
		}
		for _, in := range list.Instances {
			if in.ID == "box1" && in.Observed != nil {
				return in.Observed.State + "/" + in.Observed.Health
			}
		}
		return "none"
	}
	waitObserved := func(want string) {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			if got := observedState(); got == want {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("observed 未收敛到 %q（当前 %q）", want, observedState())
	}

	// 未知消息类型必须被忽略且不断连（向后兼容）。
	writeEnv(t, ews, api.Envelope{V: api.Version, Type: api.MsgType("cloudpath_future_type"),
		Ts: time.Now().Unix(), Data: rawData(t, map[string]any{"x": 1})})

	send("boot-1", 1, "STARTING", 0)
	waitObserved("STARTING/HEALTHY")
	// 重复 sequence：payload 不同也必须被忽略（投影不变）。
	send("boot-1", 1, "CRASHED", 9)
	send("boot-1", 0, "CRASHED", 9)
	time.Sleep(300 * time.Millisecond)
	if got := observedState(); got != "STARTING/HEALTHY" {
		t.Fatalf("重复/倒序 sequence 被接受: %q", got)
	}
	// 正常前进。
	send("boot-1", 2, "HEALTHY", 0)
	waitObserved("HEALTHY/HEALTHY")
	// 新 boot 可从 1 开始（Edge 进程重启）。
	send("boot-2", 1, "STARTING", 1)
	waitObserved("STARTING/HEALTHY")
	// 旧 boot 的迟到消息（更大 sequence）必须忽略（暗卷 2）。
	send("boot-1", 99, "CRASHED", 42)
	time.Sleep(300 * time.Millisecond)
	if got := observedState(); got != "STARTING/HEALTHY" {
		t.Fatalf("旧 boot 迟到消息被接受: %q", got)
	}
	// 连接仍然存活：后续状态上报照常生效。
	writeEnv(t, ews, api.Envelope{V: api.Version, Type: api.MsgState, Device: "e1/d1",
		Ts: time.Now().Unix(), Data: rawData(t, api.StateData{Online: true, Raw: map[string]any{"k": 1}, UpdatedAt: time.Now().Unix()})})
	waitDeviceOnline(t, srv, "e1/d1")
}

// TestPluginStatusRejectedFromEvictedLink 锁定：被重连挤掉的旧连接不得再写投影
// （旧 boot 的迟到消息在传输层就被挡住）。
func TestPluginStatusRejectedFromEvictedLink(t *testing.T) {
	st, srv, ts, _, a, _ := setupPluginSync(t)
	admin := issueTenantToken(t, st, a, `["admin"]`)
	edgeTok := issueTenantToken(t, st, a, `["edge"]`)
	if rev := createInstance(t, ts, admin, "e1", "box1"); rev != 1 {
		t.Fatalf("revision = %d", rev)
	}
	old := dialEdgeHello(t, ts, "e1", edgeTok, api.DeviceMeta{ID: "d1", Adapter: "stcb"})
	oldCh := edgeReader(old)
	waitEdgeLink(t, srv, "e1", a)
	if _, ok := waitEnv(t, oldCh, api.MsgPluginDesired, 30*time.Second); !ok {
		t.Fatal("旧连接未收到 desired")
	}
	writeEnv(t, old, api.Envelope{V: api.Version, Type: api.MsgPluginStatus, Ts: time.Now().Unix(),
		Data: rawData(t, api.PluginStatusData{BootID: "boot-old", Sequence: 1,
			ObservedInstances: []api.PluginObservedInstanceData{{
				InstanceID: "box1", PluginID: "p1", Version: "1.0.0", State: "HEALTHY", Health: "HEALTHY"}}})})
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		srv.plugin.mu.Lock()
		tp := srv.plugin.tenants[a]
		n := 0
		if tp != nil {
			if ep, ok := tp.edges["e1"]; ok {
				n = len(ep.observed)
			}
		}
		srv.plugin.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// 同租户新连接挤掉旧连接；旧连接随后的高 sequence 上报不得写入投影。
	fresh := dialEdgeHello(t, ts, "e1", edgeTok, api.DeviceMeta{ID: "d1", Adapter: "stcb"})
	defer fresh.CloseNow()
	freshCh := edgeReader(fresh)
	if _, _, err := old.Read(context.Background()); err == nil {
		t.Log("旧连接未被立即关闭，继续验证迟到消息被忽略")
	}
	writeEnv(t, old, api.Envelope{V: api.Version, Type: api.MsgPluginStatus, Ts: time.Now().Unix(),
		Data: rawData(t, api.PluginStatusData{BootID: "boot-old", Sequence: 500,
			ObservedInstances: []api.PluginObservedInstanceData{{
				InstanceID: "box1", PluginID: "p1", Version: "1.0.0", State: "CRASHED", Health: "UNKNOWN"}}})})
	if _, ok := waitEnv(t, freshCh, api.MsgPluginDesired, 30*time.Second); !ok {
		t.Fatal("新连接未收到 desired")
	}
	writeEnv(t, fresh, api.Envelope{V: api.Version, Type: api.MsgPluginStatus, Ts: time.Now().Unix(),
		Data: rawData(t, api.PluginStatusData{BootID: "boot-new", Sequence: 1,
			ObservedInstances: []api.PluginObservedInstanceData{{
				InstanceID: "box1", PluginID: "p1", Version: "1.0.0", State: "HEALTHY", Health: "HEALTHY"}}})})
	time.Sleep(400 * time.Millisecond)
	srv.plugin.mu.Lock()
	tp := srv.plugin.tenants[a]
	var state, boot string
	var seq uint64
	if tp != nil {
		if ep, ok := tp.edges["e1"]; ok {
			boot, seq = ep.bootID, ep.lastSequence
			if o, ok := ep.observed["box1"]; ok {
				state = o.State
			}
		}
	}
	srv.plugin.mu.Unlock()
	if state != "HEALTHY" || boot != "boot-new" || seq != 1 {
		t.Fatalf("旧连接迟到消息污染了投影: state=%q boot=%q seq=%d", state, boot, seq)
	}
	old.CloseNow()
}

// TestPluginStatusCrossTenantFailClosed 锁定暗卷 3：tenant-b 伪造 tenant-a 的 edge id
// 被 hello 阶段拒绝；b 用自己 edge 上报只写入 b 的投影，a 的投影与连接不受影响。
func TestPluginStatusCrossTenantFailClosed(t *testing.T) {
	st, srv, ts, _, a, b := setupPluginSync(t)
	adminA := issueTenantToken(t, st, a, `["admin"]`)
	edgeA := issueTenantToken(t, st, a, `["edge"]`)
	edgeB := issueTenantToken(t, st, b, `["edge"]`)
	if rev := createInstance(t, ts, adminA, "ea", "boxA"); rev != 1 {
		t.Fatalf("revision = %d", rev)
	}

	aWS := dialEdgeHello(t, ts, "ea", edgeA, api.DeviceMeta{ID: "d1", Adapter: "stcb"})
	defer aWS.CloseNow()
	aCh := edgeReader(aWS)
	linkA := waitEdgeLink(t, srv, "ea", a)
	if _, ok := waitEnv(t, aCh, api.MsgPluginDesired, 30*time.Second); !ok {
		t.Fatal("a 未收到 desired")
	}
	writeEnv(t, aWS, api.Envelope{V: api.Version, Type: api.MsgPluginStatus, Ts: time.Now().Unix(),
		Data: rawData(t, api.PluginStatusData{BootID: "boot-a", Sequence: 1,
			ObservedInstances: []api.PluginObservedInstanceData{{
				InstanceID: "boxA", PluginID: "p1", Version: "1.0.0", State: "HEALTHY", Health: "HEALTHY"}}})})

	// tenant-b 伪造 tenant-a 的 edge id：hello 阶段 fail-closed，不驱逐 a。
	forged := dialEdgeHello(t, ts, "ea", edgeB, api.DeviceMeta{ID: "d1", Adapter: "stcb"})
	expectEdgeRejected(t, forged)
	srv.mu.RLock()
	stillA := srv.edges["ea"] == linkA
	srv.mu.RUnlock()
	if !stillA {
		t.Fatal("伪造上报驱逐了 tenant-a 的连接")
	}

	// tenant-b 用自己的 edge 上报：只能写进 b 的投影。
	bWS := dialEdgeHello(t, ts, "eb", edgeB, api.DeviceMeta{ID: "d2", Adapter: "stcb"})
	defer bWS.CloseNow()
	bCh := edgeReader(bWS)
	waitEdgeLink(t, srv, "eb", b)
	if _, ok := waitEnv(t, bCh, api.MsgPluginDesired, 30*time.Second); !ok {
		t.Fatal("b 未收到 desired（空快照也应下发）")
	}
	writeEnv(t, bWS, api.Envelope{V: api.Version, Type: api.MsgPluginStatus, Ts: time.Now().Unix(),
		Data: rawData(t, api.PluginStatusData{BootID: "boot-b", Sequence: 1,
			ObservedInstances: []api.PluginObservedInstanceData{{
				InstanceID: "boxA", PluginID: "evil", Version: "6.6.6", State: "CRASHED", Health: "UNKNOWN"}}})})
	time.Sleep(400 * time.Millisecond)

	srv.plugin.mu.Lock()
	ta, tb := srv.plugin.tenants[a], srv.plugin.tenants[b]
	var aState, bState string
	if ta != nil {
		if ep, ok := ta.edges["ea"]; ok {
			if o, ok := ep.observed["boxA"]; ok {
				aState = o.State
			}
		}
	}
	if tb != nil {
		if ep, ok := tb.edges["eb"]; ok {
			if o, ok := ep.observed["boxA"]; ok {
				bState = o.State + "/" + o.PluginID
			}
		}
	}
	srv.plugin.mu.Unlock()
	if aState != "HEALTHY" {
		t.Fatalf("tenant-a 投影被 tenant-b 改写: %q", aState)
	}
	if bState != "CRASHED/evil" {
		t.Fatalf("tenant-b 投影未落在自己作用域: %q", bState)
	}
	// b 的读面看不到 a 的实例，a 的读面看不到 b 的上报。
	resp := pluginREST(t, ts, issueTenantToken(t, st, b, `["admin"]`), http.MethodGet, "/api/plugin-instances", "")
	raw := readBody(t, resp)
	if strings.Contains(raw, "io.github.acme.driver") {
		t.Fatalf("tenant-b 读面泄漏 tenant-a 实例: %s", raw)
	}
}

// TestPluginAckAdvancesOnlyOnApplied 锁定 §4.3：只有 applied 推进 applied_revision；
// rejected/failed 保持上一完整 revision 并记失败审计；同 revision 不同摘要拒绝并审计协议异常。
func TestPluginAckAdvancesOnlyOnApplied(t *testing.T) {
	st, srv, ts, mem, a, _ := setupPluginSync(t)
	admin := issueTenantToken(t, st, a, `["admin"]`)
	edgeTok := issueTenantToken(t, st, a, `["edge"]`)
	if rev := createInstance(t, ts, admin, "e1", "box1"); rev != 1 {
		t.Fatalf("revision = %d", rev)
	}
	ews := dialEdgeHello(t, ts, "e1", edgeTok, api.DeviceMeta{ID: "d1", Adapter: "stcb"})
	defer ews.CloseNow()
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
	ack := func(revision uint64, status, digest string, results []api.PluginApplyResultData) {
		t.Helper()
		writeEnv(t, ews, api.Envelope{V: api.Version, Type: api.MsgPluginAck, Ts: time.Now().Unix(),
			Data: rawData(t, api.PluginAckData{Revision: revision, SnapshotDigest: digest,
				Status: status, Results: results})})
	}
	appliedRev := func() uint64 {
		t.Helper()
		row, err := mem.GetPluginEdgeRevision(a, "e1")
		if err != nil {
			t.Fatal(err)
		}
		return row.AppliedRevision
	}
	waitApplied := func(want uint64) {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			if appliedRev() == want {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("applied_revision = %d, want %d", appliedRev(), want)
	}

	// 1) rejected：不推进 applied_revision，记失败审计与逐实例结果。
	ack(1, api.PluginAckRejected, desired.SnapshotDigest, []api.PluginApplyResultData{
		{InstanceID: "box1", Status: "failed", Detail: "manifest missing"},
	})
	time.Sleep(400 * time.Millisecond)
	if got := appliedRev(); got != 0 {
		t.Fatalf("rejected 却推进了 applied_revision = %d", got)
	}
	actions := auditActions(t, st, a)
	if !hasAudit(actions, actionPluginRejected+":failure") {
		t.Fatalf("rejected 未记失败审计: %v", actions)
	}
	// 2) 同 revision 不同摘要：协议异常，拒绝推进并审计。
	ack(1, api.PluginAckApplied, "sha256:"+strings.Repeat("f", 64), nil)
	time.Sleep(400 * time.Millisecond)
	if got := appliedRev(); got != 0 {
		t.Fatalf("摘要不匹配却推进了 applied_revision = %d", got)
	}
	if !hasAudit(auditActions(t, st, a), actionPluginProtocol+":failure") {
		t.Fatalf("摘要不匹配未记协议异常审计: %v", auditActions(t, st, a))
	}
	// 3) 未知 revision（大于 desired）：忽略并审计协议异常。
	ack(99, api.PluginAckApplied, desired.SnapshotDigest, nil)
	time.Sleep(300 * time.Millisecond)
	if got := appliedRev(); got != 0 {
		t.Fatalf("未知 revision 却推进 = %d", got)
	}
	// 4) 正确 applied：推进 applied_revision，drift 消失。
	ack(1, api.PluginAckApplied, desired.SnapshotDigest, []api.PluginApplyResultData{
		{InstanceID: "box1", Status: "applied"},
	})
	waitApplied(1)
	if !hasAudit(auditActions(t, st, a), actionPluginApplied+":success") {
		t.Fatalf("applied 未记成功审计: %v", auditActions(t, st, a))
	}
	resp := pluginREST(t, ts, admin, http.MethodGet, "/api/plugin-instances/box1", "")
	raw := readBody(t, resp)
	var view api.PluginInstanceView
	if err := json.Unmarshal([]byte(raw), &view); err != nil {
		t.Fatalf("decode view: %v (%s)", err, raw)
	}
	if view.Drift || view.AppliedRevision != 1 || view.DesiredRevision != 1 {
		t.Fatalf("ack 后 drift/revision 错误: %+v", view)
	}
	// 5) 重复 applied ack 幂等：不重复推进、不改状态。
	ack(1, api.PluginAckApplied, desired.SnapshotDigest, nil)
	time.Sleep(300 * time.Millisecond)
	if got := appliedRev(); got != 1 {
		t.Fatalf("重复 ack 改变了 applied_revision = %d", got)
	}
	// 6) failed 状态同样不推进。
	if resp := pluginREST(t, ts, admin, http.MethodPatch, "/api/plugin-instances/box1",
		`{"enabled":false}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("patch = %d", resp.StatusCode)
	}
	ack(2, api.PluginAckFailed, "", []api.PluginApplyResultData{{InstanceID: "box1", Status: "failed"}})
	time.Sleep(400 * time.Millisecond)
	if got := appliedRev(); got != 1 {
		t.Fatalf("failed 却推进了 applied_revision = %d, want 1", got)
	}
}

// TestPluginMessagesIgnoredWhenStoreUnwired 锁定向后兼容：PluginStore 未接线时
// plugin_status/ack 被忽略且**不断开连接**，设备链路完全不受影响。
func TestPluginMessagesIgnoredWhenStoreUnwired(t *testing.T) {
	st, srv, ts, a, _ := setupIdentityTenants(t)
	edgeTok := issueTenantToken(t, st, a, `["edge"]`)
	if srv.PluginControlPlaneWired() {
		t.Fatal("未注入 PluginStore 却报告已接线")
	}
	ews := dialEdgeHello(t, ts, "e1", edgeTok, api.DeviceMeta{ID: "d1", Adapter: "stcb"})
	defer ews.CloseNow()
	ch := edgeReader(ews)
	waitEdgeLink(t, srv, "e1", a)
	if _, ok := waitEnv(t, ch, api.MsgPluginDesired, 300*time.Millisecond); ok {
		t.Fatal("未接线却下发了 plugin_desired")
	}
	writeEnv(t, ews, api.Envelope{V: api.Version, Type: api.MsgPluginStatus, Ts: time.Now().Unix(),
		Data: rawData(t, api.PluginStatusData{BootID: "b1", Sequence: 1})})
	writeEnv(t, ews, api.Envelope{V: api.Version, Type: api.MsgPluginAck, Ts: time.Now().Unix(),
		Data: rawData(t, api.PluginAckData{Revision: 1, Status: api.PluginAckApplied})})
	// 连接必须存活：随后的设备状态照常生效。
	writeEnv(t, ews, api.Envelope{V: api.Version, Type: api.MsgState, Device: "e1/d1",
		Ts: time.Now().Unix(), Data: rawData(t, api.StateData{Online: true, Raw: map[string]any{"k": 1}, UpdatedAt: time.Now().Unix()})})
	waitDeviceOnline(t, srv, "e1/d1")
}
