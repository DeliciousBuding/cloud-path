// Package plugin implements the CloudPath Application Protocol v1 for the
// template application plugin. It binds to a Capability by semantic id (not by
// a Driver id), receives capability events, and emits only Core-approved
// ApplicationEffect operations. It never opens a Core database and holds no
// global token; all requests are scoped by Core to this plugin instance.
//
// Use it as the starting point for a real application: keep the
// RequirementDescriptor stable, and let HandleEvents translate domain events
// into the closed set of ApplicationEffect operations.
package plugin

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/application"
	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/status"
)

// Stable identities for this plugin. rename.py rewrites these literals when a
// developer copies the template into a real plugin repository.
const (
	PluginID      = "io.github.deliciousbuding.cloud-path-app-template"
	pluginVersion = "0.1.0"

	// appID is the stable Application identifier reported by Describe.
	appID = "template"

	// capabilityID is the Capability this application binds to. Binding is by
	// capability semantics, never by Driver id, per the architecture rule
	// "Application -X-> Driver ID".
	capabilityID  = "cloudpath.dev/capability/drivertemplate@1"
	requirementID = "req/temperature"
)

var (
	_ application.ApplicationServer = (*App)(nil)
)

// App is a minimal in-memory ApplicationServer.
type App struct {
	initialized bool
	shutdown    bool
	rev         uint32
}

// New returns a fresh App.
func New() *App {
	return &App{}
}

func unavailable() *status.Status {
	return status.Errorf(status.CodeUnavailable, "template application shut down")
}

// Initialize performs the launch handshake.
func (a *App) Initialize(ctx context.Context, req *application.InitializeRequest) (*application.InitializeResponse, error) {
	if a.shutdown {
		return nil, unavailable()
	}
	a.initialized = true
	return &application.InitializeResponse{
		NegotiatedProtocolVersion: application.ProtocolVersion,
		Status:                    status.New(),
		RuntimeID:                 "template-app-" + req.LaunchID,
	}, nil
}

// Describe returns the stable descriptor with the capability requirement.
func (a *App) Describe(ctx context.Context) (*application.ApplicationDescriptor, error) {
	if a.shutdown {
		return nil, unavailable()
	}
	if !a.initialized {
		return nil, status.Errorf(status.CodeFailedPrecondition, "Initialize required before Describe")
	}
	return &application.ApplicationDescriptor{
		ApplicationID:  appID,
		Version:        pluginVersion,
		SchemaVersions: []string{application.SchemaVersion},
		Requirements: []application.RequirementDescriptor{
			{ID: requirementID, Capability: capabilityID, Cardinality: "one", MinItems: 1},
		},
		DeclarativeOnly: false,
	}, nil
}

// ConfigureInstance records the per-instance configuration revision.
func (a *App) ConfigureInstance(ctx context.Context, req *application.ConfigureInstanceRequest) (*application.ConfigureInstanceResponse, error) {
	if a.shutdown {
		return nil, unavailable()
	}
	// The config payload is raw JSON; the template accepts it and only records
	// the revision. A real plugin would unmarshal and validate it here.
	a.rev = req.ConfigRevision
	return &application.ConfigureInstanceResponse{
		PluginInstanceID: req.PluginInstanceID,
		AppliedRevision:  req.ConfigRevision,
		Status:           status.New(),
	}, nil
}

// ValidateBinding checks that every bound entity satisfies the declared
// requirement. It binds by Capability, so entity metadata is not consulted for
// a Driver id.
func (a *App) ValidateBinding(ctx context.Context, req *application.ValidateBindingRequest) (*application.ValidateBindingResponse, error) {
	if a.shutdown {
		return nil, unavailable()
	}
	var issues []application.BindingIssue
	seen := make(map[string]bool)
	for _, b := range req.Bindings {
		if b.RequirementID == requirementID && b.EntityID != "" {
			seen[b.EntityID] = true
			continue
		}
		issues = append(issues, application.BindingIssue{
			RequirementID: b.RequirementID,
			Severity:      "error",
			Message:       "binding does not satisfy requirement " + requirementID,
		})
	}
	if len(seen) == 0 {
		issues = append(issues, application.BindingIssue{
			RequirementID: requirementID,
			Severity:      "error",
			Message:       "at least one bound entity is required",
		})
	}
	return &application.ValidateBindingResponse{Valid: len(issues) == 0, Issues: issues}, nil
}

// HandleEvents is the bidi event/effect stream. It translates each inbound
// event into a safe, Core-approved ApplicationEffect.
func (a *App) HandleEvents(ctx context.Context, events application.ApplicationEventReader, effects application.ApplicationEffectWriter) error {
	for {
		ev, err := events.Recv(ctx)
		if err != nil {
			return err
		}
		eff := a.respond(ev)
		if eff == nil {
			continue
		}
		eff.PluginInstanceID = ev.PluginInstanceID
		eff.Sequence = ev.Sequence
		eff.SchemaVersion = application.SchemaVersion
		if err := effects.Send(ctx, eff); err != nil {
			return err
		}
	}
}

// respond maps one event to at most one effect. Only the closed
// ApplicationEffect set is emitted; no arbitrary SQL, shell or file effect.
func (a *App) respond(ev *application.ApplicationEvent) *application.ApplicationEffect {
	switch body := ev.Union.(type) {
	case *application.CapabilityEvent:
		payload := body.PayloadJSON
		if payload == "" {
			payload = "{}"
		}
		return &application.ApplicationEffect{
			Union: &application.UpsertDomainRecord{
				RecordType: "temperature_reading",
				RecordID:   body.EntityID,
				DataJSON:   payload,
				Version:    "1",
			},
		}
	case *application.ScheduleTick:
		return &application.ApplicationEffect{
			Union: &application.SendNotification{
				Title:    "Template app tick",
				Body:     "A scheduled tick occurred at " + body.OccurredAt,
				Severity: "info",
			},
		}
	default:
		return nil
	}
}

// HandleRequest serves the plugin HTTP subroute. Core injects tenant/actor/
// instance/scope in the request context and scopes the route to
// /api/plugins/{id}/instances/{instance}/... The template returns a small
// JSON body; it never reads Core SQLite or global credentials.
func (a *App) HandleRequest(ctx context.Context, req *application.PluginHTTPRequest) (*application.PluginHTTPResponse, error) {
	if a.shutdown {
		return nil, unavailable()
	}
	body, _ := json.Marshal(map[string]string{"plugin": appID, "ok": "true"})
	return &application.PluginHTTPResponse{
		StatusCode: http.StatusOK,
		Headers:    map[string]string{"content-type": "application/json"},
		Body:       body,
	}, nil
}

// RunJob executes a declared job. The template returns an empty result.
func (a *App) RunJob(ctx context.Context, req *application.RunJobRequest) (*application.RunJobResponse, error) {
	if a.shutdown {
		return nil, unavailable()
	}
	return &application.RunJobResponse{
		JobID:      req.JobID,
		Status:     status.New(),
		ResultJSON: `{"ok":true}`,
	}, nil
}

// Health returns the serving state.
func (a *App) Health(ctx context.Context) (*application.HealthResponse, error) {
	if a.shutdown {
		return &application.HealthResponse{State: application.HealthStateNotServing}, nil
	}
	return &application.HealthResponse{State: application.HealthStateServing}, nil
}

// Shutdown marks the plugin as stopped.
func (a *App) Shutdown(ctx context.Context, req *application.ShutdownRequest) (*application.ShutdownResponse, error) {
	a.shutdown = true
	return &application.ShutdownResponse{Status: status.New()}, nil
}
