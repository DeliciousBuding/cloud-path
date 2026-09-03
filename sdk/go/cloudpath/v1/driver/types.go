// Package driver is the handwritten Go contract for
// proto/cloudpath/v1/driver.proto (Driver Protocol v1).
//
// Every type maps 1:1 to a proto message and is JSON-encoded exactly the way
// the proto text defines it (proto3 json_name, snake_case keys, bytes as
// base64). oneof fields are modeled as a typed Union plus custom
// MarshalJSON/UnmarshalJSON in codec.go. There is no protoc-generated code,
// and no third-party dependency is used.
//
// The package also provides DriverClient/DriverServer interfaces and
// in-process adapters that run the protocol over sdk/go/transport:
//
//	cli := driver.NewClient(transportEnd)
//	srv := driver.NewRPCServer(transportEnd, myDriverImpl)
//	go srv.Serve(ctx)
//
// See testing/plugin-harness for a mock Core, mock Driver and the
// conformance suite.
package driver

import (
	"context"

	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/status"
	"github.com/DeliciousBuding/cloud-path/sdk/go/rpc"
	"github.com/DeliciousBuding/cloud-path/sdk/go/transport"
)

// ProtocolVersion is the single driver protocol version implemented here.
const ProtocolVersion uint32 = 1

// SchemaVersion is the data semantic version stamped on every stream message.
const SchemaVersion = "1"

// Service method names carried on the wire.
const (
	MethodInitialize        = "cloudpath.v1.driver.DriverService/Initialize"
	MethodDescribe          = "cloudpath.v1.driver.DriverService/Describe"
	MethodConfigureInstance = "cloudpath.v1.driver.DriverService/ConfigureInstance"
	MethodDiscover          = "cloudpath.v1.driver.DriverService/Discover"
	MethodOpenDevice        = "cloudpath.v1.driver.DriverService/OpenDevice"
	MethodCloseDevice       = "cloudpath.v1.driver.DriverService/CloseDevice"
	MethodWatch             = "cloudpath.v1.driver.DriverService/Watch"
	MethodExecute           = "cloudpath.v1.driver.DriverService/Execute"
	MethodHealth            = "cloudpath.v1.driver.DriverService/Health"
	MethodShutdown          = "cloudpath.v1.driver.DriverService/Shutdown"
)

// Status is the protocol status type (mirrors the inline Status message in
// driver.proto; the application protocol shares the same code table).
type Status = status.Status

// ---------------------------------------------------------------------------
// Handshake
// ---------------------------------------------------------------------------

type InitializeRequest struct {
	PluginID                  string            `json:"plugin_id"`
	PluginVersion             string            `json:"plugin_version"`
	LaunchID                  string            `json:"launch_id"`
	HandshakeCookie           string            `json:"handshake_cookie"`
	ProtocolVersion           uint32            `json:"protocol_version"`
	SupportedProtocolVersions []uint32          `json:"supported_protocol_versions"`
	NodeID                    string            `json:"node_id"`
	RuntimeType               string            `json:"runtime_type"`
	HostInfo                  map[string]string `json:"host_info"`
}

type InitializeResponse struct {
	NegotiatedProtocolVersion uint32  `json:"negotiated_protocol_version"`
	Status                    *Status `json:"status"`
	ReplaySupported           bool    `json:"replay_supported"`
	RuntimeID                 string  `json:"runtime_id"`
}

// ---------------------------------------------------------------------------
// Describe
// ---------------------------------------------------------------------------

// DescribeRequest is empty by contract; the descriptor must be stable and
// deterministic for a given plugin version.
type DescribeRequest struct{}

type DriverDescriptor struct {
	DriverID       string                 `json:"driver_id"`
	Version        string                 `json:"version"`
	SchemaVersions []string               `json:"schema_versions"`
	Capabilities   []CapabilityDescriptor `json:"capabilities"`
}

type CapabilityDescriptor struct {
	ID         string               `json:"id"`
	Title      string               `json:"title"`
	Properties []PropertyDescriptor `json:"properties"`
	Events     []EventDescriptor    `json:"events"`
	Actions    []ActionDescriptor   `json:"actions"`
}

type PropertyDescriptor struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Unit    string   `json:"unit"`
	Access  string   `json:"access"`
	Quality []string `json:"quality"`
}

type EventDescriptor struct {
	Name              string `json:"name"`
	PayloadSchemaJSON string `json:"payload_schema_json"`
}

type ActionDescriptor struct {
	Name             string `json:"name"`
	InputSchemaJSON  string `json:"input_schema_json"`
	ResultSchemaJSON string `json:"result_schema_json"`
}

// ---------------------------------------------------------------------------
// Instance configuration
// ---------------------------------------------------------------------------

type ConfigureInstanceRequest struct {
	PluginInstanceID string `json:"plugin_instance_id"`
	Config           []byte `json:"config"`
	ConfigRevision   uint32 `json:"config_revision"`
}

type ConfigureInstanceResponse struct {
	PluginInstanceID string  `json:"plugin_instance_id"`
	AppliedRevision  uint32  `json:"applied_revision"`
	Status           *Status `json:"status"`
}

// ---------------------------------------------------------------------------
// Discovery
// ---------------------------------------------------------------------------

type DiscoverRequest struct {
	PluginInstanceID string            `json:"plugin_instance_id"`
	DiscoveryID      string            `json:"discovery_id"`
	Parameters       map[string]string `json:"parameters"`
}

// DiscoveryEvent carries exactly one body variant.
type DiscoveryEvent struct {
	PluginInstanceID string              `json:"plugin_instance_id"`
	Sequence         uint64              `json:"sequence"`
	SchemaVersion    string              `json:"schema_version"`
	DiscoveryID      string              `json:"discovery_id"`
	Union            DiscoveryEventUnion `json:"-"`
}

type DiscoveryEventUnion interface {
	discoveryEventVariant()
}

type DiscoveryStarted struct {
	DiscoveryID string `json:"discovery_id"`
}

func (*DiscoveryStarted) discoveryEventVariant() {}

type DiscoveryFoundDevice struct {
	DeviceID     string `json:"device_id"`
	ExternalID   string `json:"external_id"`
	Manufacturer string `json:"manufacturer"`
	Model        string `json:"model"`
}

func (*DiscoveryFoundDevice) discoveryEventVariant() {}

type DiscoveryProgress struct {
	Fraction float64 `json:"fraction"`
	Detail   string  `json:"detail"`
}

func (*DiscoveryProgress) discoveryEventVariant() {}

type DiscoveryFinished struct {
	FoundCount uint32 `json:"found_count"`
}

func (*DiscoveryFinished) discoveryEventVariant() {}

type DiscoveryFailed struct {
	Status *Status `json:"status"`
}

func (*DiscoveryFailed) discoveryEventVariant() {}

// ---------------------------------------------------------------------------
// Open / Close device
// ---------------------------------------------------------------------------

type OpenDeviceRequest struct {
	PluginInstanceID string            `json:"plugin_instance_id"`
	DeviceID         string            `json:"device_id"`
	ConnectionHints  map[string]string `json:"connection_hints"`
}

type OpenDeviceResponse struct {
	PluginInstanceID string  `json:"plugin_instance_id"`
	DeviceID         string  `json:"device_id"`
	Status           *Status `json:"status"`
}

type CloseDeviceRequest struct {
	PluginInstanceID string `json:"plugin_instance_id"`
	DeviceID         string `json:"device_id"`
}

type CloseDeviceResponse struct {
	PluginInstanceID string  `json:"plugin_instance_id"`
	DeviceID         string  `json:"device_id"`
	Status           *Status `json:"status"`
}

// ---------------------------------------------------------------------------
// Watch stream
// ---------------------------------------------------------------------------

type WatchRequest struct {
	PluginInstanceID   string   `json:"plugin_instance_id"`
	ResumeFromSequence uint64   `json:"resume_from_sequence"`
	DeviceIDs          []string `json:"device_ids"`
	MaxBuffered        uint32   `json:"max_buffered"`
}

// DriverMessage is the single report envelope of the Watch stream. Every
// message carries plugin_instance_id, a monotonically increasing sequence
// scoped to (instance, device), and a schema version. Core dedupes on that
// scope; see SequenceTracker.
type DriverMessage struct {
	PluginInstanceID string             `json:"plugin_instance_id"`
	Sequence         uint64             `json:"sequence"`
	SchemaVersion    string             `json:"schema_version"`
	DeviceID         string             `json:"device_id"`
	Union            DriverMessageUnion `json:"-"`
}

type DriverMessageUnion interface {
	driverMessageVariant()
}

func (*DeviceUpsert) driverMessageVariant()    {}
func (*EntityUpsert) driverMessageVariant()    {}
func (*Observation) driverMessageVariant()     {}
func (*Event) driverMessageVariant()           {}
func (*CommandProgress) driverMessageVariant() {}
func (*Diagnostic) driverMessageVariant()      {}

type DeviceStatus int32

const (
	DeviceStatusUnspecified DeviceStatus = 0
	DeviceStatusOnline      DeviceStatus = 1
	DeviceStatusOffline     DeviceStatus = 2
	DeviceStatusUnavailable DeviceStatus = 3
	DeviceStatusDegraded    DeviceStatus = 4
)

type Device struct {
	DeviceID     string       `json:"device_id"`
	ExternalID   string       `json:"external_id"`
	Manufacturer string       `json:"manufacturer"`
	Model        string       `json:"model"`
	Status       DeviceStatus `json:"status"`
	DisplayName  string       `json:"display_name"`
}

type DeviceUpsert struct {
	Device  Device `json:"device"`
	Removed bool   `json:"removed"`
}

type EntityCategory int32

const (
	EntityCategoryUnspecified EntityCategory = 0
	EntityCategorySensor      EntityCategory = 1
	EntityCategoryActuator    EntityCategory = 2
	EntityCategoryDiagnostic  EntityCategory = 3
	EntityCategoryConfig      EntityCategory = 4
)

type Entity struct {
	EntityID     string            `json:"entity_id"`
	DeviceID     string            `json:"device_id"`
	UniqueKey    string            `json:"unique_key"`
	Name         string            `json:"name"`
	Category     EntityCategory    `json:"category"`
	Capabilities []string          `json:"capabilities"`
	Attributes   map[string]string `json:"attributes"`
}

type EntityUpsert struct {
	Entity  Entity `json:"entity"`
	Removed bool   `json:"removed"`
}

type Observation struct {
	EntityID   string `json:"entity_id"`
	Capability string `json:"capability"`
	Property   string `json:"property"`
	Value      Value  `json:"value"`
	ObservedAt string `json:"observed_at"`
	ReceivedAt string `json:"received_at"`
	Quality    string `json:"quality"`
}

// ValueKind enumerates the Observation.Value oneof alternatives.
type ValueKind int32

const (
	ValueUnspecified ValueKind = 0
	ValueNumber      ValueKind = 1
	ValueInt         ValueKind = 2
	ValueString      ValueKind = 3
	ValueBool        ValueKind = 4
	ValueJSON        ValueKind = 5
)

// Value is the typed oneof inside Observation.
type Value struct {
	Kind        ValueKind `json:"-"`
	NumberValue float64   `json:"-"`
	IntValue    int64     `json:"-"`
	StringValue string    `json:"-"`
	BoolValue   bool      `json:"-"`
	JSONValue   string    `json:"-"`
}

type Event struct {
	EntityID      string `json:"entity_id"`
	DeviceID      string `json:"device_id"`
	EventType     string `json:"event_type"`
	PayloadJSON   string `json:"payload_json"`
	OccurredAt    string `json:"occurred_at"`
	CorrelationID string `json:"correlation_id"`
}

type CommandProgress struct {
	CommandID      string       `json:"command_id"`
	IdempotencyKey string       `json:"idempotency_key"`
	EntityID       string       `json:"entity_id"`
	Action         string       `json:"action"`
	State          CommandState `json:"state"`
	Progress       float64      `json:"progress"`
	Detail         string       `json:"detail"`
	ResultJSON     string       `json:"result_json"`
	ErrorCode      string       `json:"error_code"`
}

type Diagnostic struct {
	Level      string            `json:"level"`
	Message    string            `json:"message"`
	Fields     map[string]string `json:"fields"`
	ObservedAt string            `json:"observed_at"`
	RawJSON    string            `json:"raw_json"`
}

// ---------------------------------------------------------------------------
// Execute
// ---------------------------------------------------------------------------

type CommandState int32

const (
	CommandStateUnspecified CommandState = 0
	CommandStateCreated     CommandState = 1
	CommandStateDispatched  CommandState = 2
	CommandStateAccepted    CommandState = 3
	CommandStateRunning     CommandState = 4
	CommandStateSucceeded   CommandState = 5
	CommandStateFailed      CommandState = 6
	CommandStateTimedOut    CommandState = 7
	CommandStateCancelled   CommandState = 8
)

// ExecuteRequest either runs a command or, when CancelCommandID is non-empty,
// cancels the referenced in-flight command. IdempotencyKey is required in
// both cases so Core can safely retry.
type ExecuteRequest struct {
	PluginInstanceID string `json:"plugin_instance_id"`
	IdempotencyKey   string `json:"idempotency_key"`
	EntityID         string `json:"entity_id"`
	Action           string `json:"action"`
	ArgsJSON         string `json:"args_json"`
	Deadline         string `json:"deadline"`
	Actor            string `json:"actor"`
	CancelCommandID  string `json:"cancel_command_id"`
}

type ExecuteResponse struct {
	CommandID        string       `json:"command_id"`
	IdempotencyKey   string       `json:"idempotency_key"`
	Status           *Status      `json:"status"`
	State            CommandState `json:"state"`
	Replay           bool         `json:"replay"`
	AcceptedDeadline string       `json:"accepted_deadline"`
}

// ---------------------------------------------------------------------------
// Health / Shutdown
// ---------------------------------------------------------------------------

type HealthRequest struct{}

type HealthState int32

const (
	HealthStateUnspecified HealthState = 0
	HealthStateServing     HealthState = 1
	HealthStateNotServing  HealthState = 2
)

type InstanceHealth struct {
	PluginInstanceID string      `json:"plugin_instance_id"`
	State            HealthState `json:"state"`
	Detail           string      `json:"detail"`
}

type HealthResponse struct {
	State     HealthState      `json:"state"`
	Instances []InstanceHealth `json:"instances"`
}

type ShutdownRequest struct {
	Reason       string `json:"reason"`
	GraceSeconds uint32 `json:"grace_seconds"`
}

type ShutdownResponse struct {
	Status *Status `json:"status"`
}

// ---------------------------------------------------------------------------
// Client / Server interfaces
// ---------------------------------------------------------------------------

// DriverClient is the Core-side view of DriverService.
type DriverClient interface {
	Initialize(ctx context.Context, req *InitializeRequest) (*InitializeResponse, error)
	Describe(ctx context.Context) (*DriverDescriptor, error)
	ConfigureInstance(ctx context.Context, req *ConfigureInstanceRequest) (*ConfigureInstanceResponse, error)
	Discover(ctx context.Context, req *DiscoverRequest) (DiscoveryStream, error)
	OpenDevice(ctx context.Context, req *OpenDeviceRequest) (*OpenDeviceResponse, error)
	CloseDevice(ctx context.Context, req *CloseDeviceRequest) (*CloseDeviceResponse, error)
	Watch(ctx context.Context, req *WatchRequest) (DriverMessageStream, error)
	Execute(ctx context.Context, req *ExecuteRequest) (*ExecuteResponse, error)
	Health(ctx context.Context) (*HealthResponse, error)
	Shutdown(ctx context.Context, req *ShutdownRequest) (*ShutdownResponse, error)
}

// DiscoveryStream is the client view of the Discover server stream.
type DiscoveryStream interface {
	Recv(ctx context.Context) (*DiscoveryEvent, error)
	Cancel(ctx context.Context) error
}

// DriverMessageStream is the client view of the Watch server stream.
type DriverMessageStream interface {
	Recv(ctx context.Context) (*DriverMessage, error)
	Cancel(ctx context.Context) error
}

// DiscoveryWriter lets a plugin publish discovery events.
type DiscoveryWriter interface {
	Send(ctx context.Context, msg *DiscoveryEvent) error
}

// DriverMessageWriter lets a plugin publish Watch messages.
type DriverMessageWriter interface {
	Send(ctx context.Context, msg *DriverMessage) error
}

// DriverServer is the plugin-side implementation of DriverService.
type DriverServer interface {
	Initialize(ctx context.Context, req *InitializeRequest) (*InitializeResponse, error)
	Describe(ctx context.Context) (*DriverDescriptor, error)
	ConfigureInstance(ctx context.Context, req *ConfigureInstanceRequest) (*ConfigureInstanceResponse, error)
	Discover(ctx context.Context, req *DiscoverRequest, stream DiscoveryWriter) error
	OpenDevice(ctx context.Context, req *OpenDeviceRequest) (*OpenDeviceResponse, error)
	CloseDevice(ctx context.Context, req *CloseDeviceRequest) (*CloseDeviceResponse, error)
	Watch(ctx context.Context, req *WatchRequest, stream DriverMessageWriter) error
	Execute(ctx context.Context, req *ExecuteRequest) (*ExecuteResponse, error)
	Health(ctx context.Context) (*HealthResponse, error)
	Shutdown(ctx context.Context, req *ShutdownRequest) (*ShutdownResponse, error)
}

// NewClient wraps a transport as a DriverClient.
func NewClient(tr transport.Transport) DriverClient {
	return &driverClient{rc: rpc.NewClient(tr)}
}

// NewRPCServer binds impl to a transport and returns the dispatcher. Call
// Serve to consume frames.
func NewRPCServer(tr transport.Transport, impl DriverServer) *rpc.Server {
	return register(impl, rpc.NewServer(tr))
}
