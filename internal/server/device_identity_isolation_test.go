package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"

	_ "github.com/DeliciousBuding/cloud-path/examples/stcb"
	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/auth"
	"github.com/DeliciousBuding/cloud-path/internal/store"
)

// setupIdentityTenants 构造带 tenant-a/tenant-b 的真实 store/server，并返回两个租户 id。
func setupIdentityTenants(t *testing.T) (*store.Store, *Server, *httptest.Server, int64, int64) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "identity.db"))
	if err != nil {
		t.Fatal(err)
	}
	ensure := func(slug string) int64 {
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
	a := ensure("tenant-a")
	b := ensure("tenant-b")
	srv := New(Config{Store: st, Version: "test", RequireAuth: true})
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(func() { ts.Close(); srv.CloseAll(); time.Sleep(50 * time.Millisecond) })
	t.Cleanup(func() { st.Close() })
	return st, srv, ts, a, b
}

// issueTenantToken 为指定租户签发一枚服务令牌（scopes 为 JSON 数组；返回明文仅测试内使用）。
func issueTenantToken(t *testing.T, st *store.Store, tenantID int64, scopes string) string {
	t.Helper()
	plain, hash, prefix, err := auth.NewTenantToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateTenantToken(tenantID, "tok", hash, prefix, scopes, nil); err != nil {
		t.Fatal(err)
	}
	return plain
}

// dialEdgeHello 建立真实 WS 连接并发送 edge hello（走 handleEdgeWS 全链路）。
func dialEdgeHello(t *testing.T, ts *httptest.Server, edgeID, token string, devices ...api.DeviceMeta) *websocket.Conn {
	t.Helper()
	ws := dial(t, wsURL(ts.URL, "/ws/edge"))
	writeEnv(t, ws, api.Envelope{
		V: api.Version, Type: api.MsgHello, Ts: time.Now().Unix(),
		Data: rawData(t, api.HelloData{EdgeID: edgeID, Token: token, Version: "test", Devices: devices}),
	})
	return ws
}

// expectEdgeRejected 断言 edge 被服务端以 policy violation 断开（fail-closed）。
func expectEdgeRejected(t *testing.T, ws *websocket.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, _, err := ws.Read(ctx); err == nil {
		t.Fatal("edge 应被拒绝，却仍保持连接")
	} else if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatalf("断开 close status = %v, want policy violation", websocket.CloseStatus(err))
	}
	ws.CloseNow()
}

// waitEdgeLink 等待指定租户的 edge 连接注册可见并返回其 link。
func waitEdgeLink(t *testing.T, srv *Server, edgeID string, tenantID int64) *edgeLink {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		srv.mu.RLock()
		l := srv.edges[edgeID]
		if l != nil && l.tenantID == tenantID {
			srv.mu.RUnlock()
			return l
		}
		srv.mu.RUnlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("edge %q tenant %d 未在 5s 内注册", edgeID, tenantID)
	return nil
}

// waitDeviceOnline 等待设备内存态上线（真实 WS state 消息已应用）。
func waitDeviceOnline(t *testing.T, srv *Server, key string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		srv.mu.RLock()
		v := srv.devices[key]
		online := v != nil && v.Online
		srv.mu.RUnlock()
		if online {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("设备 %q 未上线", key)
}

// TestRejectCrossTenantEdgeIDCollision tenant-a 绑定 e1 后，tenant-b 同名 edge 必须被拒，
// 且不能覆盖内存连接、改写设备归属或落库到 tenant-b。
func TestRejectCrossTenantEdgeIDCollision(t *testing.T) {
	st, srv, ts, a, b := setupIdentityTenants(t)
	tokenA := issueTenantToken(t, st, a, `["edge"]`)
	tokenB := issueTenantToken(t, st, b, `["edge"]`)
	devs := []api.DeviceMeta{{ID: "d1", Adapter: "stcb", Name: "A", Port: "COM1"}}

	aWS := dialEdgeHello(t, ts, "e1", tokenA, devs...)
	waitEdgeLink(t, srv, "e1", a)
	defer aWS.CloseNow()

	bWS := dialEdgeHello(t, ts, "e1", tokenB, devs...)
	expectEdgeRejected(t, bWS)

	srv.mu.RLock()
	link := srv.edges["e1"]
	devTenant := srv.deviceTenants["e1/d1"]
	srv.mu.RUnlock()
	if link == nil || link.tenantID != a {
		t.Fatalf("tenant-b 覆盖/驱逐了 tenant-a 的 edge 连接: %+v", link)
	}
	if devTenant != "tenant-a" {
		t.Fatalf("设备归属 = %q, want tenant-a", devTenant)
	}
	rowsA, err := st.ListDevicesTenant(a)
	if err != nil {
		t.Fatal(err)
	}
	rowsB, err := st.ListDevicesTenant(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(rowsA) != 1 || rowsA[0].ID != "e1/d1" || rowsA[0].TenantID != a {
		t.Fatalf("tenant-a devices = %+v", rowsA)
	}
	if len(rowsB) != 0 {
		t.Fatalf("tenant-b 不应拥有 e1/d1: %+v", rowsB)
	}
}

// TestCrossTenantCollisionCannotEvictEdge tenant-b 同名注册不得把 tenant-a 的连接挤下线、
// 不得把其设备标离线，tenant-a 仍可正常接收命令。
func TestCrossTenantCollisionCannotEvictEdge(t *testing.T) {
	st, srv, ts, a, b := setupIdentityTenants(t)
	tokenA := issueTenantToken(t, st, a, `["edge"]`)
	tokenB := issueTenantToken(t, st, b, `["edge"]`)
	devs := []api.DeviceMeta{{ID: "d1", Adapter: "stcb"}}

	aWS := dialEdgeHello(t, ts, "e1", tokenA, devs...)
	linkA := waitEdgeLink(t, srv, "e1", a)
	defer aWS.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	writeEnv(t, aWS, api.Envelope{
		V: api.Version, Type: api.MsgState, Device: "e1/d1", Ts: time.Now().Unix(),
		Data: rawData(t, api.StateData{Online: true, Raw: map[string]any{"x": 1}, UpdatedAt: time.Now().Unix()}),
	})
	waitDeviceOnline(t, srv, "e1/d1")

	bWS := dialEdgeHello(t, ts, "e1", tokenB, devs...)
	expectEdgeRejected(t, bWS)

	srv.mu.RLock()
	link := srv.edges["e1"]
	v := srv.devices["e1/d1"]
	online := v != nil && v.Online
	srv.mu.RUnlock()
	if link != linkA {
		t.Fatal("tenant-b 注册驱逐了 tenant-a 的连接")
	}
	if !online {
		t.Fatalf("tenant-b 注册把 tenant-a 设备标离线: present=%v", v != nil)
	}

	writeTokenA := issueTenantToken(t, st, a, `["write"]`)
	if resp := doJSON(t, http.MethodPost, ts.URL+"/api/devices/e1/d1/commands",
		`{"cmd":"sync"}`, bearerJSON(writeTokenA), nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("tenant-a 下发命令 = %d, want 200", resp.StatusCode)
	}
	if _, err := readEnvUntil(ctx, aWS, api.MsgCommand); err != nil {
		t.Fatalf("tenant-a 未收到命令（连接被挤掉）: %v", err)
	}
}

// TestDeviceTenantCannotBeReassigned 设备归属一旦建立：store 单条/批量 upsert 与 WS 注册
// 都必须 fail-closed，不得静默重归属或留半绑定行。
func TestDeviceTenantCannotBeReassigned(t *testing.T) {
	st, srv, ts, a, b := setupIdentityTenants(t)
	tokenA := issueTenantToken(t, st, a, `["edge"]`)
	tokenB := issueTenantToken(t, st, b, `["edge"]`)

	// 1) store 单设备 upsert：跨租户报错且原行不变。
	if err := st.UpsertDeviceTenant("e1/d1", "e1", "stcb", "A", "COM1", a); err != nil {
		t.Fatal(err)
	}
	err := st.UpsertDeviceTenant("e1/d1", "e1", "stcb", "B", "COM2", b)
	if !errors.Is(err, store.ErrDeviceTenantMismatch) {
		t.Fatalf("跨租户 upsert err = %v, want ErrDeviceTenantMismatch", err)
	}
	rowsA, err := st.ListDevicesTenant(a)
	if err != nil {
		t.Fatal(err)
	}
	rowsB, err := st.ListDevicesTenant(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(rowsA) != 1 || rowsA[0].Name != "A" || rowsA[0].TenantID != a {
		t.Fatalf("tenant-a 设备被改写: %+v", rowsA)
	}
	if len(rowsB) != 0 {
		t.Fatalf("tenant-b 不应有设备: %+v", rowsB)
	}

	// 2) 批量事务：edge 身份冲突必须整体回滚（不得留下 e1/d2 半绑定行）。
	err = st.UpsertDevicesTenant("e1", []store.DeviceMetaInput{
		{ID: "e1/d2", Adapter: "stcb", Name: "B2"},
	}, b)
	if !errors.Is(err, store.ErrEdgeTenantMismatch) {
		t.Fatalf("跨租户 edge 批量 upsert err = %v, want ErrEdgeTenantMismatch", err)
	}
	if rows, err := st.ListDevicesTenant(b); err != nil || len(rows) != 0 {
		t.Fatalf("批量失败后残留 tenant-b 行: %+v err=%v", rows, err)
	}

	// 3) server/WS：tenant-b 同名 edge 注册被拒；tenant-a 同租户注册仍成功且归属不变。
	bWS := dialEdgeHello(t, ts, "e1", tokenB, api.DeviceMeta{ID: "d1", Adapter: "stcb"})
	expectEdgeRejected(t, bWS)
	aWS := dialEdgeHello(t, ts, "e1", tokenA, api.DeviceMeta{ID: "d1", Adapter: "stcb"})
	waitEdgeLink(t, srv, "e1", a)
	aWS.CloseNow()
	srv.mu.RLock()
	devTenant := srv.deviceTenants["e1/d1"]
	srv.mu.RUnlock()
	if devTenant != "tenant-a" {
		t.Fatalf("设备被重归属: %q, want tenant-a", devTenant)
	}
}

// TestCrossTenantCollisionCannotCommandDevice tenant-b 主休对 tenant-a 设备下发命令必须 404，
// 且 tenant-a 的连接不得收到任何命令；tenant-a 自身命令不受影响。
func TestCrossTenantCollisionCannotCommandDevice(t *testing.T) {
	st, srv, ts, a, b := setupIdentityTenants(t)
	tokenA := issueTenantToken(t, st, a, `["edge"]`)
	devs := []api.DeviceMeta{{ID: "d1", Adapter: "stcb"}}

	aWS := dialEdgeHello(t, ts, "e1", tokenA, devs...)
	waitEdgeLink(t, srv, "e1", a)
	defer aWS.CloseNow()

	writeTokenB := issueTenantToken(t, st, b, `["write"]`)
	if resp := doJSON(t, http.MethodPost, ts.URL+"/api/devices/e1/d1/commands",
		`{"cmd":"sync"}`, bearerJSON(writeTokenB), nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("跨租户命令 = %d, want 404", resp.StatusCode)
	}

	// 用独立读循环观察 tenant-a 连接（避免带 deadline 的读破坏真实 WS 状态）。
	gotCmd := make(chan api.Envelope, 4)
	go func() {
		for {
			_, data, err := aWS.Read(context.Background())
			if err != nil {
				return
			}
			var env api.Envelope
			if err := json.Unmarshal(data, &env); err == nil && env.Type == api.MsgCommand {
				gotCmd <- env
			}
		}
	}()

	// tenant-a 的连接在观察窗口内不得收到命令。
	select {
	case env := <-gotCmd:
		t.Fatalf("tenant-a 收到跨租户命令: %+v", env)
	case <-time.After(300 * time.Millisecond):
	}

	// tenant-a 自己下发命令正常送达。
	writeTokenA := issueTenantToken(t, st, a, `["write"]`)
	if resp := doJSON(t, http.MethodPost, ts.URL+"/api/devices/e1/d1/commands",
		`{"cmd":"sync"}`, bearerJSON(writeTokenA), nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("tenant-a 下发命令 = %d, want 200", resp.StatusCode)
	}
	select {
	case <-gotCmd:
	case <-time.After(30 * time.Second):
		t.Fatal("tenant-a 未收到命令")
	}
}

// TestStaleDisconnectCannotClearCurrentConnection 同租户重连后，旧连接退出时不得删除
// 当前连接或把当前连接拥有的设备标离线，命令仍投递给新连接。
func TestStaleDisconnectCannotClearCurrentConnection(t *testing.T) {
	st, srv, ts, a, _ := setupIdentityTenants(t)
	tokenA := issueTenantToken(t, st, a, `["edge"]`)
	devs := []api.DeviceMeta{{ID: "d1", Adapter: "stcb"}}

	oldWS := dialEdgeHello(t, ts, "e1", tokenA, devs...)
	waitEdgeLink(t, srv, "e1", a)
	writeEnv(t, oldWS, api.Envelope{
		V: api.Version, Type: api.MsgState, Device: "e1/d1", Ts: time.Now().Unix(),
		Data: rawData(t, api.StateData{Online: true, Raw: map[string]any{"v": 1}, UpdatedAt: time.Now().Unix()}),
	})
	waitDeviceOnline(t, srv, "e1/d1")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 同租户新连接挤掉旧连接。
	newWS := dialEdgeHello(t, ts, "e1", tokenA, devs...)
	if _, _, err := oldWS.Read(ctx); err == nil {
		t.Fatal("旧连接应被挤掉")
	}
	// 给旧连接 defer 足够时间执行；其清理必须跳过当前（新）连接。
	time.Sleep(300 * time.Millisecond)

	srv.mu.RLock()
	link := srv.edges["e1"]
	v := srv.devices["e1/d1"]
	online := v != nil && v.Online
	srv.mu.RUnlock()
	if link == nil || link.tenantID != a {
		t.Fatalf("新连接被旧连接 defer 清掉: %+v", link)
	}
	if !online {
		t.Fatalf("旧连接 defer 把设备标离线: present=%v", v != nil)
	}

	// 命令仍投递给当前（新）连接。
	writeTokenA := issueTenantToken(t, st, a, `["write"]`)
	if resp := doJSON(t, http.MethodPost, ts.URL+"/api/devices/e1/d1/commands",
		`{"cmd":"sync"}`, bearerJSON(writeTokenA), nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("命令下发 = %d, want 200", resp.StatusCode)
	}
	if _, err := readEnvUntil(ctx, newWS, api.MsgCommand); err != nil {
		t.Fatalf("新连接未收到命令: %v", err)
	}
	oldWS.CloseNow()
	newWS.CloseNow()
}
