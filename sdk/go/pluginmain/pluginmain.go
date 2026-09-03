// Package pluginmain is the common executable entrypoint helper for CloudPath
// process plugins. It reads the launch identity the A4 Plugin Host injects
// through the process environment, renders the single handshake line the host
// parses with pluginhost.ParseHandshake, dials the host's loopback endpoint,
// and serves an SDK RPC server over that authenticated transport.
//
// This package is public and must never import internal/pluginhost, so the
// handshake format and environment contract are defined here independently and
// locked by the tests in this package.
package pluginmain

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/DeliciousBuding/cloud-path/sdk/go/pluginruntime"
	"github.com/DeliciousBuding/cloud-path/sdk/go/rpc"
	"github.com/DeliciousBuding/cloud-path/sdk/go/transport"
)

// Environment variables injected by the A4 Plugin Host. These names mirror the
// host contract in internal/pluginhost/handshake.go.
const (
	EnvPluginID        = "CLOUDPATH_PLUGIN_ID"
	EnvProtocol        = "CLOUDPATH_PROTOCOL"
	EnvProtocolVersion = "CLOUDPATH_PROTOCOL_VERSION"
	EnvLaunchID        = "CLOUDPATH_LAUNCH_ID"
	EnvProof           = "CLOUDPATH_PROOF"
	EnvPluginEndpoint  = "CLOUDPATH_PLUGIN_ENDPOINT"
)

// HandshakeMarker is the fixed first field of a plugin handshake line.
const HandshakeMarker = "CP1"

// Config is the validated launch identity and endpoint for one plugin process.
type Config struct {
	PluginID        string
	Protocol        string
	ProtocolVersion uint32
	LaunchID        string
	Proof           string
	Endpoint        pluginruntime.Endpoint
}

// FromEnv reads and validates the host-injected environment. getenv is
// os.Getenv in production and a map lookup in tests. Missing or malformed
// values fail fast; error messages never contain secret values such as Proof.
func FromEnv(getenv func(string) string) (Config, error) {
	var c Config
	c.PluginID = getenv(EnvPluginID)
	if c.PluginID == "" {
		return c, fmt.Errorf("pluginmain: %s is required", EnvPluginID)
	}
	c.Protocol = getenv(EnvProtocol)
	switch c.Protocol {
	case "driver", "application":
	default:
		return c, fmt.Errorf("pluginmain: %s must be driver or application", EnvProtocol)
	}
	version, err := strconv.ParseUint(getenv(EnvProtocolVersion), 10, 32)
	if err != nil || version == 0 {
		return c, fmt.Errorf("pluginmain: %s must be a positive integer", EnvProtocolVersion)
	}
	c.ProtocolVersion = uint32(version)
	c.LaunchID = getenv(EnvLaunchID)
	if c.LaunchID == "" {
		return c, fmt.Errorf("pluginmain: %s is required", EnvLaunchID)
	}
	c.Proof = getenv(EnvProof)
	if c.Proof == "" {
		return c, fmt.Errorf("pluginmain: %s is required", EnvProof)
	}
	endpoint, err := pluginruntime.ParseEndpoint(getenv(EnvPluginEndpoint))
	if err != nil {
		return c, fmt.Errorf("pluginmain: %s: %w", EnvPluginEndpoint, err)
	}
	c.Endpoint = endpoint
	return c, nil
}

// Handshake is the public mirror of the host's parsed handshake record. Its
// String form is the canonical handshake line.
type Handshake struct {
	Marker          string
	PluginID        string
	Protocol        string
	ProtocolVersion uint32
	Transport       string
	Endpoint        string
	RPC             string
	LaunchID        string
	Proof           string
}

// String renders the canonical eight-field handshake line:
//
//	CP1|<plugin-id>|<protocol>=<version>|<transport>|<endpoint>|<rpc>|<launch-id>|<proof>
func (h Handshake) String() string {
	marker := h.Marker
	if marker == "" {
		marker = HandshakeMarker
	}
	rpcName := h.RPC
	if rpcName == "" {
		rpcName = "grpc"
	}
	return fmt.Sprintf("%s|%s|%s=%d|%s|%s|%s|%s|%s",
		marker, h.PluginID, h.Protocol, h.ProtocolVersion,
		h.Transport, h.Endpoint, rpcName, h.LaunchID, h.Proof)
}

// Handshake builds the handshake for the configured launch. The transport and
// endpoint fields are the host endpoint the plugin is about to dial, matching
// what the host handed out in the environment.
func (c Config) Handshake() Handshake {
	return Handshake{
		Marker:          HandshakeMarker,
		PluginID:        c.PluginID,
		Protocol:        c.Protocol,
		ProtocolVersion: c.ProtocolVersion,
		Transport:       c.Endpoint.Scheme,
		Endpoint:        c.Endpoint.Addr,
		RPC:             "grpc",
		LaunchID:        c.LaunchID,
		Proof:           c.Proof,
	}
}

// Dial connects to the host endpoint and authenticates with the launch
// credentials. The returned transport is ready for the SDK RPC server and must
// be closed by the caller.
func (c Config) Dial(ctx context.Context) (transport.Transport, error) {
	return pluginruntime.Dial(ctx, c.Endpoint.String(), pluginruntime.Credentials{
		LaunchID: c.LaunchID,
		Proof:    c.Proof,
	}, pluginruntime.DefaultConfig())
}

// ServerFactory builds the protocol RPC server bound to an authenticated
// transport.
type ServerFactory func(tr transport.Transport) *rpc.Server

// Serve dials the host, builds the RPC server with newServer, serves frames
// until ctx is canceled or the transport closes, and then closes the transport.
// A canceled context is reported as nil: signal-driven shutdown is the normal
// plugin exit path.
func Serve(ctx context.Context, cfg Config, newServer ServerFactory) error {
	tr, err := cfg.Dial(ctx)
	if err != nil {
		return err
	}
	defer tr.Close()

	// Closing the transport on cancellation unblocks a pending Recv, which
	// otherwise waits on a fixed read deadline rather than on ctx.Done.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = tr.Close()
		case <-done:
		}
	}()

	if err := newServer(tr).Serve(ctx); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	return nil
}

// Run is the standard plugin entrypoint: it validates the environment, prints
// the unique handshake line to out, dials the host, serves the RPC server and
// closes the transport. Diagnostics are written to errw; Proof is never written
// to out or errw except as the final field of the single handshake line.
func Run(ctx context.Context, out, errw io.Writer, newServer ServerFactory) error {
	cfg, err := FromEnv(os.Getenv)
	if err != nil {
		fmt.Fprintf(errw, "pluginmain: %v\n", err)
		return err
	}
	if _, err := fmt.Fprintln(out, cfg.Handshake().String()); err != nil {
		return err
	}
	if err := Serve(ctx, cfg, newServer); err != nil {
		fmt.Fprintf(errw, "pluginmain: %v\n", err)
		return err
	}
	return nil
}
