// Command cloudpath-driver-template is the reference entrypoint for the
// template Driver Plugin. It reads the launch identity from the environment,
// emits the CloudPath handshake line on stdout, then serves the Driver
// Protocol v1 over a loopback TCP transport accepted from the host.
//
// Environment (set by the CloudPath Plugin Host, see
// docs/architecture/plugin-system.md section 4):
//
//	CLOUDPATH_PLUGIN_ID          plugin id echoed back in the handshake
//	CLOUDPATH_PROTOCOL           "driver"
//	CLOUDPATH_PROTOCOL_VERSION   protocol version (e.g. 1)
//	CLOUDPATH_LAUNCH_ID          launch identity
//	CLOUDPATH_HANDSHAKE_COOKIE   one-time launch proof
//
// The handshake directive and transport framing are the seam a real A4 Plugin
// Host must implement on its side. The in-memory transport (transport.Pipe)
// remains the canonical test transport; this adapter is for running the
// binary against a host.
package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/DeliciousBuding/cloud-path-plugin-template-go/driver/nettransport"
	"github.com/DeliciousBuding/cloud-path-plugin-template-go/driver/plugin"
	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/driver"
)

const (
	envPluginID        = "CLOUDPATH_PLUGIN_ID"
	envProtocol        = "CLOUDPATH_PROTOCOL"
	envProtocolVersion = "CLOUDPATH_PROTOCOL_VERSION"
	envLaunchID        = "CLOUDPATH_LAUNCH_ID"
	envHandshakeCookie = "CLOUDPATH_HANDSHAKE_COOKIE"
)

func main() {
	pluginID := os.Getenv(envPluginID)
	if pluginID == "" {
		pluginID = plugin.PluginID
	}
	launchID := os.Getenv(envLaunchID)
	proof := os.Getenv(envHandshakeCookie)

	protocol := os.Getenv(envProtocol)
	if protocol == "" {
		protocol = "driver"
	}
	ver, err := strconv.ParseUint(os.Getenv(envProtocolVersion), 10, 32)
	if err != nil || ver == 0 {
		ver = uint64(driver.ProtocolVersion)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintf(os.Stderr, "template driver: listen: %v\n", err)
		os.Exit(1)
	}
	defer ln.Close()

	// Single handshake line on stdout; the host reads it to validate the
	// launch identity and learn the endpoint.
	fmt.Printf("CP1|%s|%s=%d|tcp|%s|grpc|%s|%s\n", pluginID, protocol, ver, ln.Addr().String(), launchID, proof)
	_ = os.Stdout.Sync()

	conn, err := ln.Accept()
	if err != nil {
		fmt.Fprintf(os.Stderr, "template driver: accept: %v\n", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	tr := nettransport.New(conn)
	srv := driver.NewRPCServer(tr, plugin.New())
	_ = srv.Serve(ctx)
}
