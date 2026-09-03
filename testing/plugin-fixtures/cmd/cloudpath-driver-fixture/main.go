// Command cloudpath-driver-fixture is a minimal simulated Driver plugin used by
// the process-host E2E tests. It is a real install-style binary: it reads the
// launch identity via pluginmain, dials the host's loopback endpoint, and
// serves the Driver Protocol v1.
package main

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/driver"
	"github.com/DeliciousBuding/cloud-path/sdk/go/pluginmain"
	"github.com/DeliciousBuding/cloud-path/sdk/go/rpc"
	"github.com/DeliciousBuding/cloud-path/sdk/go/transport"
	"github.com/DeliciousBuding/cloud-path/testing/plugin-fixtures"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var opts []pluginfixtures.Option
	if f := os.Getenv("CLOUDPATH_FIXTURE_EVENTS_FILE"); f != "" {
		opts = append(opts, pluginfixtures.WithEventsFile(f))
	}
	d := pluginfixtures.New(opts...)

	var once sync.Once
	d.OnShutdown = func() {
		once.Do(func() {
			go func() {
				// Give the RPC dispatcher a moment to flush the Shutdown
				// response before the process exits.
				time.Sleep(100 * time.Millisecond)
				stop()
			}()
		})
	}

	if raw := os.Getenv("CLOUDPATH_FIXTURE_EXIT_AFTER"); raw != "" {
		if dur, err := time.ParseDuration(raw); err == nil && dur > 0 {
			go func() {
				time.Sleep(dur)
				stop()
			}()
		}
	}

	if err := pluginmain.Run(ctx, os.Stdout, os.Stderr, func(tr transport.Transport) *rpc.Server {
		return driver.NewRPCServer(tr, d)
	}); err != nil {
		os.Exit(1)
	}
}
