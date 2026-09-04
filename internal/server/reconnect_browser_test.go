package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"

	_ "github.com/DeliciousBuding/cloud-path/examples/demo"
	"github.com/DeliciousBuding/cloud-path/internal/api"
)

// dialWithToken 用租户令牌拨号浏览器 WS（账号模式下 handleBrowserWS 走 Bearer 身份）。
func dialWithToken(t *testing.T, url, token string) *websocket.Conn {
	t.Helper()
	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer "+token)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		t.Fatalf("dial %s: %v", url, err)
	}
	t.Cleanup(func() { ws.CloseNow() })
	return ws
}

// collectTypes 在窗口内收集浏览器收到的信封类型序列（按到达顺序）。
func collectTypes(t *testing.T, ch <-chan api.Envelope, within time.Duration) []api.Envelope {
	t.Helper()
	var out []api.Envelope
	deadline := time.After(within)
	for {
		select {
		case env, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, env)
		case <-deadline:
			return out
		}
	}
}

// TestBrowserSeesReconnectAsOnline 锁定验收场景的 UI 侧事实：
// 一台 Edge 断开时浏览器看到 edge_down + 设备离线，另一台不受影响；
// 重连后浏览器看到 edge_up 并重新看到设备在线（真实 fan-out，不是快照兜底）。
func TestBrowserSeesReconnectAsOnline(t *testing.T) {
	st, srv, ts, a, _ := setupMultiEdge(t)
	edgeTok := issueTenantToken(t, st, a, `["edge"]`)
	readTok := issueTenantToken(t, st, a, `["read"]`)

	bws := dialWithToken(t, wsURL(ts.URL, "/ws"), readTok)
	bch := edgeReader(bws)
	if _, ok := waitEnv(t, bch, api.MsgSnapshot, 30*time.Second); !ok {
		t.Fatal("浏览器未收到首帧快照")
	}

	ws1 := dialEdgeHello(t, ts, "e1", edgeTok, api.DeviceMeta{ID: "d1", Adapter: "demo"})
	ws2 := dialEdgeHello(t, ts, "e2", edgeTok, api.DeviceMeta{ID: "d1", Adapter: "demo"})
	defer ws2.CloseNow()
	waitEdgeLink(t, srv, "e1", a)
	waitEdgeLink(t, srv, "e2", a)
	reportOnline(t, ws1, "e1/d1", map[string]any{"clock": "10:00"})
	reportOnline(t, ws2, "e2/d1", map[string]any{"clock": "11:00"})
	waitDeviceOnline(t, srv, "e1/d1")
	waitDeviceOnline(t, srv, "e2/d1")

	upA, ok := waitEnv(t, bch, api.MsgEdgeUp, 30*time.Second)
	if !ok || upA.Device == "" {
		t.Fatalf("浏览器未收到 edge_up: %+v ok=%v", upA, ok)
	}
	// 排空到当前，随后只看断线/重连产生的事件。
	collectTypes(t, bch, 300*time.Millisecond)

	// e1 断线：浏览器必须看到 edge_down 与 e1/d1 离线，且 e2/d1 不受影响。
	ws1.CloseNow()
	waitEdgeOffline(t, srv, "e1")
	downSeen, offlineSeen := false, false
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) && !(downSeen && offlineSeen) {
		envs := collectTypes(t, bch, 200*time.Millisecond)
		for _, env := range envs {
			if env.Type == api.MsgEdgeDown && env.Device == "e1" {
				downSeen = true
			}
			if env.Type == api.MsgState && env.Device == "e1/d1" {
				var st api.StateData
				if err := json.Unmarshal(env.Data, &st); err == nil && !st.Online {
					offlineSeen = true
				}
			}
			if env.Type == api.MsgState && env.Device == "e2/d1" {
				var st api.StateData
				if err := json.Unmarshal(env.Data, &st); err == nil && !st.Online {
					t.Fatalf("e1 断线把 e2/d1 也广播成离线: %+v", env)
				}
			}
		}
	}
	if !downSeen || !offlineSeen {
		t.Fatalf("断线事件未完整到达浏览器: down=%v offline=%v", downSeen, offlineSeen)
	}

	// e1 重连：浏览器必须重新看到 edge_up 与 e1/d1 在线。
	ws1b := dialEdgeHello(t, ts, "e1", edgeTok, api.DeviceMeta{ID: "d1", Adapter: "demo"})
	defer ws1b.CloseNow()
	waitEdgeLink(t, srv, "e1", a)
	upSeen, onlineSeen := false, false
	deadline = time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) && !(upSeen && onlineSeen) {
		envs := collectTypes(t, bch, 200*time.Millisecond)
		for _, env := range envs {
			if env.Type == api.MsgEdgeUp && env.Device == "e1" {
				upSeen = true
			}
			if env.Type == api.MsgState && env.Device == "e1/d1" {
				var st api.StateData
				if err := json.Unmarshal(env.Data, &st); err == nil && st.Online {
					onlineSeen = true
				}
			}
		}
	}
	if !upSeen {
		t.Fatal("重连后浏览器未收到 edge_up")
	}
	if !onlineSeen {
		t.Fatal("重连后浏览器未看到设备重新在线（UI 会一直显示离线）")
	}
	// 新连上的浏览器首帧快照也必须显示 e1/d1 在线（REST/快照一致性）。
	bws2 := dialWithToken(t, wsURL(ts.URL, "/ws"), readTok)
	snap, ok := waitEnv(t, edgeReader(bws2), api.MsgSnapshot, 30*time.Second)
	if !ok {
		t.Fatal("第二个浏览器未收到快照")
	}
	var sd api.SnapshotData
	if err := json.Unmarshal(snap.Data, &sd); err != nil {
		t.Fatal(err)
	}
	online := map[string]bool{}
	for _, d := range sd.Devices {
		online[d.ID] = d.Online
	}
	if !online["e1/d1"] || !online["e2/d1"] {
		t.Fatalf("快照未反映重连后的真实在线态: %+v", sd.Devices)
	}
	bws2.CloseNow()
}
