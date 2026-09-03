package pluginharness

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/pluginhost"
	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/application"
	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/driver"
	"github.com/DeliciousBuding/cloud-path/sdk/go/pluginmain"
	"github.com/DeliciousBuding/cloud-path/sdk/go/pluginruntime"
	"github.com/DeliciousBuding/cloud-path/sdk/go/transport"
)

const (
	appImportPath         = "github.com/DeliciousBuding/cloud-path/examples/scheduled-compartment/cmd/cloud-path-app-scheduled-compartment"
	driverFixtureImport   = "github.com/DeliciousBuding/cloud-path/testing/plugin-fixtures/cmd/cloudpath-driver-fixture"
	appPluginID           = "io.github.deliciousbuding.cloud-path-app-scheduled-compartment"
	driverFixturePluginID = "io.test.driver-fixture"
)

// buildBinary compiles one install-style plugin binary into a fresh temp dir
// and returns its path. It invokes `go build` directly, never a shell.
func buildBinary(t *testing.T, importPath, name string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, name)
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, importPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build %s: %v\n%s", importPath, err, out)
	}
	return bin
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func waitState(t *testing.T, s *pluginhost.Supervisor, want pluginhost.State) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if s.State() == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("supervisor state = %s, want %s", s.State(), want)
}

func supervisorConfig(kind pluginhost.Kind, bin string, env []string) pluginhost.Config {
	return pluginhost.Config{
		PluginID:            pluginIDFor(kind),
		Kind:                kind,
		Protocol:            kind.Protocol(),
		ProtocolVersion:     1,
		Command:             pluginhost.CommandSpec{Path: bin, Env: env},
		HandshakeTimeout:    10 * time.Second,
		ShutdownTimeout:     3 * time.Second,
		HealthCheckInterval: 50 * time.Millisecond,
		MaxRestarts:         0,
		BaseBackoff:         10 * time.Millisecond,
		MaxBackoff:          50 * time.Millisecond,
		Jitter:              func(time.Duration) time.Duration { return 0 },
		LogBufferSize:       64,
	}
}

func pluginIDFor(kind pluginhost.Kind) string {
	if kind == pluginhost.KindApplication {
		return appPluginID
	}
	return driverFixturePluginID
}

// e2eRecordingRunner records Kill calls so tests can prove graceful shutdown.
type e2eRecordingRunner struct {
	inner pluginhost.Runner
	mu    sync.Mutex
	procs []*e2eRecordingProcess
}

func newRecordingRunner(inner pluginhost.Runner) *e2eRecordingRunner {
	return &e2eRecordingRunner{inner: inner}
}

func (r *e2eRecordingRunner) Start(spec pluginhost.CommandSpec) (pluginhost.Process, error) {
	proc, err := r.inner.Start(spec)
	if err != nil {
		return nil, err
	}
	rp := &e2eRecordingProcess{Process: proc}
	r.mu.Lock()
	r.procs = append(r.procs, rp)
	r.mu.Unlock()
	return rp, nil
}

func (r *e2eRecordingRunner) lastKilled() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.procs) == 0 {
		return false
	}
	return r.procs[len(r.procs)-1].Killed()
}

type e2eRecordingProcess struct {
	pluginhost.Process
	mu     sync.Mutex
	killed bool
}

func (p *e2eRecordingProcess) Kill() error {
	p.mu.Lock()
	p.killed = true
	p.mu.Unlock()
	return p.Process.Kill()
}

func (p *e2eRecordingProcess) Killed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.killed
}

// startDirectListener binds the host side of a plugin launch.
func startDirectListener(t *testing.T, creds pluginruntime.Credentials) (*pluginruntime.Listener, context.Context, context.CancelFunc) {
	t.Helper()
	ln, err := pluginruntime.Listen(context.Background(), "tcp://127.0.0.1:0", creds, pluginruntime.DefaultConfig())
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return ln, ctx, cancel
}

// launchDirect starts bin pointed at the given host endpoint with the standard
// launch identity environment.
func launchDirect(t *testing.T, bin, endpoint, pluginID, protocol, launchID, proof string, extra ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(),
		pluginmain.EnvPluginID+"="+pluginID,
		pluginmain.EnvProtocol+"="+protocol,
		pluginmain.EnvProtocolVersion+"=1",
		pluginmain.EnvLaunchID+"="+launchID,
		pluginmain.EnvProof+"="+proof,
		pluginmain.EnvPluginEndpoint+"="+endpoint,
	)
	cmd.Env = append(cmd.Env, extra...)
	return cmd
}

// waitExit waits for cmd to exit, killing it on timeout.
func waitExit(t *testing.T, cmd *exec.Cmd, timeout time.Duration) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		<-done
	}
}

// syncBuffer 是带锁的 bytes.Buffer：os/exec 在 cmd.Stdout/Stderr 为非 *os.File
// 时会起内部 io.Copy goroutine 写缓冲，测试 goroutine 并发读 String() 必须同步
// （CI -race 实测捕获）。
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestScheduledCompartmentBinaryHostE2E(t *testing.T) {
	bin := buildBinary(t, appImportPath, "cloud-path-app-scheduled-compartment")
	creds := pluginruntime.Credentials{LaunchID: "launch-app-e2e", Proof: "proof-app-e2e"}

	t.Run("protocol", func(t *testing.T) {
		ln, serveCtx, serveCancel := startDirectListener(t, creds)
		defer serveCancel()

		type outcome struct {
			init     *application.InitializeResponse
			desc     *application.ApplicationDescriptor
			health   *application.HealthResponse
			shutdown *application.ShutdownResponse
			err      error
		}
		resultCh := make(chan outcome, 1)
		go func() {
			_ = ln.Serve(serveCtx, func(ctx context.Context, conn transport.Transport) error {
				defer conn.Close()
				cli := application.NewClient(conn)
				init, err := cli.Initialize(ctx, &application.InitializeRequest{
					PluginID:        appPluginID,
					PluginVersion:   "0.1.0",
					LaunchID:        creds.LaunchID,
					HandshakeCookie: creds.Proof,
					ProtocolVersion: application.ProtocolVersion,
				})
				if err != nil {
					resultCh <- outcome{err: err}
					return nil
				}
				desc, err := cli.Describe(ctx)
				if err != nil {
					resultCh <- outcome{err: err}
					return nil
				}
				health, err := cli.Health(ctx)
				if err != nil {
					resultCh <- outcome{err: err}
					return nil
				}
				shutdown, err := cli.Shutdown(ctx, &application.ShutdownRequest{Reason: "e2e"})
				resultCh <- outcome{init: init, desc: desc, health: health, shutdown: shutdown, err: err}
				return nil
			})
		}()

		cmd := launchDirect(t, bin, ln.Endpoint().String(), appPluginID, "application", creds.LaunchID, creds.Proof)
		var stdout, stderr syncBuffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Start(); err != nil {
			t.Fatalf("start: %v", err)
		}

		var res outcome
		select {
		case res = <-resultCh:
		case <-time.After(10 * time.Second):
			t.Fatalf("timed out waiting for application RPC; stdout=%s stderr=%s", stdout.String(), stderr.String())
		}
		if res.err != nil {
			t.Fatalf("application RPC: %v", res.err)
		}
		if res.init == nil || res.init.Status == nil || !res.init.Status.IsOK() {
			t.Fatalf("Initialize response not OK: %+v", res.init)
		}
		if res.desc == nil || res.desc.ApplicationID != appPluginID {
			t.Fatalf("Describe = %+v, want application id %s", res.desc, appPluginID)
		}
		if res.health == nil || res.health.State != application.HealthStateServing {
			t.Fatalf("Health = %+v, want serving", res.health)
		}
		if res.shutdown == nil || res.shutdown.Status == nil || !res.shutdown.Status.IsOK() {
			t.Fatalf("Shutdown response not OK: %+v", res.shutdown)
		}
		if !strings.HasPrefix(strings.TrimSpace(firstLineString(stdout.String())), pluginmain.HandshakeMarker+"|") {
			t.Fatalf("missing handshake line on stdout: %q", stdout.String())
		}

		serveCancel()
		waitExit(t, cmd, 5*time.Second)
	})

	t.Run("supervisor", func(t *testing.T) {
		runner := newRecordingRunner(pluginhost.ExecRunner{})
		cfg := supervisorConfig(pluginhost.KindApplication, bin, os.Environ())
		s := pluginhost.NewSupervisor(cfg, runner, discardLogger())
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
		if snap.Kind != pluginhost.KindApplication {
			t.Fatalf("Kind = %s, want application", snap.Kind)
		}
		assertLoopbackEndpoint(t, snap.Endpoint)

		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("Run returned %v, want nil", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("Run did not return after cancel")
		}
		if runner.lastKilled() {
			t.Fatal("application was force-killed; want graceful RPC shutdown exit")
		}
	})
}

func TestDriverFixtureBinaryHostE2E(t *testing.T) {
	bin := buildBinary(t, driverFixtureImport, "cloudpath-driver-fixture")
	creds := pluginruntime.Credentials{LaunchID: "launch-driver-e2e", Proof: "proof-driver-e2e"}

	t.Run("protocol", func(t *testing.T) {
		ln, serveCtx, serveCancel := startDirectListener(t, creds)
		defer serveCancel()

		type outcome struct {
			desc     *driver.DriverDescriptor
			health   *driver.HealthResponse
			shutdown *driver.ShutdownResponse
			err      error
		}
		resultCh := make(chan outcome, 1)
		go func() {
			_ = ln.Serve(serveCtx, func(ctx context.Context, conn transport.Transport) error {
				defer conn.Close()
				cli := driver.NewClient(conn)
				desc, err := cli.Describe(ctx)
				if err != nil {
					resultCh <- outcome{err: err}
					return nil
				}
				health, err := cli.Health(ctx)
				if err != nil {
					resultCh <- outcome{err: err}
					return nil
				}
				shutdown, err := cli.Shutdown(ctx, &driver.ShutdownRequest{Reason: "e2e"})
				resultCh <- outcome{desc: desc, health: health, shutdown: shutdown, err: err}
				return nil
			})
		}()

		cmd := launchDirect(t, bin, ln.Endpoint().String(), driverFixturePluginID, "driver", creds.LaunchID, creds.Proof)
		var stdout, stderr syncBuffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Start(); err != nil {
			t.Fatalf("start: %v", err)
		}

		var res outcome
		select {
		case res = <-resultCh:
		case <-time.After(10 * time.Second):
			t.Fatalf("timed out waiting for driver RPC; stdout=%s stderr=%s", stdout.String(), stderr.String())
		}
		if res.err != nil {
			t.Fatalf("driver RPC: %v", res.err)
		}
		if res.desc == nil || res.desc.DriverID != "io.test.driver-fixture" {
			t.Fatalf("Describe = %+v, want driver id io.test.driver-fixture", res.desc)
		}
		if res.health == nil || res.health.State != driver.HealthStateServing {
			t.Fatalf("Health = %+v, want serving", res.health)
		}
		if res.shutdown == nil || res.shutdown.Status == nil || !res.shutdown.Status.IsOK() {
			t.Fatalf("Shutdown response not OK: %+v", res.shutdown)
		}

		serveCancel()
		waitExit(t, cmd, 5*time.Second)
	})

	t.Run("supervisor", func(t *testing.T) {
		events := filepath.Join(t.TempDir(), "events.txt")
		runner := newRecordingRunner(pluginhost.ExecRunner{})
		cfg := supervisorConfig(pluginhost.KindDriver, bin, append(os.Environ(), "CLOUDPATH_FIXTURE_EVENTS_FILE="+events))
		s := pluginhost.NewSupervisor(cfg, runner, discardLogger())
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- s.Run(ctx) }()

		waitState(t, s, pluginhost.StateHealthy)
		snap := s.Snapshot()
		if !snap.HandshakeCompleted || snap.RPCConnections != 1 {
			t.Fatalf("handshake/RPC snapshot = %+v, want completed with one connection", snap)
		}
		if snap.Kind != pluginhost.KindDriver {
			t.Fatalf("Kind = %s, want driver", snap.Kind)
		}
		assertLoopbackEndpoint(t, snap.Endpoint)

		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("Run returned %v, want nil", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("Run did not return after cancel")
		}
		if runner.lastKilled() {
			t.Fatal("driver fixture was force-killed; want graceful RPC shutdown exit")
		}
		if b, err := os.ReadFile(events); err != nil || !strings.Contains(string(b), "shutdown") {
			t.Fatalf("driver Shutdown RPC was not delivered; events=%q err=%v", b, err)
		}
	})
}

func TestPluginExitCleansTransport(t *testing.T) {
	bin := buildBinary(t, driverFixtureImport, "cloudpath-driver-fixture")
	runner := newRecordingRunner(pluginhost.ExecRunner{})
	cfg := supervisorConfig(pluginhost.KindDriver, bin, append(os.Environ(), "CLOUDPATH_FIXTURE_EXIT_AFTER=200ms"))
	s := pluginhost.NewSupervisor(cfg, runner, discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = s.Run(ctx) }()

	waitState(t, s, pluginhost.StateHealthy)
	// The plugin exits on its own; the supervisor then exhausts its restart
	// budget (MaxRestarts=0) and disables the plugin.
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
			t.Fatalf("unix socket %q still present after plugin exit", ep.Addr)
		}
		return
	}
	conn, err := net.DialTimeout("tcp", ep.Addr, 500*time.Millisecond)
	if err == nil {
		_ = conn.Close()
		t.Fatalf("tcp endpoint %q still accepting after plugin exit", ep.Addr)
	}
	if runner.lastKilled() {
		t.Fatal("plugin was force-killed; want self-initiated exit")
	}
}

func assertLoopbackEndpoint(t *testing.T, endpoint string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		if !strings.HasPrefix(endpoint, "tcp://127.0.0.1:") {
			t.Fatalf("endpoint = %q, want loopback tcp", endpoint)
		}
		return
	}
	if !strings.HasPrefix(endpoint, "unix://") {
		t.Fatalf("endpoint = %q, want unix socket", endpoint)
	}
}

func firstLineString(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
