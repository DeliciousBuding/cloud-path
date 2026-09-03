package pluginruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/DeliciousBuding/cloud-path/sdk/go/transport"
)

// Handler runs one authenticated plugin connection. The host/plugin caller
// typically wraps the returned transport.Transport with an SDK rpc.Client or
// rpc.Server and then calls Serve on it.
type Handler func(ctx context.Context, conn transport.Transport) error

// Listener is a bound local socket listener together with its resolved
// endpoint and expected launch credentials.
type Listener struct {
	ln        net.Listener
	endpoint  Endpoint
	creds     Credentials
	cfg       Config
	closeOnce sync.Once
}

// Listen binds a local endpoint and returns a ready listener. For
// tcp://127.0.0.1:0 the OS assigns a random loopback port, exposed through
// Listener.Endpoint.
func Listen(ctx context.Context, endpoint string, creds Credentials, cfg Config) (*Listener, error) {
	ep, err := ParseEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	if err := creds.Validate(); err != nil {
		return nil, err
	}
	cfg = cfg.withDefaults()
	if err := checkPlatform(ep); err != nil {
		return nil, err
	}

	var ln net.Listener
	switch ep.Scheme {
	case "tcp":
		ln, err = (&net.ListenConfig{}).Listen(ctx, "tcp", ep.Addr)
	case "unix":
		ln, err = listenUnix(ctx, ep.Addr)
	}
	if err != nil {
		return nil, err
	}

	// Resolve the concrete endpoint so callers can pass Listener.Endpoint()
	// straight into the plugin environment.
	switch a := ln.Addr().(type) {
	case *net.TCPAddr:
		ep = Endpoint{Scheme: "tcp", Addr: net.JoinHostPort("127.0.0.1", strconv.Itoa(a.Port))}
	case *net.UnixAddr:
		ep = Endpoint{Scheme: "unix", Addr: a.Name}
	}

	return &Listener{ln: ln, endpoint: ep, creds: creds, cfg: cfg}, nil
}

// Endpoint returns the resolved endpoint, including an OS-assigned TCP port.
func (l *Listener) Endpoint() Endpoint { return l.endpoint }

// Close closes the listener and removes the Unix socket file, if any.
func (l *Listener) Close() error {
	var err error
	l.closeOnce.Do(func() {
		err = l.ln.Close()
		if l.endpoint.Scheme == "unix" {
			if rmErr := os.Remove(l.endpoint.Addr); rmErr != nil && !os.IsNotExist(rmErr) {
				if err == nil {
					err = rmErr
				} else {
					err = fmt.Errorf("%w; remove unix socket: %v", err, rmErr)
				}
			}
		}
	})
	return err
}

// Serve accepts connections until ctx is canceled or the listener closes.
// Each accepted connection is authenticated against the listener's launch
// credentials before its handler runs, so a wrong proof never reaches an RPC
// handler. Connections are handled concurrently up to Config.MaxConnections;
// excess connections are rejected. On ctx cancellation Serve closes the
// listener and every active connection, removes the Unix socket file and
// returns ctx.Err.
func (l *Listener) Serve(ctx context.Context, handler Handler) error {
	if handler == nil {
		return errors.New("pluginruntime: nil handler")
	}

	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		conns = map[net.Conn]struct{}{}
	)
	sem := make(chan struct{}, l.cfg.MaxConnections)
	done := make(chan struct{})
	defer close(done)

	// Closing the listener is what unblocks Accept when ctx is canceled.
	go func() {
		select {
		case <-ctx.Done():
			_ = l.ln.Close()
		case <-done:
		}
	}()

	var serveErr error
accept:
	for {
		c, err := l.ln.Accept()
		if err != nil {
			switch {
			case ctx.Err() != nil:
				serveErr = ctx.Err()
			case errors.Is(err, net.ErrClosed):
				serveErr = nil
			default:
				serveErr = err
			}
			break accept
		}

		select {
		case sem <- struct{}{}:
		default:
			_ = c.Close()
			continue
		}

		mu.Lock()
		conns[c] = struct{}{}
		mu.Unlock()

		wg.Add(1)
		go func(conn net.Conn) {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() {
				mu.Lock()
				delete(conns, conn)
				mu.Unlock()
			}()
			defer conn.Close()
			_ = serveConn(ctx, conn, l.creds, l.cfg, handler)
		}(c)
	}

	_ = l.Close()
	mu.Lock()
	for c := range conns {
		_ = c.Close()
	}
	mu.Unlock()
	wg.Wait()
	return serveErr
}

// Dial connects to a local endpoint, authenticates with creds as the first
// frame and returns an established transport.Transport ready for the SDK rpc
// client. The returned transport must be closed by the caller.
func Dial(ctx context.Context, endpoint string, creds Credentials, cfg Config) (transport.Transport, error) {
	ep, err := ParseEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	if err := creds.Validate(); err != nil {
		return nil, err
	}
	cfg = cfg.withDefaults()
	if err := checkPlatform(ep); err != nil {
		return nil, err
	}
	if ep.Scheme == "tcp" && ep.tcpPort() == 0 {
		return nil, errors.New("pluginruntime: tcp dial requires a concrete port")
	}

	var d net.Dialer
	conn, err := d.DialContext(ctx, ep.Scheme, ep.Addr)
	if err != nil {
		return nil, err
	}

	auth, err := json.Marshal(authFrame{LaunchID: creds.LaunchID, Proof: creds.Proof})
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := conn.SetWriteDeadline(opDeadline(ctx, cfg.HandshakeTimeout)); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := writeFrame(conn, auth); err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = conn.SetWriteDeadline(time.Time{})
	return newSocketConn(conn, cfg), nil
}

func checkPlatform(ep Endpoint) error {
	if ep.Scheme == "unix" && runtime.GOOS == "windows" {
		return errors.New("pluginruntime: unix sockets are not available on windows")
	}
	return nil
}

func serveConn(ctx context.Context, conn net.Conn, creds Credentials, cfg Config, handler Handler) error {
	// The first frame is the client launch identity, read under a hard
	// handshake deadline before any RPC handler runs.
	_ = conn.SetReadDeadline(time.Now().Add(cfg.HandshakeTimeout))
	body, err := readFrame(conn, cfg.MaxFrameSize)
	if err != nil {
		return err
	}
	var auth authFrame
	if err := json.Unmarshal(body, &auth); err != nil {
		return fmt.Errorf("pluginruntime: decode auth frame: %w", err)
	}
	if !creds.verify(auth) {
		return ErrAuthenticationFailed
	}
	_ = conn.SetReadDeadline(time.Time{})
	return handler(ctx, newSocketConn(conn, cfg))
}

func listenUnix(ctx context.Context, p string) (net.Listener, error) {
	// Replace a stale socket, but never clobber a regular file or directory.
	if fi, err := os.Lstat(p); err == nil {
		if fi.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("pluginruntime: refusing to replace non-socket path %q", p)
		}
		if err := os.Remove(p); err != nil {
			return nil, fmt.Errorf("pluginruntime: remove stale socket %q: %w", p, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "unix", p)
	if err != nil {
		return nil, err
	}
	// Best-effort restrictive permissions for the launch-private socket.
	_ = os.Chmod(p, 0o600)
	return ln, nil
}
