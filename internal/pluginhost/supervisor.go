package pluginhost

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	randv2 "math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DeliciousBuding/cloud-path/sdk/go/pluginruntime"
)

// Defaults applied when a Config field is left zero.
const (
	defaultHandshakeTimeout   = 5 * time.Second
	defaultShutdownTimeout    = 5 * time.Second
	defaultBaseBackoff        = 100 * time.Millisecond
	defaultMaxBackoff         = 5 * time.Second
	defaultLogBufferSize      = 256
	defaultHealthProbeTimeout = 2 * time.Second
)

// Config configures one supervised plugin process.
type Config struct {
	PluginID        string
	Kind            Kind
	Protocol        string
	ProtocolVersion uint32
	Command         CommandSpec

	// HandshakeTimeout is how long the host waits for the unique stdout
	// handshake line and the socket authentication frame before killing the
	// process and counting a crash.
	HandshakeTimeout time.Duration
	// ShutdownTimeout is the graceful shutdown deadline; after it the process
	// is killed.
	ShutdownTimeout time.Duration
	// HealthCheckInterval is how often the established RPC client is probed.
	HealthCheckInterval time.Duration
	// MaxRestarts is the crash-loop budget: at most this many re-launches are
	// attempted before the plugin is disabled. Zero disables restarts.
	MaxRestarts int
	BaseBackoff time.Duration
	MaxBackoff  time.Duration
	// LogBufferSize caps the retained redacted log lines per plugin.
	LogBufferSize int
	// Jitter randomizes backoff; default is full jitter in [0, d]. Override
	// with a deterministic function in tests.
	Jitter func(time.Duration) time.Duration
}

func (c Config) normalized() (Config, error) {
	if c.PluginID == "" {
		return c, fmt.Errorf("pluginhost: PluginID is required")
	}
	if c.Kind == 0 {
		c.Kind = kindFromProtocol(c.Protocol)
	}
	if c.Protocol == "" {
		c.Protocol = c.Kind.Protocol()
	}
	if c.Kind == KindConnector {
		return c, ErrConnectorUnsupported
	}
	if c.Protocol != c.Kind.Protocol() {
		return c, fmt.Errorf("pluginhost: protocol %q does not match plugin kind %s", c.Protocol, c.Kind)
	}
	if c.ProtocolVersion == 0 {
		return c, fmt.Errorf("pluginhost: ProtocolVersion is required")
	}
	if c.Command.Path == "" {
		return c, fmt.Errorf("pluginhost: Command.Path is required")
	}
	if c.HandshakeTimeout <= 0 {
		c.HandshakeTimeout = defaultHandshakeTimeout
	}
	if c.ShutdownTimeout <= 0 {
		c.ShutdownTimeout = defaultShutdownTimeout
	}
	if c.HealthCheckInterval <= 0 {
		c.HealthCheckInterval = defaultHealthCheckInterval
	}
	if c.BaseBackoff <= 0 {
		c.BaseBackoff = defaultBaseBackoff
	}
	if c.MaxBackoff <= 0 {
		c.MaxBackoff = defaultMaxBackoff
	}
	if c.MaxBackoff < c.BaseBackoff {
		c.MaxBackoff = c.BaseBackoff
	}
	if c.LogBufferSize <= 0 {
		c.LogBufferSize = defaultLogBufferSize
	}
	if c.Jitter == nil {
		c.Jitter = defaultJitter
	}
	return c, nil
}

func defaultJitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	return time.Duration(randv2.Int64N(int64(d) + 1))
}

// LogEntry is one collected, already-redacted plugin log line.
type LogEntry struct {
	Stream string
	Line   string
}

// Snapshot is a point-in-time view of the supervisor for observability and
// tests.
type Snapshot struct {
	State              State
	Crashes            int
	Restarts           int
	Launches           int
	HandshakeCompleted bool
	Backoffs           []time.Duration
	// Kind is the protocol kind the supervisor was configured for.
	Kind Kind
	// Endpoint is the most recent launch endpoint (loopback TCP or Unix
	// socket path). It is a local-only address and safe to surface.
	Endpoint string
	// RPCConnections is the number of authenticated RPC connections the host
	// has accepted across the current launch. Rejected proofs never count.
	RPCConnections int
}

// Supervisor owns the lifecycle of one plugin process.
type Supervisor struct {
	cfg    Config
	runner Runner
	logger *slog.Logger

	mu       sync.Mutex
	state    State
	disabled bool

	wake      chan struct{}
	restartCh chan struct{}
	collector *logCollector

	launchID     string
	proof        string
	lastEndpoint string
	rpcConns     int
	launches     int
	crashes      int
	restarts     int
	handshaked   bool
	backoffs     []time.Duration
}

// NewSupervisor builds a Supervisor. A nil runner uses the production
// ExecRunner; a nil logger discards logs.
func NewSupervisor(cfg Config, runner Runner, logger *slog.Logger) *Supervisor {
	if runner == nil {
		runner = ExecRunner{}
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Supervisor{
		cfg:       cfg,
		runner:    runner,
		logger:    logger,
		state:     StateStopped,
		wake:      make(chan struct{}, 1),
		restartCh: make(chan struct{}, 1),
		collector: &logCollector{
			logger:   logger,
			pluginID: cfg.PluginID,
			cap:      cfg.LogBufferSize,
		},
	}
}

// Run supervises the plugin until ctx is canceled. On cancellation it performs
// a graceful shutdown (RPC Shutdown, then signal, then kill after
// ShutdownTimeout). Run returns nil after a graceful stop and ctx.Err() when
// it was already shutting down.
func (s *Supervisor) Run(ctx context.Context) error {
	cfg, err := s.cfg.normalized()
	if err != nil {
		return err
	}
	s.cfg = cfg
	s.collector.setCap(cfg.LogBufferSize)

	backoff := time.Duration(0)

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if s.isDisabled() {
			s.setState(StateDisabled)
			if !s.waitEnabled(ctx) {
				return ctx.Err()
			}
			s.setState(StateStopped)
			backoff = 0
			s.mu.Lock()
			s.restarts = 0
			s.crashes = 0
			s.handshaked = false
			s.mu.Unlock()
			continue
		}

		if backoff > 0 {
			s.setState(StateBackoff)
			if !s.waitBackoff(ctx, backoff) {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				continue
			}
			s.mu.Lock()
			s.restarts++
			s.mu.Unlock()
		}

		switch s.runOnce(ctx) {
		case runCrashed:
			s.mu.Lock()
			s.crashes++
			crashes := s.crashes
			s.mu.Unlock()
			if crashes > s.cfg.MaxRestarts {
				s.logger.Error("restart budget exhausted; disabling plugin",
					"plugin_id", s.cfg.PluginID,
					"crashes", crashes,
				)
				s.mu.Lock()
				s.disabled = true
				s.mu.Unlock()
				continue
			}
			backoff = s.nextBackoff(backoff)
			s.mu.Lock()
			s.backoffs = append(s.backoffs, backoff)
			s.mu.Unlock()
		case runDisabled:
			backoff = 0
		case runRestart:
			backoff = 0
		case runShutdown:
			return nil
		}
	}
}

func (s *Supervisor) nextBackoff(prev time.Duration) time.Duration {
	sleep := s.cfg.BaseBackoff
	if prev > 0 {
		sleep = prev * 2
	}
	if sleep > s.cfg.MaxBackoff {
		sleep = s.cfg.MaxBackoff
	}
	if s.cfg.Jitter != nil {
		j := s.cfg.Jitter(sleep)
		if j < 0 {
			j = 0
		}
		if j > sleep {
			j = sleep
		}
		sleep -= j
	}
	if sleep <= 0 {
		sleep = time.Millisecond
	}
	return sleep
}

func (s *Supervisor) waitEnabled(ctx context.Context) bool {
	for {
		select {
		case <-ctx.Done():
			return false
		case <-s.wake:
			if !s.isDisabled() {
				return true
			}
		case <-s.restartCh:
			// A health restart requested while disabled is dropped; the
			// plugin is not running so there is nothing to restart.
		}
	}
}

func (s *Supervisor) waitBackoff(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-s.wake:
			if s.isDisabled() {
				return false
			}
			continue
		case <-s.restartCh:
			// Drop a stale health-restart request while backing off.
		case <-t.C:
			return true
		}
	}
}

type runOutcome int

const (
	runCrashed runOutcome = iota
	runDisabled
	runRestart
	runShutdown
)

// launch is the per-launch state owned by one runOnce pass: the listener, its
// credentials and the endpoint handed to the plugin process.
type launch struct {
	listener *pluginruntime.Listener
	endpoint pluginruntime.Endpoint
	cleanup  func()
}

// runOnce starts one process on a fresh local endpoint, waits for both the
// stdout handshake and the socket authentication frame, monitors the plugin
// with periodic RPC health, and always returns with the process dead and the
// endpoint cleaned up.
func (s *Supervisor) runOnce(ctx context.Context) runOutcome {
	s.setState(StateStarting)

	// 1. Create a per-launch endpoint and credentials. Endpoint creation never
	// reuses the previous launch's proof.
	launchID := newLaunchID()
	proof := newProof()
	creds := pluginruntime.Credentials{LaunchID: launchID, Proof: proof}

	endpointRaw, cleanup, err := launchEndpoint()
	if err != nil {
		s.logger.Error("plugin endpoint create failed", "plugin_id", s.cfg.PluginID, "error", err)
		s.setState(StateCrashed)
		return runCrashed
	}
	listener, err := pluginruntime.Listen(context.Background(), endpointRaw, creds, pluginruntime.DefaultConfig())
	if err != nil {
		cleanup()
		s.logger.Error("plugin listener create failed", "plugin_id", s.cfg.PluginID, "error", err)
		s.setState(StateCrashed)
		return runCrashed
	}

	s.mu.Lock()
	s.launchID = launchID
	s.proof = proof
	s.lastEndpoint = listener.Endpoint().String()
	s.rpcConns = 0
	s.launches++
	s.mu.Unlock()

	launch := &launch{listener: listener, endpoint: listener.Endpoint(), cleanup: cleanup}

	// 2. Start serving the endpoint before the process can dial it. The serve
	// context is independent of the Run context: on a graceful shutdown the
	// supervisor must keep the connection open until the RPC Shutdown has been
	// delivered, so the listener is torn down only by the runOnce cleanup.
	serveCtx, serveCancel := context.WithCancel(context.Background())
	sess := newRuntimeSession(s.cfg.Kind)
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- listener.Serve(serveCtx, sess.handler(func() {
			s.mu.Lock()
			s.rpcConns++
			s.mu.Unlock()
		}))
	}()

	// Endpoint/listener/socket/temp dir/session are cleaned on every return
	// path, regardless of which phase failed.
	defer func() {
		serveCancel()
		_ = listener.Close()
		cleanup()
		_ = sess.close()
	}()

	// 3. Inject endpoint/identity into the process environment and launch it.
	spec := s.cfg.Command
	spec.Env = append(append([]string{}, spec.Env...),
		EnvPluginID+"="+s.cfg.PluginID,
		EnvProtocol+"="+s.cfg.Protocol,
		EnvProtocolVersion+"="+strconv.FormatUint(uint64(s.cfg.ProtocolVersion), 10),
		EnvLaunchID+"="+launchID,
		EnvProof+"="+proof,
		EnvHandshakeCookie+"="+proof,
		EnvPluginEndpoint+"="+listener.Endpoint().String(),
	)
	s.logger.Debug("launching plugin", "plugin_id", s.cfg.PluginID, "launch_id", launchID)

	proc, err := s.runner.Start(spec)
	if err != nil {
		s.logger.Error("plugin start failed", "plugin_id", s.cfg.PluginID, "error", err)
		s.setState(StateCrashed)
		return runCrashed
	}
	if proc == nil {
		s.logger.Error("plugin start failed", "plugin_id", s.cfg.PluginID, "error", "runner returned a nil process")
		s.setState(StateCrashed)
		return runCrashed
	}
	h := newProcHandle(proc, s.collector)

	// 4. Wait for both the stdout handshake and the socket authentication.
	hsTimer := time.NewTimer(s.cfg.HandshakeTimeout)
	defer hsTimer.Stop()

	var hs *Handshake
	var sessReady bool
	for hs == nil || !sessReady {
		select {
		case res := <-h.hsCh:
			if res.err != nil {
				s.logger.Error("handshake rejected", "plugin_id", s.cfg.PluginID, "error", res.err)
				h.kill()
				s.setState(StateCrashed)
				return runCrashed
			}
			if err := s.validateHandshake(launch, res.hs); err != nil {
				s.logger.Error("handshake rejected", "plugin_id", s.cfg.PluginID, "error", err)
				h.kill()
				s.setState(StateCrashed)
				return runCrashed
			}
			hs = &res.hs
		case <-sess.established:
			sessReady = true
		case <-hsTimer.C:
			s.logger.Error("handshake timeout", "plugin_id", s.cfg.PluginID, "timeout", s.cfg.HandshakeTimeout)
			h.kill()
			s.setState(StateCrashed)
			return runCrashed
		case err := <-h.exitCh:
			s.logger.Error("plugin exited before handshake", "plugin_id", s.cfg.PluginID, "error", err)
			s.setState(StateCrashed)
			return runCrashed
		case err := <-serveDone:
			if err == nil || errors.Is(err, context.Canceled) {
				err = errors.New("listener stopped before session established")
			}
			s.logger.Error("socket serve stopped before handshake", "plugin_id", s.cfg.PluginID, "error", err)
			h.kill()
			s.setState(StateCrashed)
			return runCrashed
		case <-s.wake:
			if s.isDisabled() {
				s.logger.Info("plugin disabled during handshake", "plugin_id", s.cfg.PluginID)
				h.kill()
				s.setState(StateDisabled)
				return runDisabled
			}
		case <-s.restartCh:
			// Drop a health-restart request that races the handshake; it will
			// be re-issued by the Manager health loop if still needed.
		case <-ctx.Done():
			return s.shutdown(h, sess)
		}
	}

	s.mu.Lock()
	s.handshaked = true
	s.mu.Unlock()
	return s.monitor(ctx, h, sess, launch)
}

// monitor watches a healthy/degraded process until it exits, degrades, is
// disabled, or the context is canceled. It probes the plugin over the RPC
// session on a fixed cadence and reflects the result in the process state.
func (s *Supervisor) monitor(ctx context.Context, h *procHandle, sess *runtimeSession, launch *launch) runOutcome {
	s.setState(StateHealthy)
	s.logger.Info("plugin healthy", "plugin_id", s.cfg.PluginID, "endpoint", launch.endpoint.String())

	interval := s.cfg.HealthCheckInterval
	if interval <= 0 {
		interval = defaultHealthCheckInterval
	}
	healthTicker := time.NewTicker(interval)
	defer healthTicker.Stop()

	for {
		select {
		case err := <-h.exitCh:
			s.logger.Error("plugin exited", "plugin_id", s.cfg.PluginID, "error", err)
			s.setState(StateCrashed)
			return runCrashed
		case <-h.dupCh:
			s.logger.Error("duplicate handshake rejected; degrading plugin", "plugin_id", s.cfg.PluginID)
			s.setState(StateDegraded)
		case <-healthTicker.C:
			probeCtx, cancel := context.WithTimeout(ctx, defaultHealthProbeTimeout)
			err := sess.health(probeCtx)
			cancel()
			if err != nil {
				s.logger.Warn("plugin health degraded", "plugin_id", s.cfg.PluginID, "error", err)
				s.setState(StateDegraded)
			} else {
				s.setState(StateHealthy)
			}
		case <-s.restartCh:
			s.logger.Info("health restart requested", "plugin_id", s.cfg.PluginID)
			h.kill()
			select {
			case <-h.exitCh:
			case <-time.After(s.cfg.ShutdownTimeout):
			}
			s.setState(StateStopped)
			return runRestart
		case <-s.wake:
			if s.isDisabled() {
				s.logger.Info("plugin disabled", "plugin_id", s.cfg.PluginID)
				s.setState(StateDisabled)
				h.kill()
				return runDisabled
			}
		case <-ctx.Done():
			return s.shutdown(h, sess)
		}
	}
}

// shutdown stops the plugin: first it sends the protocol Shutdown RPC with a
// deadline, then it signals the process and finally force-kills the process
// tree if it is still alive.
func (s *Supervisor) shutdown(h *procHandle, sess *runtimeSession) runOutcome {
	s.logger.Info("graceful shutdown", "plugin_id", s.cfg.PluginID, "timeout", s.cfg.ShutdownTimeout)

	if sess != nil {
		rpcCtx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout)
		err := sess.shutdown(rpcCtx)
		cancel()
		if err != nil {
			s.logger.Debug("RPC shutdown failed; falling back to process signal",
				"plugin_id", s.cfg.PluginID, "error", err)
		}
		// If the plugin exited in response to the RPC, no signal or kill is
		// needed.
		select {
		case <-h.exitCh:
			s.setState(StateStopped)
			return runShutdown
		case <-time.After(s.cfg.ShutdownTimeout):
		}
	}

	_ = h.proc.Signal(os.Interrupt)

	t := time.NewTimer(s.cfg.ShutdownTimeout)
	defer t.Stop()

	for {
		select {
		case <-h.exitCh:
			s.setState(StateStopped)
			return runShutdown
		case <-t.C:
			s.logger.Warn("shutdown deadline exceeded; killing plugin", "plugin_id", s.cfg.PluginID)
			h.kill()
			<-h.exitCh
			s.setState(StateStopped)
			return runShutdown
		case <-s.wake:
			// A disable racing shutdown still resolves to a stopped process.
		}
	}
}

// launchEndpoint chooses the per-launch transport endpoint. Windows uses
// loopback TCP on an OS-assigned port; other platforms use a Unix socket in a
// private temporary directory that cleanup removes.
func launchEndpoint() (endpoint string, cleanup func(), _ error) {
	if runtime.GOOS == "windows" {
		return "tcp://127.0.0.1:0", func() {}, nil
	}
	dir, err := os.MkdirTemp("", "cloudpath-plugin-*")
	if err != nil {
		return "", nil, fmt.Errorf("pluginhost: create unix socket dir: %w", err)
	}
	return "unix://" + filepath.Join(dir, "plugin.sock"), func() { _ = os.RemoveAll(dir) }, nil
}

// validateHandshake checks the handshake against the current launch identity
// and the endpoint that was handed to the process.
func (s *Supervisor) validateHandshake(launch *launch, h Handshake) error {
	s.mu.Lock()
	launchID, proof := s.launchID, s.proof
	s.mu.Unlock()

	if h.PluginID != s.cfg.PluginID {
		return fmt.Errorf("handshake plugin id %q, want %q", h.PluginID, s.cfg.PluginID)
	}
	if h.Protocol != s.cfg.Protocol {
		return fmt.Errorf("handshake protocol %q, want %q", h.Protocol, s.cfg.Protocol)
	}
	if h.ProtocolVersion != s.cfg.ProtocolVersion {
		return fmt.Errorf("handshake protocol version %d, want %d", h.ProtocolVersion, s.cfg.ProtocolVersion)
	}
	if h.LaunchID != launchID {
		return errors.New("handshake launch id mismatch")
	}
	if h.Proof != proof {
		return errors.New("handshake proof mismatch")
	}
	if launch != nil {
		if h.Transport != launch.endpoint.Scheme {
			return fmt.Errorf("handshake transport %q, want %q", h.Transport, launch.endpoint.Scheme)
		}
		if h.Endpoint != launch.endpoint.Addr {
			return fmt.Errorf("handshake endpoint %q, want %q", h.Endpoint, launch.endpoint.Addr)
		}
	}
	return nil
}

// State returns the current lifecycle state.
func (s *Supervisor) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// Snapshot returns a point-in-time view of the supervisor.
func (s *Supervisor) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Snapshot{
		State:              s.state,
		Crashes:            s.crashes,
		Restarts:           s.restarts,
		Launches:           s.launches,
		HandshakeCompleted: s.handshaked,
		Backoffs:           append([]time.Duration(nil), s.backoffs...),
		Kind:               s.cfg.Kind,
		Endpoint:           s.lastEndpoint,
		RPCConnections:     s.rpcConns,
	}
}

// Logs returns the retained, already-redacted plugin log lines.
func (s *Supervisor) Logs() []LogEntry {
	return s.collector.snapshot()
}

// Disable requests an immediate stop and prevents any further launches until
// Enable is called.
func (s *Supervisor) Disable() {
	s.mu.Lock()
	changed := !s.disabled
	s.disabled = true
	s.mu.Unlock()
	if changed {
		s.wakeLoop()
	}
}

// Enable allows the supervisor to start the plugin again.
func (s *Supervisor) Enable() {
	s.mu.Lock()
	changed := s.disabled
	s.disabled = false
	s.mu.Unlock()
	if changed {
		s.wakeLoop()
	}
}

// Restart requests a supervised in-place restart: the current process is
// killed and a fresh one is launched inside the same Run loop without touching
// the crash/restart budget. The Manager health failure policy uses this so
// health restarts are never counted as crashes.
func (s *Supervisor) Restart() {
	select {
	case s.restartCh <- struct{}{}:
	default:
	}
}

func (s *Supervisor) isDisabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.disabled
}

func (s *Supervisor) setState(to State) {
	s.mu.Lock()
	if s.state != to {
		if !s.state.CanTransition(to) {
			s.logger.Warn("pluginhost: illegal state transition",
				"plugin_id", s.cfg.PluginID,
				"from", s.state.String(),
				"to", to.String(),
			)
		}
		s.state = to
	}
	s.mu.Unlock()
}

func (s *Supervisor) wakeLoop() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// procHandle is the per-launch bookkeeping for one process.
type procHandle struct {
	proc      Process
	collector *logCollector
	exitCh    chan error
	hsCh      chan handshakeResult
	dupCh     chan struct{}
}

type handshakeResult struct {
	hs  Handshake
	err error
}

func newProcHandle(proc Process, collector *logCollector) *procHandle {
	h := &procHandle{
		proc:      proc,
		collector: collector,
		exitCh:    make(chan error, 1),
		hsCh:      make(chan handshakeResult, 1),
		dupCh:     make(chan struct{}, 1),
	}
	go func() { h.exitCh <- proc.Wait() }()
	go h.readStdout()
	go h.readStderr()
	return h
}

func (h *procHandle) kill() {
	if h != nil && h.proc != nil {
		_ = h.proc.Kill()
	}
}

// readStdout consumes the unique handshake line and then treats every later
// line as a log line; any additional handshake-shaped line is reported as a
// duplicate-handshake violation.
func (h *procHandle) readStdout() {
	sc := bufio.NewScanner(h.proc.Stdout())
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	first := true
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if first {
			first = false
			hs, err := ParseHandshake(line)
			select {
			case h.hsCh <- handshakeResult{hs: hs, err: err}:
			default:
			}
			continue
		}
		if IsHandshakeLine(line) {
			select {
			case h.dupCh <- struct{}{}:
			default:
			}
			continue
		}
		h.collector.append("stdout", line)
	}
}

func (h *procHandle) readStderr() {
	sc := bufio.NewScanner(h.proc.Stderr())
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		h.collector.append("stderr", line)
	}
}

// logCollector retains bounded, redacted plugin logs and emits structured log
// records.
type logCollector struct {
	mu       sync.Mutex
	entries  []LogEntry
	cap      int
	logger   *slog.Logger
	pluginID string
}

func (c *logCollector) setCap(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cap = n
}

func (c *logCollector) append(stream, line string) {
	line = Redact(line)
	c.mu.Lock()
	c.entries = append(c.entries, LogEntry{Stream: stream, Line: line})
	if c.cap > 0 && len(c.entries) > c.cap {
		c.entries = c.entries[len(c.entries)-c.cap:]
	}
	c.mu.Unlock()
	c.logger.Info("plugin log", "plugin_id", c.pluginID, "stream", stream, "line", line)
}

func (c *logCollector) snapshot() []LogEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]LogEntry, len(c.entries))
	copy(out, c.entries)
	return out
}

func newLaunchID() string { return randomHex(8) }
func newProof() string    { return randomHex(16) }

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
