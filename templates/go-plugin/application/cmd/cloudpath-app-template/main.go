// Command cloudpath-app-template is the reference entrypoint for the template
// Application Plugin. It reads the launch identity the CloudPath Plugin Host
// injects through the environment, emits the single CloudPath handshake line,
// dials the host's loopback endpoint and serves the Application Protocol v1
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

	"github.com/DeliciousBuding/cloud-path-plugin-template-go/application/plugin"
	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/application"
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
		return application.NewRPCServer(tr, &shutdownAwareApp{App: plugin.New(), onShutdown: exitAfterShutdown})
	}); err != nil {
		os.Exit(1)
	}
}

type shutdownAwareApp struct {
	*plugin.App
	onShutdown func()
}

func (a *shutdownAwareApp) Shutdown(ctx context.Context, req *application.ShutdownRequest) (*application.ShutdownResponse, error) {
	resp, err := a.App.Shutdown(ctx, req)
	if a.onShutdown != nil {
		a.onShutdown()
	}
	return resp, err
}
