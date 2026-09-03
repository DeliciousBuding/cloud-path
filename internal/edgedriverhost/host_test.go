package edgedriverhost

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/plugincontrol"
	"github.com/DeliciousBuding/cloud-path/internal/pluginhost"
	"github.com/DeliciousBuding/cloud-path/internal/registry"
)

type fakeManager struct {
	mu       sync.Mutex
	closed   bool
	closeErr error
	startErr error
	block    chan struct{}
}

func (m *fakeManager) RegisterInstallation(pluginhost.Installation) error { return nil }
func (m *fakeManager) CreateInstance(spec pluginhost.InstanceSpec) (pluginhost.Instance, error) {
	return pluginhost.Instance{ID: spec.ID, Tenant: spec.Tenant, PluginID: spec.PluginID, Version: spec.Version}, nil
}
func (m *fakeManager) Start(tenant, id string) error   { return m.startErr }
func (m *fakeManager) Disable(tenant, id string) error { return nil }
func (m *fakeManager) Remove(tenant, id string, _ ...pluginhost.RemoveOption) (pluginhost.RemoveResult, error) {
	return pluginhost.RemoveResult{DataPreserved: true}, nil
}
func (m *fakeManager) Close() error {
	if m.block != nil {
		<-m.block
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return m.closeErr
}
func (m *fakeManager) wasClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}

func newTestOptions(t *testing.T) (Options, *fakeManager) {
	t.Helper()
	root := t.TempDir()
	m := &fakeManager{}
	return Options{
		Manager:      m,
		PluginsDir:   filepath.Join(root, "plugins.d"),
		StateDir:     filepath.Join(root, "state"),
		LockPath:     filepath.Join(root, "plugins.lock"),
		Tenant:       "default",
		CloseTimeout: time.Second,
	}, m
}

func TestNewValidatesOptions(t *testing.T) {
	opts, _ := newTestOptions(t)
	opts.PluginsDir = ""
	if _, err := New(opts); err == nil {
		t.Fatal("缺 plugins dir 应报错")
	}
	opts, _ = newTestOptions(t)
	opts.StateDir = ""
	if _, err := New(opts); err == nil {
		t.Fatal("缺 state dir 应报错")
	}
	opts, _ = newTestOptions(t)
	opts.LockPath = ""
	if _, err := New(opts); err == nil {
		t.Fatal("缺 lock path 应报错")
	}
}

func TestHostStartLoadsAndCloseStops(t *testing.T) {
	opts, m := newTestOptions(t)
	h, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !m.wasClosed() {
		t.Fatal("manager 应已关闭")
	}
}

func TestHostStartFailureClosesManager(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, "io.github.example.driver-stcb", "contributes:\n  drivers:\n    - id: stcb\n")
	lock := registry.NewLockFile()
	lock.Plugins = []registry.LockedPlugin{
		{ID: "io.github.example.driver-stcb", Version: "0.1.0", Digest: "d1"},
	}
	lockPath := filepath.Join(root, "plugins.lock")
	if err := registry.WriteLockFile(lockPath, lock); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(root, "state")
	if err := plugincontrol.NewStore(stateDir).Save(plugincontrol.InstanceState{
		Tenant:     "default",
		InstanceID: "i1",
		PluginID:   "io.github.example.driver-stcb",
		Version:    "0.1.0",
		Enabled:    true,
		Isolation:  plugincontrol.IsolationShared,
	}); err != nil {
		t.Fatal(err)
	}

	m := &fakeManager{startErr: errors.New("launch fail")}
	h, err := New(Options{
		Manager:      m,
		PluginsDir:   root,
		StateDir:     stateDir,
		LockPath:     lockPath,
		Tenant:       "default",
		CloseTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Start(context.Background()); err == nil {
		t.Fatal("Start 失败应返回错误")
	}
	if !m.wasClosed() {
		t.Fatal("Start 失败时应清理已注册/已创建实例（关闭 manager），避免孤儿")
	}
}
func TestHostCloseDeadlineExceeded(t *testing.T) {
	opts, m := newTestOptions(t)
	m.block = make(chan struct{})
	opts.CloseTimeout = 50 * time.Millisecond
	h, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := h.Close(); err == nil {
		t.Fatal("超过 deadline 应报错")
	}
	if time.Since(start) > time.Second {
		t.Fatal("Close 应在 deadline 附近返回")
	}
	close(m.block)
}

type fakeDialer struct {
	mu    sync.Mutex
	calls int
}

func (d *fakeDialer) Dial(context.Context, string, string) (io.Closer, error) {
	d.mu.Lock()
	d.calls++
	d.mu.Unlock()
	return io.NopCloser(nil), nil
}

func TestHostOnlyNeverDials(t *testing.T) {
	opts, m := newTestOptions(t)
	d := &fakeDialer{}
	opts.Dialer = d
	h, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.calls != 0 {
		t.Fatalf("host-only 阶段不应拨号，calls=%d", d.calls)
	}
	_ = m
}

type fakeRunner struct {
	mu    sync.Mutex
	calls int
}

func (r *fakeRunner) Start(pluginhost.CommandSpec) (pluginhost.Process, error) {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	return nil, errors.New("fake runner must not launch real plugin")
}

func TestNewBuildsManagerFromRunnerWithoutLaunching(t *testing.T) {
	root := t.TempDir()
	r := &fakeRunner{}
	h, err := New(Options{
		Runner:       r,
		PluginsDir:   filepath.Join(root, "plugins.d"),
		StateDir:     filepath.Join(root, "state"),
		LockPath:     filepath.Join(root, "plugins.lock"),
		Tenant:       "default",
		CloseTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	// 空 lockfile + 空 state：Load 不会启动任何实例，fake Runner 也绝不派发进程。
	if err := h.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.calls != 0 {
		t.Fatalf("空 desired-state 不应启动进程，calls=%d", r.calls)
	}
}
