package pluginhost_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/pluginhost"
	pluginharness "github.com/DeliciousBuding/cloud-path/testing/plugin-harness"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testConfig(restarts int) pluginhost.Config {
	return pluginhost.Config{
		PluginID:         "io.test.plugin",
		Protocol:         "driver",
		ProtocolVersion:  1,
		Command:          pluginhost.CommandSpec{Path: "fake-plugin", Args: []string{"--fake"}},
		HandshakeTimeout: 300 * time.Millisecond,
		ShutdownTimeout:  2 * time.Second,
		MaxRestarts:      restarts,
		BaseBackoff:      10 * time.Millisecond,
		MaxBackoff:       50 * time.Millisecond,
		Jitter:           func(time.Duration) time.Duration { return 0 },
		LogBufferSize:    64,
	}
}

func waitState(t *testing.T, s *pluginhost.Supervisor, want pluginhost.State) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s.State() == want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("state = %s, want %s (snapshot=%+v)", s.State(), want, s.Snapshot())
}

func TestHandshakeSuccess(t *testing.T) {
	runner := pluginharness.NewFakeRunner()
	s := pluginhost.NewSupervisor(testConfig(0), runner, discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	waitState(t, s, pluginhost.StateHealthy)
	if !s.Snapshot().HandshakeCompleted {
		t.Fatal("handshake was not recorded")
	}
	started := runner.Started()
	if len(started) != 1 {
		t.Fatalf("started = %d processes, want 1", len(started))
	}
	env := started[0].Env()
	if env[pluginhost.EnvLaunchID] == "" || env[pluginhost.EnvHandshakeCookie] == "" {
		t.Fatal("launch identity was not injected into the process environment")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v after graceful cancel, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	if s.State() != pluginhost.StateStopped {
		t.Fatalf("final state = %s, want STOPPED", s.State())
	}
}

func TestHandshakeTimeout(t *testing.T) {
	runner := pluginharness.NewFakeRunner(pluginharness.WithAutoHandshake(false))
	s := pluginhost.NewSupervisor(testConfig(0), runner, discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = s.Run(ctx) }()

	waitState(t, s, pluginhost.StateDisabled)
	snap := s.Snapshot()
	if snap.HandshakeCompleted {
		t.Fatal("handshake completed but should have timed out")
	}
	if snap.Launches != 1 {
		t.Fatalf("launches = %d, want 1", snap.Launches)
	}
	if snap.Crashes != 1 {
		t.Fatalf("crashes = %d, want 1", snap.Crashes)
	}
	if snap.Restarts != 0 {
		t.Fatalf("restarts = %d, want 0 (budget 0)", snap.Restarts)
	}
	if !runner.Started()[0].Killed() {
		t.Fatal("process was not killed after handshake timeout")
	}
}

func TestRejectSecondHandshake(t *testing.T) {
	runner := pluginharness.NewFakeRunner()
	s := pluginhost.NewSupervisor(testConfig(0), runner, discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = s.Run(ctx) }()

	waitState(t, s, pluginhost.StateHealthy)
	p := runner.Started()[0]

	second := (pluginhost.Handshake{
		Marker:          pluginhost.HandshakeMarker,
		PluginID:        "io.test.plugin",
		Protocol:        "driver",
		ProtocolVersion: 1,
		Transport:       "tcp",
		Endpoint:        "127.0.0.1:40000",
		RPC:             "grpc",
		LaunchID:        "ignored",
		Proof:           "ignored",
	}).String()
	p.WriteStdout(second)

	waitState(t, s, pluginhost.StateDegraded)
	if p.Exited() {
		t.Fatal("plugin exited after a duplicate handshake; want degraded, not crashed")
	}
}

func TestCrashBackoffAndBudget(t *testing.T) {
	runner := pluginharness.NewFakeRunner(pluginharness.WithCrashAfterHandshake())
	s := pluginhost.NewSupervisor(testConfig(2), runner, discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = s.Run(ctx) }()

	waitState(t, s, pluginhost.StateDisabled)
	snap := s.Snapshot()
	if snap.Launches != 3 {
		t.Fatalf("launches = %d, want 3", snap.Launches)
	}
	if snap.Restarts != 2 {
		t.Fatalf("restarts = %d, want 2", snap.Restarts)
	}
	if snap.Crashes != 3 {
		t.Fatalf("crashes = %d, want 3", snap.Crashes)
	}
	if len(snap.Backoffs) != 2 {
		t.Fatalf("backoffs = %v, want 2 entries", snap.Backoffs)
	}
	if snap.Backoffs[0] != 10*time.Millisecond || snap.Backoffs[1] != 20*time.Millisecond {
		t.Fatalf("backoffs = %v, want [10ms 20ms] with deterministic jitter", snap.Backoffs)
	}
}

func TestDisableStopsRestart(t *testing.T) {
	runner := pluginharness.NewFakeRunner()
	s := pluginhost.NewSupervisor(testConfig(5), runner, discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = s.Run(ctx) }()

	waitState(t, s, pluginhost.StateHealthy)
	p := runner.Started()[0]

	s.Disable()
	waitState(t, s, pluginhost.StateDisabled)

	// The disabled plugin must be killed and must never be relaunched. Give the
	// supervisor several backoff windows to (wrongly) restart if it could.
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if runner.StartedCount() != 1 {
			t.Fatal("plugin was relaunched after disable")
		}
		time.Sleep(2 * time.Millisecond)
	}
	if !p.Killed() {
		t.Fatal("process was not killed after disable")
	}
	if s.State() != pluginhost.StateDisabled {
		t.Fatalf("state = %s, want DISABLED", s.State())
	}
}

func TestGracefulShutdown(t *testing.T) {
	runner := pluginharness.NewFakeRunner()
	s := pluginhost.NewSupervisor(testConfig(0), runner, discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	waitState(t, s, pluginhost.StateHealthy)
	p := runner.Started()[0]

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}

	if s.State() != pluginhost.StateStopped {
		t.Fatalf("final state = %s, want STOPPED", s.State())
	}
	if len(p.Signals()) == 0 {
		t.Fatal("graceful signal was not delivered")
	}
	if !p.Exited() {
		t.Fatal("process did not exit after graceful shutdown")
	}
	if p.Killed() {
		t.Fatal("process was force-killed, want graceful stop")
	}
}

func TestLogRedaction(t *testing.T) {
	cases := []struct{ in, want string }{
		{"boot password=hunter2 ready", "boot password=[REDACTED] ready"},
		{`{"token":"abc123","user":"alice"}`, `{"token":[REDACTED],"user":"alice"}`},
		{"Authorization: Bearer deadbeef", "Authorization=[REDACTED]"},
		{"secret_key: sup3r", "secret_key: [REDACTED]"},
	}
	for _, c := range cases {
		if got := pluginhost.Redact(c.in); got != c.want {
			t.Fatalf("Redact(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	runner := pluginharness.NewFakeRunner()
	s := pluginhost.NewSupervisor(testConfig(0), runner, discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = s.Run(ctx) }()

	waitState(t, s, pluginhost.StateHealthy)
	runner.Started()[0].WriteStderr("db password=hunter2 ready")

	var found bool
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		found = false
		for _, e := range s.Logs() {
			if strings.Contains(e.Line, "hunter2") {
				t.Fatal("secret value leaked into collected logs")
			}
			if e.Stream == "stderr" && strings.Contains(e.Line, "password=[REDACTED]") {
				found = true
			}
		}
		if found {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if !found {
		t.Fatal("redacted stderr line was not collected")
	}
}
