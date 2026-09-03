package pluginhost_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/pluginhost"
	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/application"
	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/driver"
	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/status"
	"github.com/DeliciousBuding/cloud-path/sdk/go/pluginruntime"
)

// Helper-process environment switches. The test binary is re-executed with
// -test.run=^TestHelperProcess$ so no shell is involved.
const (
	helperProcessEnv  = "CLOUDPATH_HELPER_PROCESS"
	helperExitEarly   = "CLOUDPATH_HELPER_EXIT_EARLY"
	helperMismatch    = "CLOUDPATH_HELPER_MISMATCH"
	helperHealthFile  = "CLOUDPATH_HELPER_HEALTH_FILE"
	helperHealthMark  = "CLOUDPATH_HELPER_HEALTH_MARK"
	helperShutdownMrk = "CLOUDPATH_HELPER_SHUTDOWN_MARK"
)

// TestHelperProcess is the entrypoint for the re-executed helper binary.
func TestHelperProcess(t *testing.T) {
	if os.Getenv(helperProcessEnv) != "1" {
		return
	}
	os.Exit(helperMain())
}

func helperMain() int {
	if os.Getenv(helperExitEarly) == "1" {
		return 7
	}

	endpoint := os.Getenv(pluginhost.EnvPluginEndpoint)
	protocol := os.Getenv(pluginhost.EnvProtocol)
	pluginID := os.Getenv(pluginhost.EnvPluginID)
	version, _ := strconv.ParseUint(os.Getenv(pluginhost.EnvProtocolVersion), 10, 32)
	creds := pluginruntime.Credentials{
		LaunchID: os.Getenv(pluginhost.EnvLaunchID),
		Proof:    os.Getenv(pluginhost.EnvProof),
	}

	ep, err := pluginruntime.ParseEndpoint(endpoint)
	if err != nil {
		return 8
	}
	hs := pluginhost.Handshake{
		Marker:          pluginhost.HandshakeMarker,
		PluginID:        pluginID,
		Protocol:        protocol,
		ProtocolVersion: uint32(version),
		Transport:       ep.Scheme,
		Endpoint:        ep.Addr,
		RPC:             "grpc",
		LaunchID:        creds.LaunchID,
		Proof:           creds.Proof,
	}
	fmt.Println(hs.String())

	if os.Getenv(helperMismatch) == "1" {
		// Dial with a tampered proof. Dial only writes the auth frame, so the
		// transport is returned; the host listener must reject it and never
		// reach an RPC handler.
		bad := pluginruntime.Credentials{LaunchID: creds.LaunchID, Proof: creds.Proof + "tampered"}
		_, _ = pluginruntime.Dial(context.Background(), endpoint, bad, pluginruntime.DefaultConfig())
		select {}
	}

	tr, err := pluginruntime.Dial(context.Background(), endpoint, creds, pluginruntime.DefaultConfig())
	if err != nil {
		return 9
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	healthFile := os.Getenv(helperHealthFile)
	healthMark := os.Getenv(helperHealthMark)
	shutdownMark := os.Getenv(helperShutdownMrk)

	shutdownCh := make(chan struct{})
	shutdownOnce := sync.Once{}
	onShutdown := func() {
		shutdownOnce.Do(func() {
			if shutdownMark != "" {
				_ = os.WriteFile(shutdownMark, []byte("shutdown"), 0o644)
			}
			close(shutdownCh)
		})
	}

	serveDone := make(chan error, 1)
	if protocol == "application" {
		srv := application.NewRPCServer(tr, &helperApplicationServer{
			healthFile: healthFile,
			healthMark: healthMark,
			onShutdown: onShutdown,
		})
		go func() { serveDone <- srv.Serve(ctx) }()
	} else {
		srv := driver.NewRPCServer(tr, &helperDriverServer{
			healthFile: healthFile,
			healthMark: healthMark,
			onShutdown: onShutdown,
		})
		go func() { serveDone <- srv.Serve(ctx) }()
	}

	select {
	case <-shutdownCh:
		// Give the dispatcher a moment to flush the Shutdown response before
		// the process exits.
		time.Sleep(100 * time.Millisecond)
		return 0
	case <-serveDone:
		return 0
	}
}

func helperHealth(file, mark string) bool {
	if file != "" {
		if b, err := os.ReadFile(file); err == nil && strings.Contains(string(b), "degraded") {
			return false
		}
	}
	if mark != "" {
		_ = os.WriteFile(mark, []byte("healthy"), 0o644)
	}
	return true
}

type helperDriverServer struct {
	healthFile string
	healthMark string
	onShutdown func()
}

var _ driver.DriverServer = (*helperDriverServer)(nil)

func (h *helperDriverServer) Initialize(_ context.Context, _ *driver.InitializeRequest) (*driver.InitializeResponse, error) {
	return &driver.InitializeResponse{NegotiatedProtocolVersion: driver.ProtocolVersion, Status: status.New()}, nil
}
func (h *helperDriverServer) Describe(_ context.Context) (*driver.DriverDescriptor, error) {
	return &driver.DriverDescriptor{DriverID: "helper-driver"}, nil
}
func (h *helperDriverServer) ConfigureInstance(_ context.Context, _ *driver.ConfigureInstanceRequest) (*driver.ConfigureInstanceResponse, error) {
	return &driver.ConfigureInstanceResponse{Status: status.New()}, nil
}
func (h *helperDriverServer) Discover(_ context.Context, _ *driver.DiscoverRequest, _ driver.DiscoveryWriter) error {
	return nil
}
func (h *helperDriverServer) OpenDevice(_ context.Context, _ *driver.OpenDeviceRequest) (*driver.OpenDeviceResponse, error) {
	return &driver.OpenDeviceResponse{Status: status.New()}, nil
}
func (h *helperDriverServer) CloseDevice(_ context.Context, _ *driver.CloseDeviceRequest) (*driver.CloseDeviceResponse, error) {
	return &driver.CloseDeviceResponse{Status: status.New()}, nil
}
func (h *helperDriverServer) Watch(_ context.Context, _ *driver.WatchRequest, _ driver.DriverMessageWriter) error {
	return nil
}
func (h *helperDriverServer) Execute(_ context.Context, _ *driver.ExecuteRequest) (*driver.ExecuteResponse, error) {
	return &driver.ExecuteResponse{Status: status.New()}, nil
}
func (h *helperDriverServer) Health(_ context.Context) (*driver.HealthResponse, error) {
	if !helperHealth(h.healthFile, h.healthMark) {
		return &driver.HealthResponse{State: driver.HealthStateNotServing}, nil
	}
	return &driver.HealthResponse{State: driver.HealthStateServing}, nil
}
func (h *helperDriverServer) Shutdown(_ context.Context, _ *driver.ShutdownRequest) (*driver.ShutdownResponse, error) {
	if h.onShutdown != nil {
		h.onShutdown()
	}
	return &driver.ShutdownResponse{Status: status.New()}, nil
}

type helperApplicationServer struct {
	healthFile string
	healthMark string
	onShutdown func()
}

var _ application.ApplicationServer = (*helperApplicationServer)(nil)

func (h *helperApplicationServer) Initialize(_ context.Context, _ *application.InitializeRequest) (*application.InitializeResponse, error) {
	return &application.InitializeResponse{NegotiatedProtocolVersion: application.ProtocolVersion, Status: status.New()}, nil
}
func (h *helperApplicationServer) Describe(_ context.Context) (*application.ApplicationDescriptor, error) {
	return &application.ApplicationDescriptor{ApplicationID: "helper-app"}, nil
}
func (h *helperApplicationServer) ConfigureInstance(_ context.Context, _ *application.ConfigureInstanceRequest) (*application.ConfigureInstanceResponse, error) {
	return &application.ConfigureInstanceResponse{Status: status.New()}, nil
}
func (h *helperApplicationServer) ValidateBinding(_ context.Context, _ *application.ValidateBindingRequest) (*application.ValidateBindingResponse, error) {
	return &application.ValidateBindingResponse{Valid: true}, nil
}
func (h *helperApplicationServer) HandleEvents(_ context.Context, _ application.ApplicationEventReader, _ application.ApplicationEffectWriter) error {
	return nil
}
func (h *helperApplicationServer) HandleRequest(_ context.Context, _ *application.PluginHTTPRequest) (*application.PluginHTTPResponse, error) {
	return &application.PluginHTTPResponse{}, nil
}
func (h *helperApplicationServer) RunJob(_ context.Context, _ *application.RunJobRequest) (*application.RunJobResponse, error) {
	return &application.RunJobResponse{}, nil
}
func (h *helperApplicationServer) Health(_ context.Context) (*application.HealthResponse, error) {
	if !helperHealth(h.healthFile, h.healthMark) {
		return &application.HealthResponse{State: application.HealthStateNotServing}, nil
	}
	return &application.HealthResponse{State: application.HealthStateServing}, nil
}
func (h *helperApplicationServer) Shutdown(_ context.Context, _ *application.ShutdownRequest) (*application.ShutdownResponse, error) {
	if h.onShutdown != nil {
		h.onShutdown()
	}
	return &application.ShutdownResponse{Status: status.New()}, nil
}

// recordingRunner wraps a real Runner and records the environment injected
// into every process, plus Kill/Signal calls on the returned process handle.
type recordingRunner struct {
	mu    sync.Mutex
	inner pluginhost.Runner
	envs  []map[string]string
	procs []*recordingProcess
}

func newRecordingRunner() *recordingRunner {
	return &recordingRunner{inner: pluginhost.ExecRunner{}}
}

func (r *recordingRunner) Start(spec pluginhost.CommandSpec) (pluginhost.Process, error) {
	proc, err := r.inner.Start(spec)
	if err != nil {
		return nil, err
	}
	rp := &recordingProcess{Process: proc}
	r.mu.Lock()
	r.envs = append(r.envs, splitEnv(spec.Env))
	r.procs = append(r.procs, rp)
	r.mu.Unlock()
	return rp, nil
}

func (r *recordingRunner) StartCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.envs)
}

func (r *recordingRunner) Env(i int) map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if i < 0 || i >= len(r.envs) {
		return nil
	}
	return r.envs[i]
}

func (r *recordingRunner) Proc(i int) *recordingProcess {
	r.mu.Lock()
	defer r.mu.Unlock()
	if i < 0 || i >= len(r.procs) {
		return nil
	}
	return r.procs[i]
}

type recordingProcess struct {
	pluginhost.Process
	mu      sync.Mutex
	killed  bool
	signals []os.Signal
}

func (p *recordingProcess) Kill() error {
	p.mu.Lock()
	p.killed = true
	p.mu.Unlock()
	return p.Process.Kill()
}

func (p *recordingProcess) Signal(sig os.Signal) error {
	p.mu.Lock()
	p.signals = append(p.signals, sig)
	p.mu.Unlock()
	return p.Process.Signal(sig)
}

func (p *recordingProcess) Killed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.killed
}

func (p *recordingProcess) Signals() []os.Signal {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]os.Signal(nil), p.signals...)
}

func splitEnv(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[k] = v
		}
	}
	return m
}

func helperCommand() pluginhost.CommandSpec {
	return pluginhost.CommandSpec{
		Path: os.Args[0],
		Args: []string{"-test.run=^TestHelperProcess$"},
	}
}

func helperEnv(extra ...string) []string {
	return append(append([]string{}, os.Environ()...), append([]string{helperProcessEnv + "=1"}, extra...)...)
}

func rpcConfig(kind pluginhost.Kind) pluginhost.Config {
	return pluginhost.Config{
		PluginID:            "io.test.helper",
		Kind:                kind,
		Protocol:            kind.Protocol(),
		ProtocolVersion:     1,
		Command:             helperCommand(),
		HandshakeTimeout:    5 * time.Second,
		ShutdownTimeout:     5 * time.Second,
		HealthCheckInterval: 50 * time.Millisecond,
		MaxRestarts:         0,
		BaseBackoff:         10 * time.Millisecond,
		MaxBackoff:          50 * time.Millisecond,
		Jitter:              func(time.Duration) time.Duration { return 0 },
		LogBufferSize:       64,
	}
}

func TestHostStartsHelperOverSocket(t *testing.T) {
	cfg := rpcConfig(pluginhost.KindDriver)
	cfg.Command.Env = helperEnv()
	s := pluginhost.NewSupervisor(cfg, pluginhost.ExecRunner{}, discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	waitState(t, s, pluginhost.StateHealthy)
	snap := s.Snapshot()
	if !snap.HandshakeCompleted {
		t.Fatal("handshake was not completed")
	}
	if snap.RPCConnections != 1 {
		t.Fatalf("RPCConnections = %d, want 1", snap.RPCConnections)
	}
	if snap.Kind != pluginhost.KindDriver {
		t.Fatalf("Kind = %s, want driver", snap.Kind)
	}
	if runtime.GOOS == "windows" {
		if !strings.HasPrefix(snap.Endpoint, "tcp://127.0.0.1:") {
			t.Fatalf("endpoint = %q, want loopback tcp", snap.Endpoint)
		}
	} else if !strings.HasPrefix(snap.Endpoint, "unix://") {
		t.Fatalf("endpoint = %q, want unix socket", snap.Endpoint)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v after cancel, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestHostRejectsHandshakeCredentialMismatch(t *testing.T) {
	cfg := rpcConfig(pluginhost.KindDriver)
	cfg.Command.Env = helperEnv(helperMismatch + "=1")
	cfg.HandshakeTimeout = 500 * time.Millisecond
	s := pluginhost.NewSupervisor(cfg, pluginhost.ExecRunner{}, discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = s.Run(ctx) }()

	waitState(t, s, pluginhost.StateDisabled)
	snap := s.Snapshot()
	if snap.HandshakeCompleted {
		t.Fatal("handshake completed despite socket proof mismatch")
	}
	if snap.RPCConnections != 0 {
		t.Fatalf("RPCConnections = %d, want 0 (bad proof must never reach a handler)", snap.RPCConnections)
	}
}

func TestRestartRotatesCredentials(t *testing.T) {
	runner := newRecordingRunner()
	cfg := rpcConfig(pluginhost.KindDriver)
	cfg.Command.Env = helperEnv()
	s := pluginhost.NewSupervisor(cfg, runner, discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = s.Run(ctx) }()

	waitState(t, s, pluginhost.StateHealthy)
	if runner.StartCount() != 1 {
		t.Fatalf("StartCount = %d, want 1", runner.StartCount())
	}
	first := runner.Env(0)

	s.Restart()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if runner.StartCount() >= 2 && s.State() == pluginhost.StateHealthy {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if runner.StartCount() < 2 {
		t.Fatalf("StartCount = %d, want >= 2 after restart", runner.StartCount())
	}
	second := runner.Env(1)

	if first[pluginhost.EnvLaunchID] == second[pluginhost.EnvLaunchID] {
		t.Fatal("launch id was not rotated across restart")
	}
	if first[pluginhost.EnvProof] == second[pluginhost.EnvProof] {
		t.Fatal("proof was not rotated across restart")
	}
	if first[pluginhost.EnvPluginEndpoint] == second[pluginhost.EnvPluginEndpoint] {
		t.Fatal("endpoint was not recreated across restart")
	}

	// Reverse verification: the previous launch's proof must not authenticate
	// against the new listener.
	before := s.Snapshot().RPCConnections
	oldCreds := pluginruntime.Credentials{
		LaunchID: first[pluginhost.EnvLaunchID],
		Proof:    first[pluginhost.EnvProof],
	}
	tr, err := pluginruntime.Dial(context.Background(), second[pluginhost.EnvPluginEndpoint], oldCreds, pluginruntime.DefaultConfig())
	if err == nil {
		defer tr.Close()
		// Give the listener a moment to reject the stale proof.
		time.Sleep(100 * time.Millisecond)
	}
	after := s.Snapshot().RPCConnections
	if after != before {
		t.Fatalf("RPCConnections changed from %d to %d after stale-proof dial; want unchanged", before, after)
	}
}

func TestRPCHealthDegradedRecovery(t *testing.T) {
	dir := t.TempDir()
	healthFile := filepath.Join(dir, "health")
	if err := os.WriteFile(healthFile, []byte("healthy"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := rpcConfig(pluginhost.KindDriver)
	cfg.Command.Env = helperEnv(helperHealthFile + "=" + healthFile)
	s := pluginhost.NewSupervisor(cfg, pluginhost.ExecRunner{}, discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = s.Run(ctx) }()

	waitState(t, s, pluginhost.StateHealthy)

	if err := os.WriteFile(healthFile, []byte("degraded"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitState(t, s, pluginhost.StateDegraded)

	if err := os.WriteFile(healthFile, []byte("healthy"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitState(t, s, pluginhost.StateHealthy)
}

func TestRPCShutdownBeforeKill(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "shutdown")

	runner := newRecordingRunner()
	cfg := rpcConfig(pluginhost.KindDriver)
	cfg.Command.Env = helperEnv(helperShutdownMrk + "=" + marker)
	s := pluginhost.NewSupervisor(cfg, runner, discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	waitState(t, s, pluginhost.StateHealthy)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after cancel")
	}

	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("plugin did not receive the RPC shutdown: %v", err)
	}
	if runner.StartCount() != 1 {
		t.Fatalf("StartCount = %d, want 1", runner.StartCount())
	}
	proc := runner.Proc(0)
	if proc.Killed() {
		t.Fatal("process was force-killed after RPC shutdown; want graceful exit")
	}
}

func TestStartupFailureCleansEndpoint(t *testing.T) {
	cfg := rpcConfig(pluginhost.KindDriver)
	cfg.Command.Env = helperEnv(helperExitEarly + "=1")
	cfg.HandshakeTimeout = 500 * time.Millisecond
	s := pluginhost.NewSupervisor(cfg, pluginhost.ExecRunner{}, discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = s.Run(ctx) }()

	waitState(t, s, pluginhost.StateDisabled)
	snap := s.Snapshot()
	if snap.Endpoint == "" {
		t.Fatal("no endpoint recorded")
	}

	ep, err := pluginruntime.ParseEndpoint(snap.Endpoint)
	if err != nil {
		t.Fatalf("recorded endpoint %q is invalid: %v", snap.Endpoint, err)
	}
	if ep.Scheme == "unix" {
		if _, err := os.Stat(ep.Addr); !os.IsNotExist(err) {
			t.Fatalf("unix socket %q still present after startup failure: %v", ep.Addr, err)
		}
		return
	}
	conn, err := net.DialTimeout("tcp", ep.Addr, 500*time.Millisecond)
	if err == nil {
		conn.Close()
		t.Fatalf("tcp endpoint %q still accepting after startup failure", ep.Addr)
	}
}

func TestApplicationClientSelectedByKind(t *testing.T) {
	dir := t.TempDir()
	healthMark := filepath.Join(dir, "health-mark")

	cfg := rpcConfig(pluginhost.KindApplication)
	cfg.Command.Env = helperEnv(helperHealthMark + "=" + healthMark)
	s := pluginhost.NewSupervisor(cfg, pluginhost.ExecRunner{}, discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = s.Run(ctx) }()

	waitState(t, s, pluginhost.StateHealthy)
	if got := s.Snapshot().Kind; got != pluginhost.KindApplication {
		t.Fatalf("Kind = %s, want application", got)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(healthMark); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("application health RPC was never served; the host may have selected the driver client")
}

func TestConnectorKindUnsupported(t *testing.T) {
	runner := newRecordingRunner()
	cfg := rpcConfig(pluginhost.KindConnector)
	cfg.Command.Env = helperEnv()
	s := pluginhost.NewSupervisor(cfg, runner, discardLogger())

	err := s.Run(context.Background())
	if !errors.Is(err, pluginhost.ErrConnectorUnsupported) {
		t.Fatalf("Run error = %v, want ErrConnectorUnsupported", err)
	}
	if runner.StartCount() != 0 {
		t.Fatalf("StartCount = %d, want 0 for an unsupported connector kind", runner.StartCount())
	}
}
