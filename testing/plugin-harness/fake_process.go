package pluginharness

import (
	"context"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/DeliciousBuding/cloud-path/internal/pluginhost"
	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/driver"
	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/status"
	"github.com/DeliciousBuding/cloud-path/sdk/go/pluginruntime"
	"github.com/DeliciousBuding/cloud-path/sdk/go/transport"
)

// ErrKilled is returned by FakeProcess.Wait when the host force-kills the
// process.
var ErrKilled = errors.New("fake plugin process killed")

// FakeRunner is an in-memory pluginhost.Runner used by the Supervisor tests.
// Although the process itself is in-memory, each fake process still dials the
// real launch listener with the injected credentials and serves a minimal
// Driver Protocol so the Supervisor's socket handshake, RPC health and RPC
// shutdown paths run end to end.
type FakeRunner struct {
	mu       sync.Mutex
	factory  func() *FakeProcess
	started  []*FakeProcess
	startErr error
	pidSeq   int
}

// FakeRunnerOption configures a FakeRunner.
type FakeRunnerOption func(*FakeRunner)

// NewFakeRunner builds a runner that hands out fake processes. Each Start call
// creates a fresh process from the configured factory.
func NewFakeRunner(opts ...FakeRunnerOption) *FakeRunner {
	r := &FakeRunner{pidSeq: 1000}
	r.factory = func() *FakeProcess { return newFakeProcess() }
	for _, o := range opts {
		o(r)
	}
	return r
}

// WithAutoHandshake controls whether each fake process emits the expected
// handshake line on stdout after Start.
func WithAutoHandshake(v bool) FakeRunnerOption {
	return func(r *FakeRunner) {
		base := r.factory
		r.factory = func() *FakeProcess {
			p := base()
			p.autoHandshake = v
			return p
		}
	}
}

// WithCrashAfterHandshake makes each fake process exit with a crash right
// after its handshake, exercising crash detection and the restart budget.
func WithCrashAfterHandshake() FakeRunnerOption {
	return func(r *FakeRunner) {
		base := r.factory
		r.factory = func() *FakeProcess {
			p := base()
			p.crashAfterHandshake = true
			return p
		}
	}
}

// WithOnSignal installs a Signal hook on each fake process.
func WithOnSignal(fn func(os.Signal)) FakeRunnerOption {
	return func(r *FakeRunner) {
		base := r.factory
		r.factory = func() *FakeProcess {
			p := base()
			p.onSignal = fn
			return p
		}
	}
}

// Start implements pluginhost.Runner.
func (r *FakeRunner) Start(spec pluginhost.CommandSpec) (pluginhost.Process, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.startErr != nil {
		return nil, r.startErr
	}
	p := r.factory()
	p.mu.Lock()
	p.pid = r.pidSeq
	r.pidSeq++
	p.env = envMap(spec.Env)
	p.mu.Unlock()
	r.started = append(r.started, p)
	go p.run()
	return p, nil
}

// Started returns a copy of every process this runner has started.
func (r *FakeRunner) Started() []*FakeProcess {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*FakeProcess, len(r.started))
	copy(out, r.started)
	return out
}

// StartedCount returns how many Start calls have been issued.
func (r *FakeRunner) StartedCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.started)
}

// FakeProcess is an in-memory pluginhost.Process that also connects to the
// real launch listener, so the host observes a real socket authentication.
type FakeProcess struct {
	mu      sync.Mutex
	pid     int
	stdoutR *io.PipeReader
	stdoutW *io.PipeWriter
	stderrR *io.PipeReader
	stderrW *io.PipeWriter
	waitCh  chan error
	env     map[string]string

	autoHandshake       bool
	crashAfterHandshake bool
	onSignal            func(os.Signal)

	exited        bool
	killed        bool
	signals       []os.Signal
	hostTransport transport.Transport

	exitOnce sync.Once
	writeMu  sync.Mutex
}

func newFakeProcess() *FakeProcess {
	p := &FakeProcess{
		waitCh:        make(chan error, 1),
		autoHandshake: true,
	}
	p.stdoutR, p.stdoutW = io.Pipe()
	p.stderrR, p.stderrW = io.Pipe()
	return p
}

func (p *FakeProcess) run() {
	if p.autoHandshake {
		p.dialHost()
		p.WriteStdout(p.handshakeLine())
	}
	if p.crashAfterHandshake {
		p.Exit(errors.New("simulated plugin crash"))
		return
	}
	if p.autoHandshake {
		p.serveHost()
	}
}

// dialHost connects to the injected endpoint using the injected launch
// credentials. A missing endpoint (for example a timeout-only test) is a no-op.
func (p *FakeProcess) dialHost() {
	p.mu.Lock()
	env := p.env
	p.mu.Unlock()
	endpoint := env[pluginhost.EnvPluginEndpoint]
	if endpoint == "" {
		return
	}
	creds := pluginruntime.Credentials{
		LaunchID: env[pluginhost.EnvLaunchID],
		Proof:    env[pluginhost.EnvProof],
	}
	tr, err := pluginruntime.Dial(context.Background(), endpoint, creds, pluginruntime.DefaultConfig())
	if err != nil {
		return
	}
	p.mu.Lock()
	p.hostTransport = tr
	p.mu.Unlock()
}

// serveHost runs a minimal Driver Protocol server over the dialed transport so
// the host's periodic Health and Shutdown RPCs succeed.
func (p *FakeProcess) serveHost() {
	p.mu.Lock()
	tr := p.hostTransport
	p.mu.Unlock()
	if tr == nil {
		return
	}
	srv := driver.NewRPCServer(tr, fakeDriverServer{})
	_ = srv.Serve(context.Background())
}

func (p *FakeProcess) handshakeLine() string {
	p.mu.Lock()
	env := p.env
	p.mu.Unlock()
	version, _ := strconv.ParseUint(env[pluginhost.EnvProtocolVersion], 10, 32)
	transportName := "tcp"
	endpoint := "127.0.0.1:40000"
	if raw := env[pluginhost.EnvPluginEndpoint]; raw != "" {
		if ep, err := pluginruntime.ParseEndpoint(raw); err == nil {
			transportName, endpoint = ep.Scheme, ep.Addr
		}
	}
	return (pluginhost.Handshake{
		Marker:          pluginhost.HandshakeMarker,
		PluginID:        env[pluginhost.EnvPluginID],
		Protocol:        env[pluginhost.EnvProtocol],
		ProtocolVersion: uint32(version),
		Transport:       transportName,
		Endpoint:        endpoint,
		RPC:             "grpc",
		LaunchID:        env[pluginhost.EnvLaunchID],
		Proof:           env[pluginhost.EnvProof],
	}).String()
}

// Wait implements pluginhost.Process.
func (p *FakeProcess) Wait() error { return <-p.waitCh }

// Signal implements pluginhost.Process. By default a signal is treated as a
// graceful stop; tests can install a hook with WithOnSignal.
func (p *FakeProcess) Signal(sig os.Signal) error {
	p.mu.Lock()
	p.signals = append(p.signals, sig)
	fn := p.onSignal
	p.mu.Unlock()
	if fn != nil {
		fn(sig)
		return nil
	}
	p.Exit(nil)
	return nil
}

// Kill implements pluginhost.Process.
func (p *FakeProcess) Kill() error {
	p.mu.Lock()
	p.killed = true
	p.mu.Unlock()
	p.Exit(ErrKilled)
	return nil
}

// Pid implements pluginhost.Process.
func (p *FakeProcess) Pid() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pid
}

// Stdout implements pluginhost.Process.
func (p *FakeProcess) Stdout() io.ReadCloser { return p.stdoutR }

// Stderr implements pluginhost.Process.
func (p *FakeProcess) Stderr() io.ReadCloser { return p.stderrR }

// Exit makes the process terminate with err. It is idempotent.
func (p *FakeProcess) Exit(err error) {
	p.exitOnce.Do(func() {
		p.mu.Lock()
		p.exited = true
		tr := p.hostTransport
		p.mu.Unlock()
		if tr != nil {
			_ = tr.Close()
		}
		_ = p.stdoutW.Close()
		_ = p.stderrW.Close()
		p.waitCh <- err
	})
}

// WriteStdout appends one stdout line.
func (p *FakeProcess) WriteStdout(line string) {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	_, _ = p.stdoutW.Write([]byte(line + "\n"))
}

// WriteStderr appends one stderr line.
func (p *FakeProcess) WriteStderr(line string) {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	_, _ = p.stderrW.Write([]byte(line + "\n"))
}

// Killed reports whether Kill was called.
func (p *FakeProcess) Killed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.killed
}

// Exited reports whether the process has terminated.
func (p *FakeProcess) Exited() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exited
}

// Signals returns the signals delivered to the process.
func (p *FakeProcess) Signals() []os.Signal {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]os.Signal(nil), p.signals...)
}

// Env returns the environment the runner injected into the process.
func (p *FakeProcess) Env() map[string]string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]string, len(p.env))
	for k, v := range p.env {
		out[k] = v
	}
	return out
}

// fakeDriverServer is the minimal DriverService the fake process serves. Only
// Health and Shutdown are exercised by the host lifecycle; the other methods
// return stable empty responses.
type fakeDriverServer struct{}

var _ driver.DriverServer = fakeDriverServer{}

func (fakeDriverServer) Initialize(_ context.Context, _ *driver.InitializeRequest) (*driver.InitializeResponse, error) {
	return &driver.InitializeResponse{NegotiatedProtocolVersion: driver.ProtocolVersion, Status: status.New()}, nil
}
func (fakeDriverServer) Describe(_ context.Context) (*driver.DriverDescriptor, error) {
	return &driver.DriverDescriptor{DriverID: "fake-driver"}, nil
}
func (fakeDriverServer) ConfigureInstance(_ context.Context, _ *driver.ConfigureInstanceRequest) (*driver.ConfigureInstanceResponse, error) {
	return &driver.ConfigureInstanceResponse{Status: status.New()}, nil
}
func (fakeDriverServer) Discover(_ context.Context, _ *driver.DiscoverRequest, _ driver.DiscoveryWriter) error {
	return nil
}
func (fakeDriverServer) OpenDevice(_ context.Context, _ *driver.OpenDeviceRequest) (*driver.OpenDeviceResponse, error) {
	return &driver.OpenDeviceResponse{Status: status.New()}, nil
}
func (fakeDriverServer) CloseDevice(_ context.Context, _ *driver.CloseDeviceRequest) (*driver.CloseDeviceResponse, error) {
	return &driver.CloseDeviceResponse{Status: status.New()}, nil
}
func (fakeDriverServer) Watch(_ context.Context, _ *driver.WatchRequest, _ driver.DriverMessageWriter) error {
	return nil
}
func (fakeDriverServer) Execute(_ context.Context, _ *driver.ExecuteRequest) (*driver.ExecuteResponse, error) {
	return &driver.ExecuteResponse{Status: status.New()}, nil
}
func (fakeDriverServer) Health(_ context.Context) (*driver.HealthResponse, error) {
	return &driver.HealthResponse{State: driver.HealthStateServing}, nil
}
func (fakeDriverServer) Shutdown(_ context.Context, _ *driver.ShutdownRequest) (*driver.ShutdownResponse, error) {
	return &driver.ShutdownResponse{Status: status.New()}, nil
}

func envMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[k] = v
		}
	}
	return m
}
