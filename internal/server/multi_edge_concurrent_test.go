package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	_ "github.com/DeliciousBuding/cloud-path/examples/stcb"
	"github.com/DeliciousBuding/cloud-path/internal/api"
)

// writeEnvRaw 是不调用 t.Fatalf 的 WS 写入：并发 goroutine 里绝不能在非测试
// goroutine 上 Fatal，错误一律交回主 goroutine 断言。
func writeEnvRaw(ws *websocket.Conn, env api.Envelope) error {
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return ws.Write(ctx, websocket.MessageText, data)
}

// mustJSON 是并发路径用的序列化助手（失败即测试代码 bug，直接 panic 暴露）。
func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// TestConcurrentMultiEdgeCommandRouting 锁定多 Edge 并发场景：3 台 Edge 同时在线、
// 并发上报状态、并发下发 6 条命令，每台 Edge 只收到自己设备的命令；
// 同时验证 s.mu 与插件控制面锁并存时不死锁、聚合读面在并发写入后仍真实一致。
func TestConcurrentMultiEdgeCommandRouting(t *testing.T) {
	st, srv, ts, a, _ := setupMultiEdge(t)
	edgeTok := issueTenantToken(t, st, a, `["edge"]`)
	writeTok := issueTenantToken(t, st, a, `["write"]`)
	readTok := issueTenantToken(t, st, a, `["read"]`)

	const edges = 3
	devs := []string{"d1", "d2"}
	edgeIDs := make([]string, edges)
	conns := make([]*websocket.Conn, edges)
	chans := make([]<-chan api.Envelope, edges)
	for i := 0; i < edges; i++ {
		idx := i
		edgeIDs[i] = fmt.Sprintf("ce%d", i+1)
		conns[i] = dialEdgeHello(t, ts, edgeIDs[i], edgeTok,
			api.DeviceMeta{ID: "d1", Adapter: "stcb"}, api.DeviceMeta{ID: "d2", Adapter: "stcb"})
		t.Cleanup(func() { conns[idx].CloseNow() })
		chans[i] = edgeReader(conns[i])
		waitEdgeLink(t, srv, edgeIDs[i], a)
	}

	// 1) 并发状态上报（3 Edge × 2 设备）。
	errCh := make(chan error, edges*len(devs))
	var wg sync.WaitGroup
	for i := 0; i < edges; i++ {
		for _, dev := range devs {
			wg.Add(1)
			go func(conn *websocket.Conn, key string) {
				defer wg.Done()
				errCh <- writeEnvRaw(conn, api.Envelope{V: api.Version, Type: api.MsgState,
					Device: key, Ts: time.Now().Unix(),
					Data: mustJSON(api.StateData{Online: true, Raw: map[string]any{"clock": key},
						UpdatedAt: time.Now().Unix()})})
			}(conns[i], edgeIDs[i]+"/"+dev)
		}
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("并发状态上报失败: %v", err)
		}
	}
	for i := 0; i < edges; i++ {
		for _, dev := range devs {
			waitDeviceOnline(t, srv, edgeIDs[i]+"/"+dev)
		}
	}

	// 2) 并发下发 6 条命令。
	type postResult struct {
		key  string
		code int
		body string
	}
	results := make(chan postResult, edges*len(devs))
	var wg2 sync.WaitGroup
	for i := 0; i < edges; i++ {
		for _, dev := range devs {
			wg2.Add(1)
			go func(key string) {
				defer wg2.Done()
				req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/devices/"+key+"/commands",
					strings.NewReader(`{"cmd":"sync"}`))
				if err != nil {
					results <- postResult{key: key, code: -1, body: err.Error()}
					return
				}
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", "Bearer "+writeTok)
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					results <- postResult{key: key, code: -1, body: err.Error()}
					return
				}
				raw, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				results <- postResult{key: key, code: resp.StatusCode, body: string(raw)}
			}(edgeIDs[i] + "/" + dev)
		}
	}
	wg2.Wait()
	close(results)
	for r := range results {
		if r.code != http.StatusOK {
			t.Fatalf("并发命令 %s = %d body=%s", r.key, r.code, r.body)
		}
	}

	// 3) 每台 Edge 收到的命令集合必须恰好是自己的两个设备键。
	for i := 0; i < edges; i++ {
		got := map[string]int{}
		deadline := time.After(3 * time.Second)
	collect:
		for len(got) < len(devs) {
			select {
			case env, ok := <-chans[i]:
				if !ok {
					break collect
				}
				if env.Type == api.MsgCommand {
					got[env.Device]++
				}
			case <-deadline:
				break collect
			}
		}
		for _, dev := range devs {
			want := edgeIDs[i] + "/" + dev
			if got[want] != 1 {
				t.Fatalf("edge %s 收到 %s 的命令 %d 次, want 1（实际收到=%v）", edgeIDs[i], want, got[want], got)
			}
		}
		if len(got) != len(devs) {
			t.Fatalf("edge %s 收到了别的设备的命令: %v", edgeIDs[i], got)
		}
	}

	// 4) 聚合读面在并发写入后仍然真实一致。
	view, raw := getOverview(t, ts, readTok)
	if view.DevicesTotal != edges*len(devs) || view.DevicesOnline != edges*len(devs) ||
		view.EdgesTotal != edges || view.EdgesOnline != edges {
		t.Fatalf("并发后 overview 计数错误: %+v (%s)", view, raw)
	}
	if view.CommandsFailed != 0 {
		t.Fatalf("并发后出现失败命令: %+v", view.FailedCommands)
	}
}
