package pluginhost_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/pluginhost"
	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/driver"
	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/status"
	"github.com/DeliciousBuding/cloud-path/sdk/go/pluginmain"
	"github.com/DeliciousBuding/cloud-path/sdk/go/rpc"
	"github.com/DeliciousBuding/cloud-path/sdk/go/transport"
)

const reconfigureHelperEnv = "CLOUDPATH_RECONFIGURE_TEST_HELPER"
const reconfigureMarkerEnv = "CLOUDPATH_RECONFIGURE_TEST_MARKERS"
const reconfigurePluginID = "io.test.reconfigure"

type processReport struct {
	PID        int
	Version    string
	Tenant     string
	InstanceID string
	Configs    map[string]map[string]string
	Calls      map[string]int
	Revisions  map[string]uint32
}

type reconfigureDriver struct {
	driver.DriverServer
	mu            sync.Mutex
	report        processReport
	cancel        context.CancelFunc
	initialized   bool
	rejectRestore bool
}

// Only the test executable is launched. Version comes from its actual binary
// path, not a Manager snapshot; all RPC/configuration and shutdown cross an OS
// process boundary. The helper never opens devices or external services.
func TestReconfigureHelperProcess(t *testing.T) {
	if os.Getenv(reconfigureHelperEnv) != "1" {
		return
	}
	os.Exit(runReconfigureHelper())
}

func runReconfigureHelper() int {
	pid := strconv.Itoa(os.Getpid())
	markers := os.Getenv(reconfigureMarkerEnv)
	defer func() { _ = os.WriteFile(filepath.Join(markers, pid+".exit"), []byte("exited"), 0o600) }()
	version := filepath.Base(filepath.Dir(os.Args[0]))
	if version == "0.3.0" {
		return 7
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := &reconfigureDriver{cancel: cancel, report: processReport{
		PID: os.Getpid(), Version: version, Tenant: os.Getenv(pluginhost.EnvTenant), InstanceID: os.Getenv(pluginhost.EnvInstanceID),
		Configs: map[string]map[string]string{}, Calls: map[string]int{}, Revisions: map[string]uint32{},
	}}
	err := pluginmain.Run(ctx, os.Stdout, os.Stderr, func(tr transport.Transport) *rpc.Server { return driver.NewRPCServer(tr, server) })
	if err != nil {
		return 2
	}
	return 0
}

func (s *reconfigureDriver) Initialize(context.Context, *driver.InitializeRequest) (*driver.InitializeResponse, error) {
	s.mu.Lock()
	s.initialized = true
	s.mu.Unlock()
	return &driver.InitializeResponse{NegotiatedProtocolVersion: driver.ProtocolVersion, RuntimeID: strconv.Itoa(os.Getpid()), Status: status.New()}, nil
}
func (s *reconfigureDriver) Describe(context.Context) (*driver.DriverDescriptor, error) {
	return &driver.DriverDescriptor{DriverID: reconfigurePluginID, Version: s.report.Version}, nil
}
func (s *reconfigureDriver) ConfigureInstance(_ context.Context, req *driver.ConfigureInstanceRequest) (*driver.ConfigureInstanceResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var config map[string]string
	if err := json.Unmarshal(req.Config, &config); err != nil {
		return nil, err
	}
	if _, reserved := config[pluginhost.ConfigPathKey]; reserved {
		return &driver.ConfigureInstanceResponse{Status: status.Errorf(status.CodeInvalidArgument, "Host-only path was forwarded as plugin config")}, nil
	}
	if !s.initialized {
		return &driver.ConfigureInstanceResponse{Status: status.Errorf(status.CodeFailedPrecondition, "not initialized")}, nil
	}
	if s.rejectRestore && config["mode"] == "initial" {
		s.rejectRestore = false
		return &driver.ConfigureInstanceResponse{Status: status.Errorf(status.CodeUnavailable, "restore temporarily unavailable")}, nil
	}
	if req.ConfigRevision <= s.report.Revisions[req.PluginInstanceID] {
		return &driver.ConfigureInstanceResponse{Status: status.Errorf(status.CodeFailedPrecondition, "config revision did not advance")}, nil
	}
	s.report.Configs[req.PluginInstanceID] = config
	s.report.Calls[req.PluginInstanceID]++
	s.report.Revisions[req.PluginInstanceID] = req.ConfigRevision
	if err := s.save(); err != nil {
		return nil, err
	}
	// Deliberately mutate before rejecting: an in-place failure must restore the
	// last config, not merely leave the Manager's map unchanged.
	if config["mode"] == "reject-restore" {
		s.rejectRestore = true
	}
	if config["mode"] == "reject" || s.rejectRestore {
		return &driver.ConfigureInstanceResponse{Status: status.Errorf(status.CodeInvalidArgument, "config rejected")}, nil
	}
	return &driver.ConfigureInstanceResponse{PluginInstanceID: req.PluginInstanceID, AppliedRevision: req.ConfigRevision, Status: status.New()}, nil
}
func (s *reconfigureDriver) save() error {
	data, err := json.Marshal(s.report)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(os.Getenv(reconfigureMarkerEnv), strconv.Itoa(os.Getpid())+".json"), data, 0o600)
}
func (s *reconfigureDriver) Health(context.Context) (*driver.HealthResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.Marshal(s.report)
	if err != nil {
		return nil, err
	}
	health := driver.HealthStateServing
	if !s.initialized || s.report.Version == "0.4.0" {
		health = driver.HealthStateNotServing
	}
	return &driver.HealthResponse{State: health, Instances: []driver.InstanceHealth{{Detail: string(data)}}}, nil
}
func (s *reconfigureDriver) Shutdown(context.Context, *driver.ShutdownRequest) (*driver.ShutdownResponse, error) {
	_ = os.WriteFile(filepath.Join(os.Getenv(reconfigureMarkerEnv), strconv.Itoa(os.Getpid())+".shutdown"), []byte("shutdown RPC"), 0o600)
	time.AfterFunc(50*time.Millisecond, s.cancel)
	return &driver.ShutdownResponse{Status: status.New()}, nil
}

func TestManagerReconfigureProcess(t *testing.T) {
	root := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]string{}
	for _, version := range []string{"0.1.0", "0.2.0", "0.3.0", "0.4.0"} {
		dir := filepath.Join(root, version)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "plugin-test.exe")
		if err := os.WriteFile(path, binary, 0o700); err != nil {
			t.Fatal(err)
		}
		paths[version] = path
	}
	newHost := func(t *testing.T) (*pluginhost.Manager, *recordingRunner, string) {
		t.Helper()
		markers := t.TempDir()
		runner := newRecordingRunner()
		m := pluginhost.NewManager(pluginhost.ManagerOptions{
			Runner: runner, CommandArgs: []string{"-test.run=^TestReconfigureHelperProcess$"},
			CommandEnv:       []string{reconfigureHelperEnv + "=1", reconfigureMarkerEnv + "=" + markers},
			HandshakeTimeout: 3 * time.Second, ShutdownTimeout: time.Second, HealthCheckInterval: time.Hour,
		})
		t.Cleanup(func() {
			_ = m.Close()
			for i := 0; i < runner.StartCount(); i++ {
				path := filepath.Join(markers, strconv.Itoa(runner.Proc(i).Pid())+".exit")
				if _, err := os.Stat(path); err != nil {
					t.Errorf("test child did not exit: pid=%d: %v", runner.Proc(i).Pid(), err)
				}
			}
		})
		for version, path := range paths {
			if err := m.RegisterInstallation(pluginhost.Installation{PluginID: reconfigurePluginID, Version: version, Path: path}); err != nil {
				t.Fatal(err)
			}
		}
		return m, runner, markers
	}
	spec := func(tenant, id, mode string) pluginhost.InstanceSpec {
		return pluginhost.InstanceSpec{
			Tenant: tenant, ID: id, PluginID: reconfigurePluginID, Version: "0.1.0", Config: map[string]string{"mode": mode},
		}
	}
	reconcile := func(t *testing.T, m *pluginhost.Manager, s pluginhost.InstanceSpec) {
		t.Helper()
		if err := m.ReconcileInstance(context.Background(), s, true); err != nil {
			t.Fatal(err)
		}
	}
	readReport := func(t *testing.T, cli driver.DriverClient) processReport {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		health, err := cli.Health(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if health.State != driver.HealthStateServing || len(health.Instances) != 1 {
			t.Fatalf("health=%+v", health)
		}
		var report processReport
		if err := json.Unmarshal([]byte(health.Instances[0].Detail), &report); err != nil {
			t.Fatal(err)
		}
		if report.PID <= 0 || report.PID == os.Getpid() {
			t.Fatalf("not a child process: pid=%d", report.PID)
		}
		return report
	}
	client := func(t *testing.T, m *pluginhost.Manager) driver.DriverClient {
		t.Helper()
		cli, err := m.DriverClient(reconfigurePluginID)
		if err != nil {
			t.Fatal(err)
		}
		return cli
	}
	hasMarker := func(markers string, pid int, suffix string) bool {
		_, err := os.Stat(filepath.Join(markers, strconv.Itoa(pid)+suffix))
		return err == nil
	}

	t.Run("version-config-isolation-and-replay", func(t *testing.T) {
		m, r, markers := newHost(t)
		s := spec("tenant-a", "worker", "initial")
		s.Config[pluginhost.ConfigPathKey] = "local-config.json"
		dataPath := filepath.Join(markers, "plugin-data")
		if err := os.WriteFile(dataPath, []byte("preserve me"), 0o600); err != nil {
			t.Fatal(err)
		}
		reconcile(t, m, s)
		old := readReport(t, client(t, m))
		if old.Configs["tenant-a/worker"]["mode"] != "initial" {
			t.Fatalf("config not delivered: %+v", old)
		}
		reconcile(t, m, s)
		if got := readReport(t, client(t, m)); r.StartCount() != 1 || got.Calls["tenant-a/worker"] != 1 {
			t.Fatalf("replay had side effects: launches=%d report=%+v", r.StartCount(), got)
		}
		s.Version = "0.2.0"
		reconcile(t, m, s)
		cli := client(t, m)
		upgraded := readReport(t, cli)
		descriptor, err := cli.Describe(context.Background())
		if err != nil || descriptor.Version != "0.2.0" {
			t.Fatalf("Describe=%+v %v", descriptor, err)
		}
		snap, err := m.Snapshot(s.Tenant, s.ID)
		if err != nil || snap.Version != "0.2.0" || upgraded.Version != "0.2.0" || upgraded.PID == old.PID || !reflect.DeepEqual(upgraded.Configs, old.Configs) {
			t.Fatalf("version did not switch: old=%+v new=%+v manager=%+v err=%v", old, upgraded, snap, err)
		}
		if !hasMarker(markers, old.PID, ".shutdown") || !hasMarker(markers, old.PID, ".exit") {
			t.Fatal("old process was not gracefully retired")
		}
		s.Config = map[string]string{"mode": "updated"}
		reconcile(t, m, s)
		configured := readReport(t, client(t, m))
		if configured.PID != upgraded.PID || configured.Configs["tenant-a/worker"]["mode"] != "updated" {
			t.Fatalf("config change did not reach existing RPC instance: %+v", configured)
		}
		s.Isolation = pluginhost.IsolationPerInstance
		reconcile(t, m, s)
		dedicated := readReport(t, client(t, m))
		if dedicated.PID == configured.PID || dedicated.Tenant != s.Tenant || dedicated.InstanceID != s.ID || dedicated.Configs["tenant-a/worker"]["mode"] != "updated" {
			t.Fatalf("isolation did not change process: %+v", dedicated)
		}
		s.Config = nil
		reconcile(t, m, s)
		cleared := readReport(t, client(t, m))
		if len(cleared.Configs["tenant-a/worker"]) != 0 {
			t.Fatalf("config not cleared: %+v", cleared)
		}
		calls, launches := cleared.Calls["tenant-a/worker"], r.StartCount()
		s.Config = map[string]string{}
		reconcile(t, m, s)
		if got := readReport(t, client(t, m)); got.Calls["tenant-a/worker"] != calls || r.StartCount() != launches {
			t.Fatalf("nil/empty replay reconfigured: %+v", got)
		}
		s.Isolation = pluginhost.IsolationShared
		reconcile(t, m, s)
		shared := readReport(t, client(t, m))
		if shared.PID == dedicated.PID || shared.Tenant != "" || shared.InstanceID != "" {
			t.Fatalf("per-instance -> shared did not take effect: %+v", shared)
		}
		if data, err := os.ReadFile(dataPath); err != nil || string(data) != "preserve me" {
			t.Fatalf("plugin data changed: %q %v", data, err)
		}
		t.Logf("real child switch: v1 PID=%d -> v2 PID=%d -> dedicated PID=%d -> shared PID=%d; replay did not launch", old.PID, upgraded.PID, dedicated.PID, shared.PID)
	})

	t.Run("reserved-path-is-not-plugin-config", func(t *testing.T) {
		m, r, _ := newHost(t)
		s := spec("tenant-a", "worker", "unused")
		s.Config = map[string]string{pluginhost.ConfigPathKey: "local-config.json"}
		reconcile(t, m, s)
		before := readReport(t, client(t, m))
		if len(before.Configs) != 0 || len(before.Calls) != 0 {
			t.Fatalf("reserved-only config was sent to plugin: %+v", before)
		}
		s.Version = "0.2.0"
		reconcile(t, m, s)
		after := readReport(t, client(t, m))
		snap, err := m.Snapshot(s.Tenant, s.ID)
		if err != nil || snap.Config[pluginhost.ConfigPathKey] != "local-config.json" || len(after.Configs) != 0 || len(after.Calls) != 0 || r.StartCount() != 2 {
			t.Fatalf("reserved config compatibility lost: runtime=%+v snapshot=%+v err=%v", after, snap, err)
		}
	})

	t.Run("shared-siblings-and-tenants", func(t *testing.T) {
		m, r, markers := newHost(t)
		a, b, sibling := spec("tenant-a", "same-id", "a"), spec("tenant-b", "same-id", "b"), spec("tenant-a", "sibling", "c")
		for _, s := range []pluginhost.InstanceSpec{a, b, sibling} {
			reconcile(t, m, s)
		}
		cli := client(t, m)
		original := readReport(t, cli)
		if r.StartCount() != 1 || len(original.Configs) != 3 {
			t.Fatalf("shared process/config isolation: launches=%d report=%+v", r.StartCount(), original)
		}
		a.Config = map[string]string{"mode": "a-updated"}
		reconcile(t, m, a)
		inPlace := readReport(t, cli)
		if inPlace.Configs["tenant-a/same-id"]["mode"] != "a-updated" || inPlace.Configs["tenant-b/same-id"]["mode"] != "b" || inPlace.Configs["tenant-a/sibling"]["mode"] != "c" || r.StartCount() != 1 {
			t.Fatalf("shared config leaked: %+v", inPlace)
		}
		// A Host-only reference change must not reconfigure even this instance,
		// let alone overwrite another instance in the same shared process.
		a.Config = map[string]string{"mode": "a-updated", pluginhost.ConfigPathKey: "another-local-config.json"}
		reconcile(t, m, a)
		metadataOnly := readReport(t, cli)
		if metadataOnly.Calls["tenant-a/same-id"] != inPlace.Calls["tenant-a/same-id"] || !reflect.DeepEqual(metadataOnly.Configs, inPlace.Configs) {
			t.Fatalf("reserved metadata rewrote shared config: %+v", metadataOnly)
		}
		a.Isolation = pluginhost.IsolationPerInstance
		reconcile(t, m, a)
		if r.StartCount() != 2 {
			t.Fatalf("wanted one new dedicated process, got %d", r.StartCount())
		}
		dedicatedPID := r.Proc(1).Pid()
		a.Version = "0.2.0"
		reconcile(t, m, a)
		if r.StartCount() != 3 || !hasMarker(markers, dedicatedPID, ".exit") {
			t.Fatal("per-instance version upgrade retained its old process")
		}
		data, err := os.ReadFile(filepath.Join(markers, strconv.Itoa(r.Proc(2).Pid())+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var actualDedicated processReport
		if err := json.Unmarshal(data, &actualDedicated); err != nil {
			t.Fatal(err)
		}
		if actualDedicated.Version != "0.2.0" || actualDedicated.Tenant != a.Tenant || actualDedicated.InstanceID != a.ID || actualDedicated.Configs["tenant-a/same-id"]["mode"] != "a-updated" {
			t.Fatalf("wrong dedicated runtime after upgrade: %+v", actualDedicated)
		}
		snap, _ := m.Snapshot(a.Tenant, a.ID)
		if snap.Version != "0.2.0" || snap.Isolation != pluginhost.IsolationPerInstance {
			t.Fatalf("new binding=%+v", snap)
		}
		stillShared := readReport(t, cli)
		if stillShared.PID != original.PID || hasMarker(markers, original.PID, ".shutdown") {
			t.Fatal("moving one instance stopped shared siblings")
		}
		if err := m.ReconcileInstance(context.Background(), a, false); err != nil {
			t.Fatal(err)
		}
		if _, err := m.Remove(b.Tenant, b.ID); err != nil {
			t.Fatal(err)
		}
		if readReport(t, cli).PID != original.PID {
			t.Fatal("removing one tenant affected the other")
		}
		if err := m.ReconcileInstance(context.Background(), sibling, false); err != nil {
			t.Fatal(err)
		}
		if !hasMarker(markers, original.PID, ".exit") {
			t.Fatal("last shared instance did not stop the process")
		}
		t.Logf("shared PID=%d survived config, isolation and version changes in another binding/tenant", original.PID)
	})

	t.Run("failed-candidates-retain-live-binding", func(t *testing.T) {
		m, r, markers := newHost(t)
		original := spec("tenant-a", "worker", "initial")
		reconcile(t, m, original)
		oldClient := client(t, m)
		old := readReport(t, oldClient)
		if err := m.RegisterInstallation(pluginhost.Installation{PluginID: reconfigurePluginID, Version: "0.5.0", Path: filepath.Join(markers, "missing-binary.exe")}); err != nil {
			t.Fatal(err)
		}
		for _, failure := range []string{"uninstalled", "missing-binary", "exit-before-handshake", "not-serving", "config-rejection", "canceled"} {
			t.Run(failure, func(t *testing.T) {
				next := original
				ctx := context.Background()
				switch failure {
				case "uninstalled":
					next.Version = "9.9.9"
				case "missing-binary":
					next.Version = "0.5.0"
				case "exit-before-handshake":
					next.Version = "0.3.0"
				case "not-serving":
					next.Version = "0.4.0"
				case "config-rejection":
					next.Config = map[string]string{"mode": "reject"}
				case "canceled":
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					cancel()
					next.Version = "0.2.0"
				}
				launches := r.StartCount()
				if err := m.ReconcileInstance(ctx, next, true); err == nil {
					t.Fatal("failed candidate reported success")
				}
				snap, err := m.Snapshot(original.Tenant, original.ID)
				if err != nil || snap.Version != original.Version || !snap.Enabled || !reflect.DeepEqual(snap.Config, original.Config) {
					t.Fatalf("failed change polluted Manager: %+v %v", snap, err)
				}
				actual := readReport(t, oldClient)
				if actual.PID != old.PID || !reflect.DeepEqual(actual.Configs, old.Configs) || hasMarker(markers, old.PID, ".shutdown") {
					t.Fatalf("last live config/process changed: %+v", actual)
				}
				if (failure == "uninstalled" || failure == "canceled") && r.StartCount() != launches {
					t.Fatal("preflight failure launched a candidate")
				}
			})
		}
		// If restoration itself is rejected, reverting to the unchanged last-good
		// spec must retry configuration rather than report a false idempotent success.
		uncertain := original
		uncertain.Config = map[string]string{"mode": "reject-restore"}
		if err := m.ReconcileInstance(context.Background(), uncertain, true); err == nil {
			t.Fatal("failed rollback reported success")
		}
		if got := readReport(t, oldClient); got.Configs["tenant-a/worker"]["mode"] != "reject-restore" {
			t.Fatal("fixture did not simulate an uncertain rollback")
		}
		reconcile(t, m, original)
		if got := readReport(t, oldClient); !reflect.DeepEqual(got.Configs, old.Configs) {
			t.Fatalf("unchanged spec skipped uncertain config restoration: %+v", got)
		}

	})
}
