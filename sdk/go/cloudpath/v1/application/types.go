// Package application is the handwritten Go contract for
// proto/cloudpath/v1/application.proto (Application Protocol v1).
//
// ApplicationEffect is deliberately a closed set: upsert/delete domain
// records, request commands, schedule/cancel tasks and send notifications.
// It cannot carry arbitrary SQL, shell commands, file operations or global
// credential requests. Plugin HTTP requests get tenant/actor/instance/scope
// context injected by Core and are scoped to
// /api/plugins/{plugin_id}/instances/{instance_id}/....
package application

import (
	"context"

	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/status"
	"github.com/DeliciousBuding/cloud-path/sdk/go/rpc"
	"github.com/DeliciousBuding/cloud-path/sdk/go/transport"
)

// ProtocolVersion is the single application protocol version implemented here.
const ProtocolVersion uint32 = 1

// SchemaVersion is the data semantic version stamped on event/effect messages.
const SchemaVersion = "1"

// Service method names carried on the wire.
const (
	MethodInitialize        = "cloudpath.v1.application.ApplicationService/Initialize"
	MethodDescribe          = "cloudpath.v1.application.ApplicationService/Describe"
	MethodConfigureInstance = "cloudpath.v1.application.ApplicationService/ConfigureInstance"
	MethodValidateBinding   = "cloudpath.v1.application.ApplicationService/ValidateBinding"
	MethodHandleEvents      = "cloudpath.v1.application.ApplicationService/HandleEvents"
	MethodHandleRequest     = "cloudpath.v1.application.ApplicationService/HandleRequest"
	MethodRunJob            = "cloudpath.v1.application.ApplicationService/RunJob"
	MethodHealth            = "cloudpath.v1.application.ApplicationService/Health"
	MethodShutdown          = "cloudpath.v1.application.ApplicationService/Shutdown"
)

// Status mirrors the inline Status message in application.proto and shares
// the driver protocol's code table.
type Status = status.Status

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
	RuntimeID                 string  `json:"runtime_id"`
}

type DescribeRequest struct{}

type ApplicationDescriptor struct {
	ApplicationID   string                  `json:"application_id"`
	Version         string                  `json:"version"`
	SchemaVersions  []string                `json:"schema_versions"`
	Requirements    []RequirementDescriptor `json:"requirements"`
	Jobs            []JobDescriptor         `json:"jobs"`
	DeclarativeOnly bool                    `json:"declarative_only"`
}

type RequirementDescriptor struct {
	ID          string `json:"id"`
	Capability  string `json:"capability"`
	Cardinality string `json:"cardinality"`
	MinItems    uint32 `json:"min_items"`
}

type JobDescriptor struct {
	ID              string `json:"id"`
	Title           string `json:"title"`
	InputSchemaJSON string `json:"input_schema_json"`
}

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

type Binding struct {
	RequirementID string `json:"requirement_id"`
	EntityID      string `json:"entity_id"`
}

type ValidateBindingRequest struct {
	PluginInstanceID string    `json:"plugin_instance_id"`
	Bindings         []Binding `json:"bindings"`
}

type ValidateBindingResponse struct {
	Valid  bool           `json:"valid"`
	Issues []BindingIssue `json:"issues"`
}

type BindingIssue struct {
	RequirementID string `json:"requirement_id"`
	Severity      string `json:"severity"`
	Message       string `json:"message"`
}

// ---------------------------------------------------------------------------
// Events (Core -> Application)
// ---------------------------------------------------------------------------

type ApplicationEvent struct {
	PluginInstanceID string                `json:"plugin_instance_id"`
	Sequence         uint64                `json:"sequence"`
	SchemaVersion    string                `json:"schema_version"`
	Union            ApplicationEventUnion `json:"-"`
}

type ApplicationEventUnion interface {
	applicationEventVariant()
}

type CapabilityEvent struct {
	RequirementID string `json:"requirement_id"`
	EntityID      string `json:"entity_id"`
	EventType     string `json:"event_type"`
	PayloadJSON   string `json:"payload_json"`
	OccurredAt    string `json:"occurred_at"`
}

func (*CapabilityEvent) applicationEventVariant() {}

type ScheduleTick struct {
	ScheduleID string `json:"schedule_id"`
	OccurredAt string `json:"occurred_at"`
	WindowJSON string `json:"window_json"`
}

func (*ScheduleTick) applicationEventVariant() {}

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

type RequestCompleted struct {
	RequestID  string       `json:"request_id"`
	EntityID   string       `json:"entity_id"`
	Action     string       `json:"action"`
	State      CommandState `json:"state"`
	ResultJSON string       `json:"result_json"`
	ErrorCode  string       `json:"error_code"`
}

func (*RequestCompleted) applicationEventVariant() {}

type InstanceLifecycle struct {
	State  string `json:"state"`
	Detail string `json:"detail"`
}

func (*InstanceLifecycle) applicationEventVariant() {}

// ---------------------------------------------------------------------------
// Effects (Application -> Core) — Core-approved operation set only
// ---------------------------------------------------------------------------

type ApplicationEffect struct {
	PluginInstanceID string                 `json:"plugin_instance_id"`
	Sequence         uint64                 `json:"sequence"`
	SchemaVersion    string                 `json:"schema_version"`
	Union            ApplicationEffectUnion `json:"-"`
}

type ApplicationEffectUnion interface {
	applicationEffectVariant()
}

type UpsertDomainRecord struct {
	RecordType string `json:"record_type"`
	RecordID   string `json:"record_id"`
	DataJSON   string `json:"data_json"`
	Version    string `json:"version"`
}

func (*UpsertDomainRecord) applicationEffectVariant() {}

type DeleteDomainRecord struct {
	RecordType string `json:"record_type"`
	RecordID   string `json:"record_id"`
	Version    string `json:"version"`
}

func (*DeleteDomainRecord) applicationEffectVariant() {}

type RequestCommand struct {
	EntityID       string `json:"entity_id"`
	Action         string `json:"action"`
	ArgsJSON       string `json:"args_json"`
	IdempotencyKey string `json:"idempotency_key"`
	Deadline       string `json:"deadline"`
}

func (*RequestCommand) applicationEffectVariant() {}

type ScheduleTask struct {
	ScheduleID  string `json:"schedule_id"`
	Cron        string `json:"cron"`
	PayloadJSON string `json:"payload_json"`
}

func (*ScheduleTask) applicationEffectVariant() {}

type CancelScheduledTask struct {
	ScheduleID string `json:"schedule_id"`
}

func (*CancelScheduledTask) applicationEffectVariant() {}

type SendNotification struct {
	Title    string `json:"title"`
	Body     string `json:"body"`
	Severity string `json:"severity"`
}

func (*SendNotification) applicationEffectVariant() {}

// ---------------------------------------------------------------------------
// Plugin HTTP subroute
// ---------------------------------------------------------------------------

type PluginHTTPRequest struct {
	RequestID        string            `json:"request_id"`
	PluginInstanceID string            `json:"plugin_instance_id"`
	Method           string            `json:"method"`
	Path             string            `json:"path"`
	Headers          map[string]string `json:"headers"`
	Body             []byte            `json:"body"`
	Context          RequestContext    `json:"context"`
}

type RequestContext struct {
	TenantID   string   `json:"tenant_id"`
	ActorID    string   `json:"actor_id"`
	InstanceID string   `json:"instance_id"`
	Scopes     []string `json:"scopes"`
}

type PluginHTTPResponse struct {
	StatusCode uint32            `json:"status_code"`
	Headers    map[string]string `json:"headers"`
	Body       []byte            `json:"body"`
}

// ---------------------------------------------------------------------------
// RunJob
// ---------------------------------------------------------------------------

type RunJobRequest struct {
	PluginInstanceID string `json:"plugin_instance_id"`
	JobID            string `json:"job_id"`
	JobType          string `json:"job_type"`
	ArgsJSON         string `json:"args_json"`
	IdempotencyKey   string `json:"idempotency_key"`
	Deadline         string `json:"deadline"`
}

type RunJobResponse struct {
	JobID      string  `json:"job_id"`
	Status     *Status `json:"status"`
	ResultJSON string  `json:"result_json"`
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

// ApplicationClient is the Core-side view of ApplicationService.
type ApplicationClient interface {
	Initialize(ctx context.Context, req *InitializeRequest) (*InitializeResponse, error)
	Describe(ctx context.Context) (*ApplicationDescriptor, error)
	ConfigureInstance(ctx context.Context, req *ConfigureInstanceRequest) (*ConfigureInstanceResponse, error)
	ValidateBinding(ctx context.Context, req *ValidateBindingRequest) (*ValidateBindingResponse, error)
	HandleEvents(ctx context.Context) (ApplicationEventStream, error)
	HandleRequest(ctx context.Context, req *PluginHTTPRequest) (*PluginHTTPResponse, error)
	RunJob(ctx context.Context, req *RunJobRequest) (*RunJobResponse, error)
	Health(ctx context.Context) (*HealthResponse, error)
	Shutdown(ctx context.Context, req *ShutdownRequest) (*ShutdownResponse, error)
}

// ApplicationEventStream is the client view of the HandleEvents bidi stream:
// Core sends events and receives effects.
type ApplicationEventStream interface {
	Send(ctx context.Context, event *ApplicationEvent) error
	CloseSend(ctx context.Context) error
	Recv(ctx context.Context) (*ApplicationEffect, error)
	Cancel(ctx context.Context) error
}

// ApplicationEventReader feeds events to a plugin handler.
type ApplicationEventReader interface {
	Recv(ctx context.Context) (*ApplicationEvent, error)
}

// ApplicationEffectWriter lets a plugin emit effects.
type ApplicationEffectWriter interface {
	Send(ctx context.Context, effect *ApplicationEffect) error
}

// ApplicationServer is the plugin-side implementation of ApplicationService.
type ApplicationServer interface {
	Initialize(ctx context.Context, req *InitializeRequest) (*InitializeResponse, error)
	Describe(ctx context.Context) (*ApplicationDescriptor, error)
	ConfigureInstance(ctx context.Context, req *ConfigureInstanceRequest) (*ConfigureInstanceResponse, error)
	ValidateBinding(ctx context.Context, req *ValidateBindingRequest) (*ValidateBindingResponse, error)
	HandleEvents(ctx context.Context, events ApplicationEventReader, effects ApplicationEffectWriter) error
	HandleRequest(ctx context.Context, req *PluginHTTPRequest) (*PluginHTTPResponse, error)
	RunJob(ctx context.Context, req *RunJobRequest) (*RunJobResponse, error)
	Health(ctx context.Context) (*HealthResponse, error)
	Shutdown(ctx context.Context, req *ShutdownRequest) (*ShutdownResponse, error)
}

// NewClient wraps a transport as an ApplicationClient.
func NewClient(tr transport.Transport) ApplicationClient {
	return &applicationClient{rc: rpc.NewClient(tr)}
}

// NewRPCServer binds impl to a transport and returns the dispatcher.
func NewRPCServer(tr transport.Transport, impl ApplicationServer) *rpc.Server {
	return register(impl, rpc.NewServer(tr))
}
