package pluginhost

import (
	"context"
	"testing"
	"time"

	"github.com/DeliciousBuding/cloud-path/sdk/go/transport"
)

func TestSessionReadyWaitsForConnectionAccounting(t *testing.T) {
	conn, peer := transport.Pipe(1)
	defer peer.Close()
	session := newRuntimeSession(KindDriver)
	entered, release, accounted := make(chan struct{}), make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- session.handler(func() {
			close(entered)
			select {
			case <-release:
				close(accounted)
			case <-ctx.Done():
			}
		})(ctx, conn)
	}()
	t.Cleanup(func() {
		cancel()
		_ = session.close()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("handler shutdown: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("handler did not stop")
		}
	})
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("connection accounting was not called")
	}
	select {
	case <-session.established:
		t.Fatal("session became ready before connection accounting completed")
	default:
	}
	close(release)
	select {
	case <-session.established:
	case <-time.After(time.Second):
		t.Fatal("session did not publish readiness after accounting")
	}
	select {
	case <-accounted:
	default:
		t.Fatal("readiness did not synchronize accounting")
	}
	if session.driverClient() == nil {
		t.Fatal("ready session has no Driver client")
	}
	duplicate, duplicatePeer := transport.Pipe(1)
	defer duplicatePeer.Close()
	err := session.handler(func() { t.Error("duplicate connection was counted") })(ctx, duplicate)
	if err != errDuplicateConnection {
		t.Fatalf("duplicate connection error = %v", err)
	}
}
