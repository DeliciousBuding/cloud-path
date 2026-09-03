// Command cloud-path-app-scheduled-compartment is the executable entrypoint for
// the Scheduled Compartment reference application.
//
// The Application Protocol is transport-agnostic (see sdk/go/transport). The
// A4 Plugin Host is responsible for injecting the real transport (named pipe,
// Unix socket or loopback TCP). Until that transport lands in the SDK, this
// command serves over an in-process transport so it is a real, buildable,
// smoke-testable binary.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/DeliciousBuding/cloud-path/examples/scheduled-compartment"
	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/application"
	"github.com/DeliciousBuding/cloud-path/sdk/go/transport"
)

func main() {
	svc := scheduledcompartment.New()

	serverEnd, _ := transport.Pipe(256)
	rpcServer := application.NewRPCServer(serverEnd, svc)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	fmt.Fprintf(os.Stderr, "%s %s: serving Application Protocol v1 over in-process transport\n",
		scheduledcompartment.ApplicationID(), scheduledcompartment.Version())
	if err := rpcServer.Serve(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "cloud-path-app-scheduled-compartment: %v\n", err)
		os.Exit(1)
	}
}
