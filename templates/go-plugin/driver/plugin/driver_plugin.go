// Package plugin implements the CloudPath Driver Protocol v1 for the
// template driver. It is a fake/simulated capability: it never touches real
// hardware. It is the canonical reference for a minimal external Driver
// Plugin built only on the public SDK (sdk/go/cloudpath/v1/*), with no
// dependency on any internal/* package.
//
// Use it as the starting point for a real driver: replace the simulated
// temperature sensor with your own transport/protocol, keep the descriptor
// and permission surface stable, and drive the Watch stream with real
// observations.
package plugin

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/driver"
	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/status"
)

// Stable identities for this plugin. rename.py rewrites these literals when a
// developer copies the template into a real plugin repository.
const (
	PluginID      = "io.github.deliciousbuding.cloud-path-driver-template"
	pluginVersion = "0.1.0"

	// driverID is the stable per-version Driver identifier reported by Describe.
	// It must not change for the lifetime of the schema version.
	driverID = "template"

	// capabilityID is the namespaced Capability semantic version. Bump to @2
	// only on a breaking change; never mutate @1 in place.
	capabilityID    = "cloudpath.dev/capability/drivertemplate@1"
	capabilityTitle = "Driver Template Temperature"

	sensorEntityID = "entity/template-sensor"
	deviceID       = "device/template"
)

var (
	_ driver.DriverServer = (*Driver)(nil)
)

// Driver is a simulated in-memory DriverServer. It publishes a temperature
// observation on the Watch stream and executes a whitelisted "read" action.
type Driver struct {
	mu          sync.Mutex
	initialized bool
	shutdown    bool
	negotiated  uint32
	runtimeID   string
	rev         uint32

	// seq tracks the per-(instance, device) monotonic sequence, which is the
	// scope Core uses to deduplicate stream messages.
	seq map[string]uint64
}

// New returns a fresh Driver.
func New() *Driver {
	return &Driver{seq: make(map[string]uint64)}
}

func unavailable() *status.Status {
	return status.Errorf(status.CodeUnavailable, "template driver shut down")
}

// Initialize performs the launch handshake and negotiates the protocol
// version.
func (d *Driver) Initialize(ctx context.Context, req *driver.InitializeRequest) (*driver.InitializeResponse, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.shutdown {
		return nil, unavailable()
	}
	v, ok := driver.NegotiateProtocolVersion(req.SupportedProtocolVersions, req.ProtocolVersion, 1, driver.ProtocolVersion)
	if !ok {
		return &driver.InitializeResponse{
			NegotiatedProtocolVersion: 0,
			Status:                    status.Errorf(status.CodeFailedPrecondition, "no common protocol version"),
		}, nil
	}
	d.initialized = true
	d.negotiated = v
	if d.runtimeID == "" {
		d.runtimeID = "template-driver-" + req.LaunchID
	}
	return &driver.InitializeResponse{
		NegotiatedProtocolVersion: v,
		Status:                    status.New(),
		ReplaySupported:           false,
		RuntimeID:                 d.runtimeID,
	}, nil
}

// Describe returns a stable, deterministic descriptor for this plugin version.
func (d *Driver) Describe(ctx context.Context) (*driver.DriverDescriptor, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.shutdown {
		return nil, unavailable()
	}
	if !d.initialized {
		return nil, status.Errorf(status.CodeFailedPrecondition, "Initialize required before Describe")
	}
	return &driver.DriverDescriptor{
		DriverID:       driverID,
		Version:        pluginVersion,
		SchemaVersions: []string{driver.SchemaVersion},
		Capabilities: []driver.CapabilityDescriptor{
			{
				ID:    capabilityID,
				Title: capabilityTitle,
				Properties: []driver.PropertyDescriptor{
					{Name: "temperature", Type: "number", Unit: "Cel", Access: "read", Quality: []string{"good", "unavailable"}},
				},
				Events: []driver.EventDescriptor{
					{Name: "threshold", PayloadSchemaJSON: `{"type":"object","properties":{"threshold":{"type":"number"}}}`},
				},
				Actions: []driver.ActionDescriptor{
					{Name: "read", InputSchemaJSON: `{}`, ResultSchemaJSON: `{"type":"object","properties":{"temperature":{"type":"number"}}}`},
				},
			},
		},
	}, nil
}

// ConfigureInstance stores the per-instance configuration. The template
// accepts arbitrary JSON config and only record the applied revision.
func (d *Driver) ConfigureInstance(ctx context.Context, req *driver.ConfigureInstanceRequest) (*driver.ConfigureInstanceResponse, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.shutdown {
		return nil, unavailable()
	}
	if req.PluginInstanceID == "" {
		return &driver.ConfigureInstanceResponse{Status: status.Errorf(status.CodeInvalidArgument, "plugin_instance_id is required")}, nil
	}
	d.rev = req.ConfigRevision
	return &driver.ConfigureInstanceResponse{
		PluginInstanceID: req.PluginInstanceID,
		AppliedRevision:  req.ConfigRevision,
		Status:           status.New(),
	}, nil
}

// Discover reports one synthetic device for the discovered instance.
func (d *Driver) Discover(ctx context.Context, req *driver.DiscoverRequest, stream driver.DiscoveryWriter) error {
	d.mu.Lock()
	if d.shutdown {
		d.mu.Unlock()
		return unavailable()
	}
	seq := d.nextSeq(req.PluginInstanceID, "")
	d.mu.Unlock()

	if err := stream.Send(ctx, &driver.DiscoveryEvent{
		PluginInstanceID: req.PluginInstanceID,
		Sequence:         seq,
		SchemaVersion:    driver.SchemaVersion,
		DiscoveryID:      req.DiscoveryID,
		Union:            &driver.DiscoveryStarted{DiscoveryID: req.DiscoveryID},
	}); err != nil {
		return err
	}
	if err := stream.Send(ctx, &driver.DiscoveryEvent{
		PluginInstanceID: req.PluginInstanceID,
		Sequence:         d.nextSeq(req.PluginInstanceID, ""),
		SchemaVersion:    driver.SchemaVersion,
		DiscoveryID:      req.DiscoveryID,
		Union:            &driver.DiscoveryFoundDevice{DeviceID: deviceID, ExternalID: deviceID, Manufacturer: "CloudPath", Model: "Template"},
	}); err != nil {
		return err
	}
	return stream.Send(ctx, &driver.DiscoveryEvent{
		PluginInstanceID: req.PluginInstanceID,
		Sequence:         d.nextSeq(req.PluginInstanceID, ""),
		SchemaVersion:    driver.SchemaVersion,
		DiscoveryID:      req.DiscoveryID,
		Union:            &driver.DiscoveryFinished{FoundCount: 1},
	})
}

// OpenDevice marks the synthetic device as open.
func (d *Driver) OpenDevice(ctx context.Context, req *driver.OpenDeviceRequest) (*driver.OpenDeviceResponse, error) {
	d.mu.Lock()
	if d.shutdown {
		d.mu.Unlock()
		return nil, unavailable()
	}
	d.mu.Unlock()
	return &driver.OpenDeviceResponse{
		PluginInstanceID: req.PluginInstanceID,
		DeviceID:         req.DeviceID,
		Status:           status.New(),
	}, nil
}

// CloseDevice closes the synthetic device.
func (d *Driver) CloseDevice(ctx context.Context, req *driver.CloseDeviceRequest) (*driver.CloseDeviceResponse, error) {
	d.mu.Lock()
	if d.shutdown {
		d.mu.Unlock()
		return nil, unavailable()
	}
	d.mu.Unlock()
	return &driver.CloseDeviceResponse{
		PluginInstanceID: req.PluginInstanceID,
		DeviceID:         req.DeviceID,
		Status:           status.New(),
	}, nil
}

// Watch opens the observation stream. It publishes a DeviceUpsert, an
// EntityUpsert and an initial Observation, then a periodic simulated
// temperature reading until the context is done.
func (d *Driver) Watch(ctx context.Context, req *driver.WatchRequest, stream driver.DriverMessageWriter) error {
	d.mu.Lock()
	if d.shutdown {
		d.mu.Unlock()
		return unavailable()
	}
	if !d.initialized {
		d.mu.Unlock()
		return status.Errorf(status.CodeFailedPrecondition, "Initialize required before Watch")
	}
	devices := req.DeviceIDs
	if len(devices) == 0 {
		devices = []string{deviceID}
	}
	d.mu.Unlock()

	for _, dev := range devices {
		if err := stream.Send(ctx, d.deviceUpsert(req.PluginInstanceID, dev)); err != nil {
			return err
		}
		if err := stream.Send(ctx, d.entityUpsert(req.PluginInstanceID, dev)); err != nil {
			return err
		}
		if err := stream.Send(ctx, d.observation(req.PluginInstanceID, dev)); err != nil {
			return err
		}
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			for _, dev := range devices {
				if err := stream.Send(ctx, d.observation(req.PluginInstanceID, dev)); err != nil {
					return err
				}
			}
		}
	}
}

func (d *Driver) deviceUpsert(instance, dev string) *driver.DriverMessage {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.messageLocked(instance, dev, &driver.DeviceUpsert{
		Device: driver.Device{
			DeviceID:     dev,
			ExternalID:   dev,
			Manufacturer: "CloudPath",
			Model:        "Template",
			Status:       driver.DeviceStatusOnline,
			DisplayName:  "Template Sensor",
		},
	})
}

func (d *Driver) entityUpsert(instance, dev string) *driver.DriverMessage {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.messageLocked(instance, dev, &driver.EntityUpsert{
		Entity: driver.Entity{
			EntityID:     sensorEntityID,
			DeviceID:     dev,
			UniqueKey:    "template-sensor",
			Name:         "Template Temperature",
			Category:     driver.EntityCategorySensor,
			Capabilities: []string{capabilityID},
			Attributes:   map[string]string{"kind": "simulated"},
		},
	})
}

// observation simulates a temperature reading derived from wall clock time so
// it is stable within a test run but varies over time.
func (d *Driver) observation(instance, dev string) *driver.DriverMessage {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now()
	temp := 20.0 + float64(now.Unix()%10)
	return d.messageLocked(instance, dev, &driver.Observation{
		EntityID:   sensorEntityID,
		Capability: capabilityID,
		Property:   "temperature",
		Value:      driver.Value{Kind: driver.ValueNumber, NumberValue: temp},
		ObservedAt: now.UTC().Format(time.RFC3339),
		ReceivedAt: time.Now().UTC().Format(time.RFC3339),
		Quality:    "good",
	})
}

func (d *Driver) messageLocked(instance, dev string, body driver.DriverMessageUnion) *driver.DriverMessage {
	return &driver.DriverMessage{
		PluginInstanceID: instance,
		Sequence:         d.nextSeq(instance, dev),
		SchemaVersion:    driver.SchemaVersion,
		DeviceID:         dev,
		Union:            body,
	}
}

// Execute accepts the single whitelisted "read" action. The template returns
// a synchronous SUCCEEDED result; a real driver would acknowledge and then
// stream CommandProgress over Watch.
func (d *Driver) Execute(ctx context.Context, req *driver.ExecuteRequest) (*driver.ExecuteResponse, error) {
	d.mu.Lock()
	if d.shutdown {
		d.mu.Unlock()
		return nil, unavailable()
	}
	if req.IdempotencyKey == "" {
		d.mu.Unlock()
		return &driver.ExecuteResponse{Status: status.Errorf(status.CodeInvalidArgument, "idempotency_key is required")}, nil
	}
	if req.Action != "read" {
		d.mu.Unlock()
		return &driver.ExecuteResponse{Status: status.Errorf(status.CodeInvalidArgument, "unknown action %q", req.Action)}, nil
	}
	d.mu.Unlock()

	return &driver.ExecuteResponse{
		CommandID:        fmt.Sprintf("cmd-%s", req.IdempotencyKey),
		IdempotencyKey:   req.IdempotencyKey,
		Status:           status.New(),
		State:            driver.CommandStateSucceeded,
		AcceptedDeadline: time.Now().Add(10 * time.Second).UTC().Format(time.RFC3339),
	}, nil
}

// Health returns the serving state.
func (d *Driver) Health(ctx context.Context) (*driver.HealthResponse, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.shutdown {
		return &driver.HealthResponse{State: driver.HealthStateNotServing}, nil
	}
	return &driver.HealthResponse{State: driver.HealthStateServing}, nil
}

// Shutdown marks the plugin as stopped.
func (d *Driver) Shutdown(ctx context.Context, req *driver.ShutdownRequest) (*driver.ShutdownResponse, error) {
	d.mu.Lock()
	d.shutdown = true
	d.mu.Unlock()
	return &driver.ShutdownResponse{Status: status.New()}, nil
}

func (d *Driver) nextSeq(instance, dev string) uint64 {
	key := instance + "\x00" + dev
	next := d.seq[key] + 1
	d.seq[key] = next
	return next
}
