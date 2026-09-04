package edge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/device"
	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/driver"
)

// fakeHost 是注入 edge.Run 的外部 Driver Plugin Host 替身。
// closed 默认 false：edge 若没有关闭 host（孤儿），断言 closed==true 就会失败。
type fakeHost struct {
	mu         sync.Mutex
	startCount int
	startedErr error
	closed     bool
	closeErr   error
	driverIDs  []string
	idsErr     error
}

func (h *fakeHost) Start(context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.startCount++
	return h.startedErr
}

func (h *fakeHost) Run(ctx context.Context) error {
	<-ctx.Done()
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	return h.closeErr
}

func (h *fakeHost) DriverIDs() ([]string, error) { return h.driverIDs, h.idsErr }

// DriverClient 在纯生命周期测试里不可用：返回错误即安全（这些测试不会真的桥接数据流）。
func (h *fakeHost) DriverClient(string) (driver.DriverClient, error) {
	return nil, errors.New("fakeHost: no driver client")
}

func (h *fakeHost) starts() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.startCount
}

func (h *fakeHost) isClosed() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.closed
}

var fakeAdapterSeq atomic.Int64

type fakeAdapter struct {
	name   string
	opened atomic.Int64
}

func (a *fakeAdapter) Name() string                { return a.name }
func (a *fakeAdapter) SupportedCommands() []string { return nil }
func (a *fakeAdapter) Open(ctx context.Context, cfg device.Config, _ func(device.Event)) (device.Device, error) {
	a.opened.Add(1)
	return &fakeDevice{id: cfg.ID, done: make(chan struct{})}, nil
}

type fakeDevice struct {
	id   string
	done chan struct{}
}

func (d *fakeDevice) ID() string { return d.id }
func (d *fakeDevice) Snapshot() device.State {
	return device.State{Online: true, Raw: map[string]any{"ok": true}, UpdatedAt: time.Now()}
}
func (d *fakeDevice) Send(context.Context, device.Command) error { return nil }
func (d *fakeDevice) Done() <-chan struct{}                      { return d.done }
func (d *fakeDevice) Close() error                               { return nil }

func registerFakeAdapter(t *testing.T) *fakeAdapter {
	t.Helper()
	a := &fakeAdapter{name: fmt.Sprintf("edgehosttest%d", fakeAdapterSeq.Add(1))}
	device.Register(a)
	return a
}

func testEdgeConfig(required bool) *Config {
	return &Config{
		Server:          "ws://127.0.0.1:1/ws/edge",
		EdgeID:          "e1",
		PollIntervalS:   3600,
		SyncIntervalS:   3600,
		ReportIntervalS: 30,
		Devices:         []DeviceCfg{{ID: "d1", Adapter: "placeholder", Name: "fake", Port: "COM3", Baud: 9600}},
		PluginHost:      PluginHostCfg{Enabled: true, Required: required},
	}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

func TestEdgeStartsAndClosesHost(t *testing.T) {
	a := registerFakeAdapter(t)
	host := &fakeHost{driverIDs: []string{"ext1"}}
	cfg := testEdgeConfig(false)
	cfg.Devices[0].Adapter = a.name

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg, "test", WithPluginHost(host)) }()

	waitFor(t, 30*time.Second, func() bool { return host.starts() == 1 })
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Run 未在取消后返回")
	}
	if !host.isClosed() {
		t.Fatal("edge 退出后 host 应已关闭")
	}
}

func TestRequiredHostFailureStopsEdge(t *testing.T) {
	a := registerFakeAdapter(t)
	host := &fakeHost{startedErr: errors.New("boom")}
	cfg := testEdgeConfig(true) // required
	cfg.Devices[0].Adapter = a.name

	err := Run(context.Background(), cfg, "test", WithPluginHost(host))
	if err == nil {
		t.Fatal("required host 失败应终止 edge")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("错误应包含根因: %v", err)
	}
	if a.opened.Load() != 0 {
		t.Fatal("required host 失败时内置设备不应启动")
	}
}

func TestOptionalHostFailureKeepsBuiltins(t *testing.T) {
	a := registerFakeAdapter(t)
	host := &fakeHost{startedErr: errors.New("boom")}
	cfg := testEdgeConfig(false) // optional
	cfg.Devices[0].Adapter = a.name

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg, "test", WithPluginHost(host)) }()

	waitFor(t, 30*time.Second, func() bool { return a.opened.Load() > 0 })
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("optional host 失败时 edge 应继续运行: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Run 未在取消后返回")
	}
	if a.opened.Load() == 0 {
		t.Fatal("optional host 失败时内置设备应继续运行")
	}
}

// TestEdgeCancellationLeavesNoHost 反向验证：若 edge 忘记关闭 host，
// fakeHost.closed 保持 false，本测试必须失败。
func TestEdgeCancellationLeavesNoHost(t *testing.T) {
	a := registerFakeAdapter(t)
	host := &fakeHost{driverIDs: []string{"ext1"}}
	cfg := testEdgeConfig(false)
	cfg.Devices[0].Adapter = a.name

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg, "test", WithPluginHost(host)) }()

	waitFor(t, 30*time.Second, func() bool { return host.starts() == 1 })
	cancel()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("Run 未返回")
	}
	if !host.isClosed() {
		t.Fatal("ctx 取消后 host 必须被关闭，不得留孤儿进程")
	}
}

func captureHello(t *testing.T) (wsURL string, hellos chan api.HelloData, shutdown func()) {
	t.Helper()
	hellos = make(chan api.HelloData, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
		if err != nil {
			return
		}
		defer ws.CloseNow()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, data, err := ws.Read(ctx)
		if err != nil {
			return
		}
		var env api.Envelope
		if err := json.Unmarshal(data, &env); err != nil || env.Type != api.MsgHello {
			return
		}
		var hello api.HelloData
		if err := json.Unmarshal(env.Data, &hello); err != nil {
			return
		}
		select {
		case hellos <- hello:
		default:
		}
		for {
			if _, _, err := ws.Read(ctx); err != nil {
				return
			}
		}
	}))
	return "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/edge", hellos, srv.Close
}

func TestHostOnlyDoesNotReportFakeDevice(t *testing.T) {
	a := registerFakeAdapter(t)
	wsURL, hellos, shutdown := captureHello(t)
	defer shutdown()

	host := &fakeHost{driverIDs: []string{"ext1"}}
	cfg := testEdgeConfig(false)
	cfg.Server = wsURL
	cfg.Devices[0].Adapter = a.name

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg, "test", WithPluginHost(host)) }()

	var hello api.HelloData
	select {
	case hello = <-hellos:
	case <-time.After(30 * time.Second):
		t.Fatal("未收到 edge hello")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("Run 未返回")
	}

	if len(hello.Devices) != 1 {
		t.Fatalf("hello 设备数 = %d, want 1（外部 driver 不得伪装成设备）: %+v", len(hello.Devices), hello.Devices)
	}
	got := hello.Devices[0]
	if got.ID != "d1" || got.Adapter != a.name {
		t.Fatalf("hello 应只含内置设备，got %+v", got)
	}
	for _, d := range hello.Devices {
		if d.Adapter == "ext1" {
			t.Fatal("外部 driver 不得作为 fake 设备上报")
		}
	}
}
