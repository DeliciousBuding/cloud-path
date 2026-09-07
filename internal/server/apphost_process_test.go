package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/appruntime"
	"github.com/DeliciousBuding/cloud-path/internal/pluginhost"
	"github.com/DeliciousBuding/cloud-path/internal/store"
	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/application"
	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/status"
	"github.com/DeliciousBuding/cloud-path/sdk/go/pluginmain"
	"github.com/DeliciousBuding/cloud-path/sdk/go/rpc"
	"github.com/DeliciousBuding/cloud-path/sdk/go/transport"
)

const appRoutingPlugin = "io.test.application-routing"

// The normal test executable also acts as a generic application fixture. It
// never opens hardware or external services; each copy's directory is its
// actual version, so a host's desired metadata cannot fake a version change.
func TestMain(m *testing.M) {
	if os.Getenv(pluginhost.EnvPluginID) == appRoutingPlugin && os.Getenv(pluginhost.EnvProtocol) == "application" {
		os.Exit(runAppRoutingHelper())
	}
	os.Exit(m.Run())
}

type appRoutingReport struct {
	PID     int
	Version string
	Calls   map[string]int
	Configs map[string]map[string]string
}

type appRoutingServer struct {
	application.ApplicationServer
	mu     sync.Mutex
	report appRoutingReport
	cancel context.CancelFunc
}

func runAppRoutingHelper() int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &appRoutingServer{cancel: cancel, report: appRoutingReport{
		PID: os.Getpid(), Version: filepath.Base(filepath.Dir(os.Args[0])),
		Calls: map[string]int{}, Configs: map[string]map[string]string{},
	}}
	if err := pluginmain.Run(ctx, os.Stdout, os.Stderr, func(tr transport.Transport) *rpc.Server { return application.NewRPCServer(tr, s) }); err != nil {
		return 2
	}
	return 0
}

func (s *appRoutingServer) Initialize(_ context.Context, req *application.InitializeRequest) (*application.InitializeResponse, error) {
	if req.PluginVersion != "" && req.PluginVersion != s.report.Version {
		return &application.InitializeResponse{Status: status.Errorf(status.CodeInvalidArgument, "wrong process version")}, nil
	}
	return &application.InitializeResponse{NegotiatedProtocolVersion: application.ProtocolVersion, Status: status.New()}, nil
}
func (s *appRoutingServer) Describe(context.Context) (*application.ApplicationDescriptor, error) {
	return &application.ApplicationDescriptor{ApplicationID: "test-application", Version: s.report.Version}, nil
}
func (s *appRoutingServer) ConfigureInstance(_ context.Context, req *application.ConfigureInstanceRequest) (*application.ConfigureInstanceResponse, error) {
	var config map[string]string
	if err := json.Unmarshal(req.Config, &config); err != nil {
		return nil, err
	}
	if config["target"] != req.PluginInstanceID || config["version"] != s.report.Version {
		return &application.ConfigureInstanceResponse{Status: status.Errorf(status.CodeInvalidArgument, "configuration reached the wrong process")}, nil
	}
	s.mu.Lock()
	s.report.Configs[req.PluginInstanceID] = config
	s.report.Calls[req.PluginInstanceID]++
	s.mu.Unlock()
	return &application.ConfigureInstanceResponse{PluginInstanceID: req.PluginInstanceID, AppliedRevision: req.ConfigRevision, Status: status.New()}, nil
}
func (s *appRoutingServer) ValidateBinding(context.Context, *application.ValidateBindingRequest) (*application.ValidateBindingResponse, error) {
	return &application.ValidateBindingResponse{Valid: true}, nil
}
func (s *appRoutingServer) HandleEvents(ctx context.Context, reader application.ApplicationEventReader, _ application.ApplicationEffectWriter) error {
	for {
		if _, err := reader.Recv(ctx); err != nil {
			return err
		}
	}
}
func (s *appRoutingServer) Health(context.Context) (*application.HealthResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.Marshal(s.report)
	if err != nil {
		return nil, err
	}
	return &application.HealthResponse{State: application.HealthStateServing, Instances: []application.InstanceHealth{{Detail: string(data)}}}, nil
}
func (s *appRoutingServer) Shutdown(context.Context, *application.ShutdownRequest) (*application.ShutdownResponse, error) {
	time.AfterFunc(20*time.Millisecond, s.cancel)
	return &application.ShutdownResponse{Status: status.New()}, nil
}

func TestAppHostRoutesProtocolToExactProcess(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	paths := map[string]string{}
	for _, version := range []string{"0.1.0", "0.2.0"} {
		dir := filepath.Join(root, version)
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		paths[version] = filepath.Join(dir, "app-test.exe")
		if err := os.WriteFile(paths[version], binary, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, isolation := range []pluginhost.Isolation{pluginhost.IsolationShared, pluginhost.IsolationPerInstance} {
		t.Run(isolation.String(), func(t *testing.T) {
			srv, _ := setup(t)
			h, err := NewAppHost(srv, AppHostConfig{Enabled: true, PluginsDir: t.TempDir(), LockPath: filepath.Join(t.TempDir(), "plugins.lock"), StateDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(h.Close)
			for version, path := range paths {
				if err := h.mgr.RegisterInstallation(pluginhost.Installation{PluginID: appRoutingPlugin, Version: version, Path: path, Kind: pluginhost.KindApplication}); err != nil {
					t.Fatal(err)
				}
			}
			specs := []pluginhost.InstanceSpec{
				{Tenant: "1", ID: "app-alpha", PluginID: appRoutingPlugin, Version: "0.1.0", Isolation: isolation},
				{Tenant: "1", ID: "app-beta", PluginID: appRoutingPlugin, Version: "0.2.0", Isolation: isolation},
			}
			if isolation == pluginhost.IsolationPerInstance {
				specs[1].Version = "0.1.0"
			}
			for _, spec := range specs {
				if err := h.mgr.ReconcileInstance(context.Background(), spec, true); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := h.mgr.ApplicationClient(appRoutingPlugin); !errors.Is(err, pluginhost.ErrAmbiguousInstance) {
				t.Fatalf("ambiguous application lookup did not fail closed: %v", err)
			}
			var reports []appRoutingReport
			for i, spec := range specs {
				config, err := json.Marshal(map[string]string{"target": spec.ID, "version": spec.Version})
				if err != nil {
					t.Fatal(err)
				}
				wrapped, err := json.Marshal(map[string]string{appConfigKey: string(config)})
				if err != nil {
					t.Fatal(err)
				}
				tenant, _ := strconv.ParseInt(spec.Tenant, 10, 64)
				row := store.PluginInstanceRow{TenantID: tenant, EdgeID: AppHostEdgeID, InstanceID: spec.ID, PluginID: spec.PluginID, Version: spec.Version, Enabled: true, Isolation: spec.Isolation.String(), ConfigJSON: string(wrapped), Revision: uint64(i + 1)}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				if err := h.startInstance(ctx, row); err != nil {
					cancel()
					t.Fatalf("start %s in exact process: %v", spec.ID, err)
				}
				cli, err := h.mgr.ApplicationClientForInstance(spec.Tenant, spec.ID)
				if err != nil {
					cancel()
					t.Fatal(err)
				}
				health, err := cli.Health(ctx)
				cancel()
				if err != nil || len(health.Instances) != 1 {
					t.Fatalf("process report: %+v %v", health, err)
				}
				var report appRoutingReport
				if err := json.Unmarshal([]byte(health.Instances[0].Detail), &report); err != nil {
					t.Fatal(err)
				}
				if report.PID <= 0 || report.PID == os.Getpid() || report.Version != spec.Version || len(report.Configs) != 1 || report.Configs[spec.ID]["target"] != spec.ID || report.Calls[spec.ID] != 1 {
					t.Fatalf("wrong physical process/config for %s: %+v", spec.ID, report)
				}
				reports = append(reports, report)
				if inst, err := h.rt.GetInstance(spec.ID); err != nil || inst.State != appruntime.StateRunning {
					t.Fatalf("protocol instance not running: %+v %v", inst, err)
				}
			}
			if reports[0].PID == reports[1].PID {
				t.Fatal("fixture did not use distinct OS processes")
			}
			target := appruntime.InstanceSpec{TenantID: "99", PluginInstanceID: specs[0].ID, PluginID: appRoutingPlugin, PluginVersion: specs[0].Version}
			if _, err := h.applicationClient(target); !errors.Is(err, pluginhost.ErrInstanceNotFound) {
				t.Fatalf("wrong tenant routed to existing app: %v", err)
			}
			target.TenantID = "1"
			target.PluginVersion = "9.9.9"
			if _, err := h.applicationClient(target); err == nil {
				t.Fatal("unapplied desired version used stale process")
			}
		})
	}
}
