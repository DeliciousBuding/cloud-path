package pluginruntime_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DeliciousBuding/cloud-path/sdk/go/pluginruntime"
	"github.com/DeliciousBuding/cloud-path/sdk/go/rpc"
	"github.com/DeliciousBuding/cloud-path/sdk/go/transport"
)

var testCreds = pluginruntime.Credentials{LaunchID: "launch-1", Proof: "proof-1"}

func echoHandler(ctx context.Context, conn transport.Transport) error {
	for {
		m, err := conn.Recv(ctx)
		if err != nil {
			return err
		}
		if err := conn.Send(ctx, transport.Message{CallID: m.CallID, Kind: transport.KindResponse, Body: m.Body}); err != nil {
			return err
		}
	}
}

func startServe(t *testing.T, l *pluginruntime.Listener, handler pluginruntime.Handler) (context.Context, context.CancelFunc, chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- l.Serve(ctx, handler) }()
	return ctx, cancel, done
}

func stopServe(t *testing.T, cancel context.CancelFunc, done chan error) {
	t.Helper()
	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Serve returned %v, want context.Canceled or nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after cancel")
	}
}

func listenTCP(t *testing.T, cfg pluginruntime.Config) *pluginruntime.Listener {
	t.Helper()
	l, err := pluginruntime.Listen(context.Background(), "tcp://127.0.0.1:0", testCreds, cfg)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func TestParseEndpointRejectsRemoteTCP(t *testing.T) {
	rejected := []string{
		"tcp://0.0.0.0:8080",
		"tcp://127.0.0.2:8080",
		"tcp://192.168.1.10:8080",
		"tcp://10.0.0.1:8080",
		"tcp://8.8.8.8:8080",
		"tcp://[::]:8080",
		"tcp://[::1]:8080",
		"tcp://localhost:8080",
		"tcp://127.0.0.1",
		"tcp://127.0.0.1:99999",
		"tcp://127.0.0.1:8080/path",
		"tcp://user@127.0.0.1:8080",
		"tcp://127.0.0.1:8080?x=1",
		"tcp://127.0.0.1:8080#frag",
		"unix://relative.sock",
		"unix:///tmp/../etc/passwd",
		"unix:///tmp/./sock",
		"http://127.0.0.1:8080",
		"",
	}
	for _, raw := range rejected {
		if _, err := pluginruntime.ParseEndpoint(raw); err == nil {
			t.Errorf("ParseEndpoint(%q) succeeded, want error", raw)
		}
	}

	accepted := []string{
		"tcp://127.0.0.1:8080",
		"tcp://127.0.0.1:0",
		"unix:///tmp/plugin.sock",
	}
	for _, raw := range accepted {
		ep, err := pluginruntime.ParseEndpoint(raw)
		if err != nil {
			t.Errorf("ParseEndpoint(%q): %v", raw, err)
			continue
		}
		if got := ep.String(); got != raw {
			t.Errorf("ParseEndpoint(%q).String() = %q, want %q", raw, got, raw)
		}
	}

	t.Run("listen rejects 0.0.0.0", func(t *testing.T) {
		if _, err := pluginruntime.Listen(context.Background(), "tcp://0.0.0.0:0", testCreds, pluginruntime.DefaultConfig()); err == nil {
			t.Fatal("Listen(tcp://0.0.0.0:0) succeeded, want error")
		}
	})
}

func TestLoopbackListenDial(t *testing.T) {
	l := listenTCP(t, pluginruntime.DefaultConfig())
	ep := l.Endpoint()
	host, port, err := net.SplitHostPort(ep.Addr)
	if err != nil {
		t.Fatalf("endpoint %q is not host:port: %v", ep.Addr, err)
	}
	if host != "127.0.0.1" {
		t.Fatalf("endpoint host = %q, want 127.0.0.1", host)
	}
	if port == "" || port == "0" {
		t.Fatalf("endpoint port = %q, want an OS-assigned port", port)
	}

	ctx, cancel, done := startServe(t, l, echoHandler)
	defer stopServe(t, cancel, done)

	tr, err := pluginruntime.Dial(ctx, ep.String(), testCreds, pluginruntime.DefaultConfig())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer tr.Close()

	want := []byte(`"ping"`)
	if err := tr.Send(ctx, transport.Message{CallID: 1, Kind: transport.KindUnary, Method: "ping", Body: want}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	m, err := tr.Recv(ctx)
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if m.CallID != 1 || !bytes.Equal(m.Body, want) {
		t.Fatalf("got CallID=%d Body=%s, want CallID=1 Body=%s", m.CallID, m.Body, want)
	}
}

func TestRejectBadProofBeforeHandler(t *testing.T) {
	l := listenTCP(t, pluginruntime.DefaultConfig())
	ep := l.Endpoint()

	var calls atomic.Int32
	handler := func(ctx context.Context, conn transport.Transport) error {
		calls.Add(1)
		return nil
	}

	ctx, cancel, done := startServe(t, l, handler)

	bad := pluginruntime.Credentials{LaunchID: testCreds.LaunchID, Proof: "wrong-proof"}
	tr, err := pluginruntime.Dial(ctx, ep.String(), bad, pluginruntime.DefaultConfig())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer tr.Close()

	if _, err := tr.Recv(ctx); err == nil {
		t.Fatal("Recv after a bad proof succeeded, want connection close")
	}

	// A wrong proof must never reach the handler, even on a slow teardown.
	cancel()
	<-done
	if got := calls.Load(); got != 0 {
		t.Fatalf("handler called %d times, want 0", got)
	}
}

func TestCancelCleansListener(t *testing.T) {
	l := listenTCP(t, pluginruntime.DefaultConfig())
	ep := l.Endpoint()

	_, cancel, done := startServe(t, l, echoHandler)
	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Serve returned %v, want context.Canceled or nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after cancel")
	}

	if conn, err := net.DialTimeout("tcp", ep.Addr, time.Second); err == nil {
		conn.Close()
		t.Fatal("listener still accepting after Serve returned")
	}
}

func TestFrameSizeLimit(t *testing.T) {
	cfg := pluginruntime.DefaultConfig()
	cfg.MaxFrameSize = 256
	l := listenTCP(t, cfg)
	ep := l.Endpoint()

	ctx, cancel, done := startServe(t, l, echoHandler)
	defer stopServe(t, cancel, done)

	tr, err := pluginruntime.Dial(ctx, ep.String(), testCreds, cfg)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer tr.Close()

	err = tr.Send(ctx, transport.Message{CallID: 1, Kind: transport.KindUnary, Body: bytes.Repeat([]byte("x"), 512)})
	if !errors.Is(err, pluginruntime.ErrFrameTooLarge) {
		t.Fatalf("Send oversized frame = %v, want ErrFrameTooLarge", err)
	}
}

func TestConcurrentConnections(t *testing.T) {
	const n = 32
	cfg := pluginruntime.DefaultConfig()
	cfg.MaxConnections = n
	l := listenTCP(t, cfg)
	ep := l.Endpoint()

	ctx, cancel, done := startServe(t, l, echoHandler)
	defer stopServe(t, cancel, done)

	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tr, err := pluginruntime.Dial(ctx, ep.String(), testCreds, cfg)
			if err != nil {
				errs <- fmt.Errorf("conn %d dial: %w", i, err)
				return
			}
			defer tr.Close()

			body := []byte(fmt.Sprintf(`"payload-%d"`, i))
			if err := tr.Send(ctx, transport.Message{CallID: uint64(i), Kind: transport.KindUnary, Body: body}); err != nil {
				errs <- fmt.Errorf("conn %d send: %w", i, err)
				return
			}
			m, err := tr.Recv(ctx)
			if err != nil {
				errs <- fmt.Errorf("conn %d recv: %w", i, err)
				return
			}
			if !bytes.Equal(m.Body, body) {
				errs <- fmt.Errorf("conn %d got body %s, want %s", i, m.Body, body)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestHandshakeBridge(t *testing.T) {
	l := listenTCP(t, pluginruntime.DefaultConfig())
	ep := l.Endpoint()

	handler := func(ctx context.Context, conn transport.Transport) error {
		srv := rpc.NewServer(conn)
		srv.HandleUnary("ping", func(ctx context.Context, body []byte) (any, error) {
			var req struct {
				V string `json:"v"`
			}
			if err := json.Unmarshal(body, &req); err != nil {
				return nil, err
			}
			return map[string]string{"echo": req.V}, nil
		})
		return srv.Serve(ctx)
	}

	ctx, cancel, done := startServe(t, l, handler)
	defer stopServe(t, cancel, done)

	tr, err := pluginruntime.Dial(ctx, ep.String(), testCreds, pluginruntime.DefaultConfig())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer tr.Close()

	client := rpc.NewClient(tr)
	resp, err := client.Call(ctx, "ping", map[string]string{"v": "hello"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var out struct {
		Echo string `json:"echo"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		t.Fatalf("unmarshal response %s: %v", resp, err)
	}
	if out.Echo != "hello" {
		t.Fatalf("echo = %q, want hello", out.Echo)
	}
}

func TestUnixSocketCleanup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix socket file lifecycle is linux/macos-only; windows uses loopback TCP")
	}
	dir := t.TempDir()
	sock := filepath.Join(dir, "plugin.sock")

	l, err := pluginruntime.Listen(context.Background(), "unix://"+sock, testCreds, pluginruntime.DefaultConfig())
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	if _, err := os.Stat(sock); err != nil {
		t.Fatalf("socket file not created: %v", err)
	}

	_, cancel, done := startServe(t, l, func(ctx context.Context, conn transport.Transport) error { return nil })
	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Serve returned %v, want context.Canceled or nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after cancel")
	}

	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Fatalf("socket file still present after cancel: %v", err)
	}
}
