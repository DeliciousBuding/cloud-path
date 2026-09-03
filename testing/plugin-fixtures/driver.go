// Package pluginfixtures provides the minimal simulated Driver plugin used by
// the process-host E2E tests. It is a real, buildable, install-style binary
// built only on the public SDK: it never touches real hardware or the network,
// and it never imports internal/*.
package pluginfixtures

import (
	"context"
	"os"
	"sync"

	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/driver"
	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/status"
)

// DriverID is the stable driver identifier reported by Describe.
const DriverID = "io.test.driver-fixture"

// Option tunes a simulated Driver.
type Option func(*Driver)

// WithEventsFile records RPC lifecycle events (one name per line) to path. The
// E2E host uses it to observe that Shutdown was actually delivered over RPC.
func WithEventsFile(path string) Option {
	return func(d *Driver) { d.eventsFile = path }
}

// Driver is a minimal simulated DriverServer. It is a no-hardware reference
// fixture whose only purpose is exercising the full binary -> Host E2E path.
type Driver struct {
	mu         sync.Mutex
	shutdown   bool
	eventsFile string

	// OnShutdown is invoked after the host delivers the Shutdown RPC.
	OnShutdown func()
}

// New returns a fresh simulated Driver.
func New(opts ...Option) *Driver {
	d := &Driver{}
	for _, o := range opts {
		o(d)
	}
	return d
}

var _ driver.DriverServer = (*Driver)(nil)

func (d *Driver) record(event string) {
	if d.eventsFile == "" {
		return
	}
	f, err := os.OpenFile(d.eventsFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	_, _ = f.WriteString(event + "\n")
	_ = f.Close()
}

// Initialize negotiates the driver protocol version.
func (d *Driver) Initialize(_ context.Context, _ *driver.InitializeRequest) (*driver.InitializeResponse, error) {
	return &driver.InitializeResponse{
		NegotiatedProtocolVersion: driver.ProtocolVersion,
		Status:                    status.New(),
	}, nil
}

// Describe returns a stable descriptor.
func (d *Driver) Describe(context.Context) (*driver.DriverDescriptor, error) {
	return &driver.DriverDescriptor{
		DriverID:       DriverID,
		Version:        "0.1.0",
		SchemaVersions: []string{driver.SchemaVersion},
		Capabilities: []driver.CapabilityDescriptor{
			{ID: "cloudpath.dev/capability/fixture@1", Title: "Fixture"},
		},
	}, nil
}

// ConfigureInstance accepts any bounded configuration.
func (d *Driver) ConfigureInstance(_ context.Context, req *driver.ConfigureInstanceRequest) (*driver.ConfigureInstanceResponse, error) {
	return &driver.ConfigureInstanceResponse{
		PluginInstanceID: req.PluginInstanceID,
		AppliedRevision:  req.ConfigRevision,
		Status:           status.New(),
	}, nil
}

// Discover returns no devices.
func (d *Driver) Discover(context.Context, *driver.DiscoverRequest, driver.DiscoveryWriter) error {
	return nil
}

// OpenDevice accepts the device id.
func (d *Driver) OpenDevice(_ context.Context, req *driver.OpenDeviceRequest) (*driver.OpenDeviceResponse, error) {
	return &driver.OpenDeviceResponse{Status: status.New()}, nil
}

// CloseDevice accepts the device id.
func (d *Driver) CloseDevice(_ context.Context, req *driver.CloseDeviceRequest) (*driver.CloseDeviceResponse, error) {
	return &driver.CloseDeviceResponse{Status: status.New()}, nil
}

// Watch blocks until the stream is canceled; the fixture publishes no data.
func (d *Driver) Watch(ctx context.Context, _ *driver.WatchRequest, _ driver.DriverMessageWriter) error {
	<-ctx.Done()
	return ctx.Err()
}

// Execute reports a successful no-op command.
func (d *Driver) Execute(_ context.Context, req *driver.ExecuteRequest) (*driver.ExecuteResponse, error) {
	return &driver.ExecuteResponse{
		CommandID:      req.IdempotencyKey,
		IdempotencyKey: req.IdempotencyKey,
		Status:         status.New(),
		State:          driver.CommandStateSucceeded,
	}, nil
}

// Health reports serving until shutdown.
func (d *Driver) Health(context.Context) (*driver.HealthResponse, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	state := driver.HealthStateServing
	if d.shutdown {
		state = driver.HealthStateNotServing
	}
	return &driver.HealthResponse{State: state}, nil
}

// Shutdown marks the driver stopped and notifies the process entrypoint.
func (d *Driver) Shutdown(_ context.Context, _ *driver.ShutdownRequest) (*driver.ShutdownResponse, error) {
	d.mu.Lock()
	first := !d.shutdown
	d.shutdown = true
	onShutdown := d.OnShutdown
	d.mu.Unlock()
	if first {
		d.record("shutdown")
	}
	if onShutdown != nil {
		onShutdown()
	}
	return &driver.ShutdownResponse{Status: status.New()}, nil
}
