package pluginhost

import (
	"context"
	"errors"
	"sync"

	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/application"
	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/driver"
	"github.com/DeliciousBuding/cloud-path/sdk/go/pluginruntime"
	"github.com/DeliciousBuding/cloud-path/sdk/go/transport"
)

// errDuplicateConnection is returned when a plugin opens more than one
// authenticated RPC connection for the same launch. The second connection is
// rejected without being handed to a client.
var errDuplicateConnection = errors.New("pluginhost: duplicate plugin connection")

// runtimeSession is the per-launch RPC session held by the Supervisor. It
// owns the authenticated transport and the protocol client built from it, and
// is the only component that may use the client for Health and Shutdown. The
// raw transport is never logged or exposed.
type runtimeSession struct {
	kind Kind

	mu          sync.Mutex
	tr          transport.Transport
	driverCli   driver.DriverClient
	appCli      application.ApplicationClient
	established chan struct{}
	closeOnce   sync.Once
	closed      bool
}

func newRuntimeSession(kind Kind) *runtimeSession {
	return &runtimeSession{
		kind:        kind,
		established: make(chan struct{}),
	}
}

// handler returns the pluginruntime handler that receives the plugin's
// authenticated connection. It builds the protocol client for the session
// kind and then keeps the connection open until ctx is canceled. A second
// connection is refused so a stale proof or a forked child cannot replace the
// established session.
func (s *runtimeSession) handler(onEstablished func()) pluginruntime.Handler {
	return func(ctx context.Context, conn transport.Transport) error {
		if !s.tryEstablish(conn) {
			_ = conn.Close()
			return errDuplicateConnection
		}
		if onEstablished != nil {
			onEstablished()
		}
		<-ctx.Done()
		return nil
	}
}

// tryEstablish installs conn as the session transport and builds the matching
// protocol client exactly once. It reports false when a session is already
// established.
func (s *runtimeSession) tryEstablish(conn transport.Transport) bool {
	s.mu.Lock()
	if s.tr != nil {
		s.mu.Unlock()
		return false
	}
	s.tr = conn
	switch s.kind {
	case KindApplication:
		s.appCli = application.NewClient(conn)
	default:
		s.driverCli = driver.NewClient(conn)
	}
	s.mu.Unlock()
	close(s.established)
	return true
}

// health probes the plugin over the established RPC client. A missing session,
// a transport error, or a not-serving response is reported as an error.
func (s *runtimeSession) health(ctx context.Context) error {
	s.mu.Lock()
	driverCli := s.driverCli
	appCli := s.appCli
	s.mu.Unlock()

	switch {
	case driverCli != nil:
		resp, err := driverCli.Health(ctx)
		if err != nil {
			return err
		}
		if resp.State == driver.HealthStateNotServing {
			return errors.New("pluginhost: plugin reports not serving")
		}
		return nil
	case appCli != nil:
		resp, err := appCli.Health(ctx)
		if err != nil {
			return err
		}
		if resp.State == application.HealthStateNotServing {
			return errors.New("pluginhost: plugin reports not serving")
		}
		return nil
	default:
		return errors.New("pluginhost: no RPC session established")
	}
}

// shutdown sends the protocol Shutdown RPC to the plugin before the host falls
// back to process signals.
func (s *runtimeSession) shutdown(ctx context.Context) error {
	s.mu.Lock()
	driverCli := s.driverCli
	appCli := s.appCli
	s.mu.Unlock()

	switch {
	case driverCli != nil:
		_, err := driverCli.Shutdown(ctx, &driver.ShutdownRequest{Reason: "host shutdown"})
		return err
	case appCli != nil:
		_, err := appCli.Shutdown(ctx, &application.ShutdownRequest{Reason: "host shutdown"})
		return err
	default:
		return nil
	}
}

// close tears down the transport if one was established. It is idempotent and
// safe to call before or after the listener has closed the connection.
func (s *runtimeSession) close() error {
	s.mu.Lock()
	tr := s.tr
	s.mu.Unlock()
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		if tr != nil {
			_ = tr.Close()
		}
	})
	return nil
}
