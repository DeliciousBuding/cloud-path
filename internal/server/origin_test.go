package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func dialWithOrigin(t *testing.T, url, origin string) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	opts := &websocket.DialOptions{}
	if origin != "" {
		h := http.Header{}
		h.Set("Origin", origin)
		opts.HTTPHeader = h
	}
	ws, _, err := websocket.Dial(ctx, url, opts)
	if err == nil {
		ws.CloseNow()
	}
	return err
}

// 未配置 AllowedOrigins = 开发策略：放行 localhost 任意端口，拒绝外站跨源。
func TestWSOriginPolicyDevDefault(t *testing.T) {
	srv := New(Config{Version: "test"})
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()
	defer srv.CloseAll()
	url := wsURL(ts.URL, "/ws")

	if err := dialWithOrigin(t, url, "http://localhost:5173"); err != nil {
		t.Fatalf("localhost dev origin 应放行: %v", err)
	}
	if err := dialWithOrigin(t, url, ""); err != nil {
		t.Fatalf("非浏览器客户端（无 Origin）应放行: %v", err)
	}
	if err := dialWithOrigin(t, url, "http://evil.example"); err == nil {
		t.Fatal("外站跨源应被拒绝")
	}
}

// 显式配置 AllowedOrigins 后只放行清单内 Origin（公网部署形态）。
func TestWSOriginPolicyExplicit(t *testing.T) {
	srv := New(Config{Version: "test", AllowedOrigins: []string{"console.example.com"}})
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()
	defer srv.CloseAll()
	url := wsURL(ts.URL, "/ws")

	if err := dialWithOrigin(t, url, "https://console.example.com"); err != nil {
		t.Fatalf("清单内 Origin 应放行: %v", err)
	}
	if err := dialWithOrigin(t, url, "http://localhost:5173"); err == nil {
		t.Fatal("配置清单后 localhost 不再默认放行")
	}
	if err := dialWithOrigin(t, url, "https://console.example.com.evil.test"); err == nil {
		t.Fatal("后缀伪装 Origin 应被拒绝")
	}
	// edge 通道同样受策略保护
	if err := dialWithOrigin(t, wsURL(ts.URL, "/ws/edge"), "http://evil.example"); err == nil {
		t.Fatal("edge 通道外站跨源应被拒绝")
	}
}
