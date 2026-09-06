package edge

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/pluginhost"
	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/driver"
	"github.com/DeliciousBuding/cloud-path/sdk/go/pluginmain"
	"github.com/DeliciousBuding/cloud-path/sdk/go/pluginruntime"
	"github.com/DeliciousBuding/cloud-path/sdk/go/rpc"
	"github.com/DeliciousBuding/cloud-path/sdk/go/transport"
)

const (
	actionMetadataHelperEnv = "CLOUDPATH_EDGE_ACTION_METADATA_HELPER"
	actionMetadataModeEnv   = "CLOUDPATH_EDGE_ACTION_METADATA_MODE"
	actionMetadataStopEnv   = "CLOUDPATH_EDGE_ACTION_METADATA_STOP"
	actionMetadataInput     = `{"type":"object","required":["value"],"properties":{"value":{"type":"integer"}}}`
)

// Re-exec the test binary, following internal/pluginhost/rpc_test.go. This
// helper has no device discovery, Execute implementation or serial access.
func TestDriverActionMetadataHelperProcess(t *testing.T) {
	if os.Getenv(actionMetadataHelperEnv) != "1" {
		return
	}
	os.Exit(runActionMetadataHelperProcess())
}

func runActionMetadataHelperProcess() int {
	mode := os.Getenv(actionMetadataModeEnv)
	if mode != "metadata" && mode != "legacy" {
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stop := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- pluginmain.Run(ctx, os.Stdout, os.Stderr, func(tr transport.Transport) *rpc.Server {
			return driver.NewRPCServer(tr, &actionMetadataProcessDriver{metadata: mode == "metadata", stop: stop})
		})
	}()
	select {
	case <-stop:
		// Same flush grace as the existing Host helper: let the SDK dispatcher
		// write the Shutdown response before closing the authenticated socket.
		time.Sleep(100 * time.Millisecond)
		cancel()
	case <-done:
		return 3 // The host must stop us through the protocol, not an early exit.
	}
	select {
	case err := <-done:
		if err != nil {
			return 4
		}
	case <-time.After(5 * time.Second):
		return 5
	}
	if err := os.WriteFile(os.Getenv(actionMetadataStopEnv), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		return 6
	}
	return 0
}

type actionMetadataProcessDriver struct {
	driver.DriverServer // Unused device/command methods must never be called.
	metadata            bool
	stop                chan<- struct{}
}

func (d *actionMetadataProcessDriver) Initialize(context.Context, *driver.InitializeRequest) (*driver.InitializeResponse, error) {
	return &driver.InitializeResponse{
		NegotiatedProtocolVersion: driver.ProtocolVersion,
		RuntimeID:                 strconv.Itoa(os.Getpid()),
	}, nil
}

func (d *actionMetadataProcessDriver) Describe(context.Context) (*driver.DriverDescriptor, error) {
	action := driver.ActionDescriptor{
		Name: "configure", InputSchemaJSON: actionMetadataInput, ResultSchemaJSON: `{"type":"boolean"}`,
	}
	if d.metadata {
		action.Title = "配置输出"
		action.Description = "  参数会写入设备状态。  "
		action.Destructive = true
		action.Confirmation = "确认执行「配置输出」？\n该操作会覆盖现有值。"
	}
	return &driver.DriverDescriptor{
		DriverID: "io.test.edge-action-metadata", Version: "1.0.0", SchemaVersions: []string{"1"},
		Capabilities: []driver.CapabilityDescriptor{{
			ID: "io.test/capability/process-settings@1", Title: "Process settings", Actions: []driver.ActionDescriptor{action},
		}},
	}, nil
}

func (d *actionMetadataProcessDriver) Health(context.Context) (*driver.HealthResponse, error) {
	return &driver.HealthResponse{State: driver.HealthStateServing}, nil
}

func (d *actionMetadataProcessDriver) Shutdown(context.Context, *driver.ShutdownRequest) (*driver.ShutdownResponse, error) {
	select {
	case d.stop <- struct{}{}:
	default:
	}
	return &driver.ShutdownResponse{}, nil
}

// A software-only gate: real child process -> authenticated Host socket/RPC ->
// externalAdapter -> public Capability model/JSON. It is not board acceptance.
func TestDriverActionMetadataSubprocess(t *testing.T) {
	for _, mode := range []string{"metadata", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			stopFile := filepath.Join(t.TempDir(), "clean-shutdown")
			sup := pluginhost.NewSupervisor(pluginhost.Config{
				PluginID: "io.test.edge-action-metadata", Kind: pluginhost.KindDriver,
				Protocol: "driver", ProtocolVersion: driver.ProtocolVersion,
				Command: pluginhost.CommandSpec{
					Path: os.Args[0], Args: []string{"-test.run=^TestDriverActionMetadataHelperProcess$"},
					Env: append(os.Environ(), actionMetadataHelperEnv+"=1", actionMetadataModeEnv+"="+mode, actionMetadataStopEnv+"="+stopFile),
				},
				HandshakeTimeout: 5 * time.Second, ShutdownTimeout: 2 * time.Second,
				HealthCheckInterval: 50 * time.Millisecond, MaxRestarts: 0,
			}, pluginhost.ExecRunner{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			done := make(chan struct{})
			var runErr error
			go func() { runErr = sup.Run(ctx); close(done) }()
			childPID := 0
			t.Cleanup(func() {
				cancel()
				select {
				case <-done:
					if runErr != nil {
						t.Errorf("Host stop: %v", runErr)
					}
				case <-time.After(10 * time.Second):
					t.Error("Host did not reap the helper process")
					return
				}
				if sup.State() != pluginhost.StateStopped {
					t.Errorf("Host state after shutdown = %s", sup.State())
				}
				if childPID > 0 {
					stoppedPID, err := os.ReadFile(stopFile)
					if err != nil || string(stoppedPID) != strconv.Itoa(childPID) {
						t.Errorf("helper did not complete protocol shutdown: %v", err)
					} else {
						t.Logf("helper_pid=%d clean_shutdown=true host_state=%s", childPID, sup.State())
					}
				}
			})

			tick := time.NewTicker(5 * time.Millisecond)
			defer tick.Stop()
			for sup.State() != pluginhost.StateHealthy {
				select {
				case <-done:
					t.Fatalf("helper exited before ready: %v (state=%s)", runErr, sup.State())
				case <-ctx.Done():
					t.Fatalf("Host never became healthy: %v (state=%s)", ctx.Err(), sup.State())
				case <-tick.C:
				}
			}
			snapshot := sup.Snapshot()
			if !snapshot.HandshakeCompleted || snapshot.RPCConnections != 1 || snapshot.Launches != 1 || snapshot.Restarts != 0 {
				t.Fatalf("Host handshake=%t connections=%d launches=%d restarts=%d", snapshot.HandshakeCompleted, snapshot.RPCConnections, snapshot.Launches, snapshot.Restarts)
			}
			endpoint, err := pluginruntime.ParseEndpoint(snapshot.Endpoint)
			if err != nil {
				t.Fatal(err)
			}
			if runtime.GOOS == "windows" {
				if endpoint.Scheme != "tcp" || !strings.HasPrefix(endpoint.Addr, "127.0.0.1:") {
					t.Fatal("expected OS-assigned loopback TCP transport")
				}
			} else if endpoint.Scheme != "unix" {
				t.Fatalf("expected Unix socket transport, got %q", endpoint.Scheme)
			}
			client := sup.DriverClient()
			if client == nil {
				t.Fatal("healthy Host has no Driver RPC client")
			}
			initialized, err := client.Initialize(ctx, &driver.InitializeRequest{ProtocolVersion: driver.ProtocolVersion})
			if err != nil {
				t.Fatalf("Initialize over child RPC: %v", err)
			}
			childPID, err = strconv.Atoi(initialized.RuntimeID)
			if err != nil || childPID <= 0 || childPID == os.Getpid() {
				t.Fatalf("RPC did not originate in a distinct process: parent=%d runtime_id=%q", os.Getpid(), initialized.RuntimeID)
			}

			adapter := newExternalAdapter(&actionMetadataRPCHost{client: client}, "io.test.edge-action-metadata")
			caps := adapter.Capabilities()
			if len(caps) != 1 {
				t.Fatalf("expected one capability from child Describe, got %d", len(caps))
			}
			if err := caps[0].Validate(); err != nil {
				t.Fatalf("public Capability: %v", err)
			}
			if got := adapter.SupportedCommands(); !reflect.DeepEqual(got, []string{"configure"}) {
				t.Fatalf("command whitelist changed: %v", got)
			}
			action, ok := caps[0].Spec.Actions["configure"]
			if !ok {
				t.Fatal("action missing after externalAdapter conversion")
			}
			var schema map[string]any
			if err := json.Unmarshal([]byte(actionMetadataInput), &schema); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(action.InputSchema, schema) {
				t.Fatalf("inputSchema lost: %+v", action.InputSchema)
			}
			want := map[string]any{"inputSchema": schema}
			if mode == "metadata" {
				if action.Title != "配置输出" || action.Description != "  参数会写入设备状态。  " || !action.Destructive || action.Confirmation != "确认执行「配置输出」？\n该操作会覆盖现有值。" {
					t.Fatalf("metadata lost across child/Host/Edge: %+v", action)
				}
				want["title"], want["description"] = "配置输出", "  参数会写入设备状态。  "
				want["destructive"], want["confirmation"] = true, "确认执行「配置输出」？\n该操作会覆盖现有值。"
			} else if action.Title != "" || action.Description != "" || action.Destructive || action.Confirmation != "" {
				t.Fatalf("legacy descriptor gained metadata: %+v", action)
			}

			encoded, err := json.Marshal(caps[0])
			if err != nil {
				t.Fatal(err)
			}
			var public struct {
				Spec struct {
					Actions map[string]map[string]any `json:"actions"`
				} `json:"spec"`
			}
			if err := json.Unmarshal(encoded, &public); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(public.Spec.Actions["configure"], want) {
				t.Fatalf("public JSON mismatch: %s", encoded)
			}
			t.Logf("mode=%s parent_pid=%d helper_pid=%d transport=%s handshake=true rpc_connections=%d public_metadata=preserved", mode, os.Getpid(), childPID, endpoint.Scheme, snapshot.RPCConnections)
		})
	}
}
