package pluginmain

import (
	"bytes"
	"context"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/application"
	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/status"
	"github.com/DeliciousBuding/cloud-path/sdk/go/pluginruntime"
	"github.com/DeliciousBuding/cloud-path/sdk/go/rpc"
	"github.com/DeliciousBuding/cloud-path/sdk/go/transport"
)

func validEnv() map[string]string {
	return map[string]string{
		EnvPluginID:        "io.test.app",
		EnvProtocol:        "application",
		EnvProtocolVersion: "1",
		EnvLaunchID:        "launch-1",
		EnvProof:           "proof-1",
		EnvPluginEndpoint:  "tcp://127.0.0.1:12345",
	}
}

func getenvOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestPluginMainRejectsMissingEnv(t *testing.T) {
	for _, key := range []string{
		EnvPluginID, EnvProtocol, EnvProtocolVersion, EnvLaunchID, EnvProof, EnvPluginEndpoint,
	} {
		t.Run(key, func(t *testing.T) {
			env := validEnv()
			delete(env, key)
			if _, err := FromEnv(getenvOf(env)); err == nil {
				t.Fatalf("FromEnv did not reject missing %s", key)
			}
		})
	}
}

func TestPluginMainRejectsInvalidEnv(t *testing.T) {
	cases := map[string]map[string]string{
		"bad protocol":          {"protocol": "connector"},
		"zero protocol version": {"version": "0"},
		"non-numeric version":   {"version": "x"},
		"empty proof":           {"proof": ""},
		"empty launch id":       {"launch": ""},
		"non-loopback tcp":      {"endpoint": "tcp://10.0.0.1:1234"},
		"bad scheme":            {"endpoint": "udp://127.0.0.1:1"},
	}
	for name, overrides := range cases {
		t.Run(name, func(t *testing.T) {
			env := validEnv()
			for k, v := range overrides {
				switch k {
				case "protocol":
					env[EnvProtocol] = v
				case "version":
					env[EnvProtocolVersion] = v
				case "proof":
					env[EnvProof] = v
				case "launch":
					env[EnvLaunchID] = v
				case "endpoint":
					env[EnvPluginEndpoint] = v
				}
			}
			if _, err := FromEnv(getenvOf(env)); err == nil {
				t.Fatalf("FromEnv accepted invalid env %q", name)
			}
		})
	}
}

// parseHandshakeLikeHost mirrors pluginhost.ParseHandshake so the public
// contract is locked without importing internal.
func parseHandshakeLikeHost(line string) ([]string, error) {
	parts := strings.Split(strings.TrimSpace(line), "|")
	if len(parts) != 8 {
		return nil, status.Errorf(status.CodeInvalidArgument, "want 8 fields, got %d", len(parts))
	}
	if parts[0] != HandshakeMarker {
		return nil, status.Errorf(status.CodeInvalidArgument, "bad marker %q", parts[0])
	}
	protocol, version, ok := strings.Cut(parts[2], "=")
	if !ok {
		return nil, status.Errorf(status.CodeInvalidArgument, "malformed protocol %q", parts[2])
	}
	switch protocol {
	case "driver", "application", "connector", "transform":
	default:
		return nil, status.Errorf(status.CodeInvalidArgument, "unsupported protocol %q", protocol)
	}
	n, err := strconv.ParseUint(version, 10, 32)
	if err != nil || n == 0 {
		return nil, status.Errorf(status.CodeInvalidArgument, "bad protocol version %q", version)
	}
	switch parts[3] {
	case "tcp", "unix", "bufconn":
	default:
		return nil, status.Errorf(status.CodeInvalidArgument, "unsupported transport %q", parts[3])
	}
	if parts[4] == "" {
		return nil, status.Errorf(status.CodeInvalidArgument, "empty endpoint")
	}
	if parts[3] == "tcp" {
		if _, _, err := net.SplitHostPort(parts[4]); err != nil {
			return nil, status.Errorf(status.CodeInvalidArgument, "invalid tcp endpoint %q", parts[4])
		}
	}
	if parts[5] != "grpc" {
		return nil, status.Errorf(status.CodeInvalidArgument, "unsupported rpc %q", parts[5])
	}
	if parts[6] == "" {
		return nil, status.Errorf(status.CodeInvalidArgument, "empty launch id")
	}
	if parts[7] == "" {
		return nil, status.Errorf(status.CodeInvalidArgument, "empty proof")
	}
	return parts, nil
}

func TestHandshakeFormatMatchesHostContract(t *testing.T) {
	cfg := Config{
		PluginID:        "io.test.app",
		Protocol:        "application",
		ProtocolVersion: 1,
		LaunchID:        "launch-1",
		Proof:           "proof-1",
		Endpoint:        pluginruntime.Endpoint{Scheme: "tcp", Addr: "127.0.0.1:12345"},
	}
	line := cfg.Handshake().String()
	parts, err := parseHandshakeLikeHost(line)
	if err != nil {
		t.Fatalf("handshake rejected by host-mirror parser: %v\nline: %s", err, line)
	}
	if parts[1] != cfg.PluginID || parts[2] != "application=1" || parts[3] != "tcp" ||
		parts[4] != "127.0.0.1:12345" || parts[5] != "grpc" || parts[6] != cfg.LaunchID || parts[7] != cfg.Proof {
		t.Fatalf("unexpected handshake fields: %q", parts)
	}
}

// countingApplicationServer counts every dispatched RPC call so tests can
// prove a rejected proof never reaches a service handler.
type countingApplicationServer struct {
	calls atomic.Int32
}

var _ application.ApplicationServer = (*countingApplicationServer)(nil)

func (s *countingApplicationServer) Initialize(context.Context, *application.InitializeRequest) (*application.InitializeResponse, error) {
	s.calls.Add(1)
	return &application.InitializeResponse{NegotiatedProtocolVersion: application.ProtocolVersion, Status: status.New()}, nil
}
func (s *countingApplicationServer) Describe(context.Context) (*application.ApplicationDescriptor, error) {
	s.calls.Add(1)
	return &application.ApplicationDescriptor{ApplicationID: "io.test.app"}, nil
}
func (s *countingApplicationServer) ConfigureInstance(context.Context, *application.ConfigureInstanceRequest) (*application.ConfigureInstanceResponse, error) {
	s.calls.Add(1)
	return &application.ConfigureInstanceResponse{Status: status.New()}, nil
}
func (s *countingApplicationServer) ValidateBinding(context.Context, *application.ValidateBindingRequest) (*application.ValidateBindingResponse, error) {
	s.calls.Add(1)
	return &application.ValidateBindingResponse{Valid: true}, nil
}
func (s *countingApplicationServer) HandleEvents(context.Context, application.ApplicationEventReader, application.ApplicationEffectWriter) error {
	s.calls.Add(1)
	return nil
}
func (s *countingApplicationServer) HandleRequest(context.Context, *application.PluginHTTPRequest) (*application.PluginHTTPResponse, error) {
	s.calls.Add(1)
	return &application.PluginHTTPResponse{}, nil
}
func (s *countingApplicationServer) RunJob(context.Context, *application.RunJobRequest) (*application.RunJobResponse, error) {
	s.calls.Add(1)
	return &application.RunJobResponse{}, nil
}
func (s *countingApplicationServer) Health(context.Context) (*application.HealthResponse, error) {
	s.calls.Add(1)
	return &application.HealthResponse{State: application.HealthStateServing}, nil
}
func (s *countingApplicationServer) Shutdown(context.Context, *application.ShutdownRequest) (*application.ShutdownResponse, error) {
	s.calls.Add(1)
	return &application.ShutdownResponse{Status: status.New()}, nil
}

func TestPluginMainDoesNotLeakProof(t *testing.T) {
	const proof = "secret-proof-value-do-not-leak"
	const correctProof = "correct-proof"

	creds := pluginruntime.Credentials{LaunchID: "launch-1", Proof: correctProof}
	ln, err := pluginruntime.Listen(context.Background(), "tcp://127.0.0.1:0", creds, pluginruntime.DefaultConfig())
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	var handlerCalls atomic.Int32
	serveCtx, serveCancel := context.WithCancel(context.Background())
	defer serveCancel()
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- ln.Serve(serveCtx, func(ctx context.Context, conn transport.Transport) error {
			handlerCalls.Add(1)
			_ = conn.Close()
			<-ctx.Done()
			return nil
		})
	}()

	t.Setenv(EnvPluginID, "io.test.app")
	t.Setenv(EnvProtocol, "application")
	t.Setenv(EnvProtocolVersion, "1")
	t.Setenv(EnvLaunchID, "launch-1")
	t.Setenv(EnvProof, proof) // tampered proof, rejected by the listener
	t.Setenv(EnvPluginEndpoint, ln.Endpoint().String())

	svc := &countingApplicationServer{}
	var out, errw bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := Run(ctx, &out, &errw, func(tr transport.Transport) *rpc.Server {
		return application.NewRPCServer(tr, svc)
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	serveCancel()
	select {
	case <-serveDone:
	case <-time.After(5 * time.Second):
		t.Fatal("listener did not stop")
	}

	if got := svc.calls.Load(); got != 0 {
		t.Fatalf("service call count = %d, want 0 (bad proof reached a handler)", got)
	}
	if got := handlerCalls.Load(); got != 0 {
		t.Fatalf("host handler call count = %d, want 0 (bad proof authenticated)", got)
	}
	if strings.Contains(errw.String(), proof) {
		t.Fatalf("stderr leaked the proof: %q", errw.String())
	}
	// The proof may appear exactly once: inside the mandatory handshake line.
	if got := strings.Count(out.String(), proof); got != 1 {
		t.Fatalf("stdout proof occurrences = %d, want exactly 1 (the handshake line); stdout=%q", got, out.String())
	}
	if _, err := parseHandshakeLikeHost(firstLine(out.String())); err != nil {
		t.Fatalf("handshake line is not contract-compliant: %v\nstdout=%q", err, out.String())
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
