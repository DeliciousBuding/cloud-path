// Command cloudpath-driver-template is the reference entrypoint for the
// template Driver Plugin. It reads the launch identity the CloudPath Plugin
// Host injects through the environment, emits the single CloudPath handshake
// line, dials the host's loopback endpoint and serves the Driver Protocol v1
// over that authenticated transport.
//
// The transport is injected by the host via pluginmain, so this binary never
// opens its own listener and never blocks forever waiting for a peer.
package main

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/DeliciousBuding/cloud-path-plugin-template-go/driver/plugin"
	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/driver"
	"github.com/DeliciousBuding/cloud-path/sdk/go/pluginmain"
	"github.com/DeliciousBuding/cloud-path/sdk/go/rpc"
	"github.com/DeliciousBuding/cloud-path/sdk/go/transport"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var once sync.Once
	exitAfterShutdown := func() {
		once.Do(func() {
			go func() {
				time.Sleep(100 * time.Millisecond)
				stop()
			}()
		})
	}

	if err := pluginmain.Run(ctx, os.Stdout, os.Stderr, func(tr transport.Transport) *rpc.Server {
		return driver.NewRPCServer(tr, &shutdownAwareDriver{Driver: plugin.New(), onShutdown: exitAfterShutdown})
	}); err != nil {
		os.Exit(1)
	}
}

type shutdownAwareDriver struct {
	*plugin.Driver
	onShutdown func()
}

func (d *shutdownAwareDriver) Shutdown(ctx context.Context, req *driver.ShutdownRequest) (*driver.ShutdownResponse, error) {
	resp, err := d.Driver.Shutdown(ctx, req)
	if d.onShutdown != nil {
		d.onShutdown()
	}
	return resp, err
}
