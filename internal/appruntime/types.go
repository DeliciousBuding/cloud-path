// Package appruntime implements the device-independent Application Runtime.
//
// The runtime owns Application Instances (tenant/id/plugin/binding/state) and
// drives the Application Protocol v1 service through the SDK client:
// Initialize, Describe, ConfigureInstance, ValidateBinding, HandleEvents,
// HandleRequest, RunJob, Health and Shutdown. It never references a Driver ID,
// a serial port, or any concrete hardware field.
//
// The design contract lives in docs/architecture/plugin-system.md (Application
// Protocol v1, permissions model, supervisor) and
// docs/architecture/capability-model.md (Application Binding and Invariants).
package appruntime

import (
	"context"
	"errors"
	"log/slog"
	"time"

	coreapplication "github.com/DeliciousBuding/cloud-path/internal/application"
	sdkapplication "github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/application"
)

// InstanceState is the lifecycle state of one application instance.
type InstanceState string

const (
	// StateCreated means the instance record exists but startup has not begun.
	StateCreated InstanceState = "created"
	// StateStarting means the runtime is performing the startup sequence.
	StateStarting InstanceState = "starting"
	// StateRunning means the event stream is active and the instance serves
	// requests and jobs.
	StateRunning InstanceState = "running"
	// StateStopping means the runtime is performing the shutdown sequence.
	StateStopping InstanceState = "stopping"
	// StateStopped means the instance is fully shut down.
	StateStopped InstanceState = "stopped"
	// StateFailed means startup or the event stream failed with an error.
	StateFailed InstanceState = "failed"
)

// Instance is a point-in-time snapshot of an application instance.
type Instance struct {
	ApplicationID    string
	PluginInstanceID string
	PluginID         string
	TenantID         string
	State            InstanceState
	Err              error

	Descriptor   *sdkapplication.ApplicationDescriptor
	Requirements []coreapplication.Requirement
	Bindings     []coreapplication.Binding
}

// InstanceSpec is the input for starting one application instance.
type InstanceSpec struct {
	ApplicationID    string
	PluginInstanceID string
	PluginID         string
	TenantID         string

	// PluginVersion is reported to the plugin during Initialize. When empty,
	// the runtime omits it rather than inventing a version.
	PluginVersion string
	// LaunchID is the host-generated launch identity used for the handshake.
	LaunchID string
	// NodeID identifies the node running the instance.
	NodeID string
	// HostInfo carries host metadata for the Initialize handshake.
	HostInfo map[string]string

	// Config and ConfigRevision are passed through to ConfigureInstance.
	Config         []byte
	ConfigRevision uint32

	// Requirements is an optional client-side declaration. When the plugin
	// Describe response carries requirements, those are authoritative and this
	// field is ignored.
	Requirements []coreapplication.Requirement
	// Candidates is the bindable view of entities for binding validation.
	Candidates []coreapplication.Candidate
	// Bindings is the proposed binding set for this instance.
	Bindings []coreapplication.Binding

	// EventQueueSize is the per-instance outbound event queue capacity. Zero
	// selects the runtime default.
	EventQueueSize int
}

// RuntimeOptions configures a Runtime. Dialer and Executor are required.
type RuntimeOptions struct {
	// Dialer returns the SDK ApplicationClient for a plugin id. A process
	// host supplies one client per installed plugin process.
	Dialer Dialer
	// Executor is the Core-provided EffectExecutor.
	Executor EffectExecutor
	// Logger receives structured diagnostics. A discard logger is used when nil.
	Logger *slog.Logger

	// EventQueueSize is the default per-instance event queue capacity.
	EventQueueSize int
	// ShutdownTimeout bounds one graceful instance shutdown.
	ShutdownTimeout time.Duration
	// Context is the parent context for all instance event loops.
	Context context.Context
}

// Dialer returns the SDK ApplicationClient for a plugin id.
type Dialer func(pluginID string) (sdkapplication.ApplicationClient, error)

// Sentinel errors returned by the runtime.
var (
	// ErrInstanceNotFound means no instance exists with the supplied id.
	ErrInstanceNotFound = errors.New("appruntime: instance not found")
	// ErrInstanceExists means an instance with the supplied id is already
	// registered.
	ErrInstanceExists = errors.New("appruntime: instance already exists")
	// ErrInstanceNotRunning means the instance exists but cannot accept the
	// operation in its current state.
	ErrInstanceNotRunning = errors.New("appruntime: instance not running")
	// ErrTenantMismatch means a caller-supplied request/effect tried to act as
	// a tenant different from the instance tenant.
	ErrTenantMismatch = errors.New("appruntime: tenant mismatch")
	// ErrRuntimeClosed means the runtime has been shut down.
	ErrRuntimeClosed = errors.New("appruntime: runtime closed")
)
