package appruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	coreapplication "github.com/DeliciousBuding/cloud-path/internal/application"
	sdkapplication "github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/application"
)

// EffectKind is the closed set of operations an Application Effect may
// request from Core.
type EffectKind string

const (
	// EffectCreateDomainRecord creates or updates a namespaced domain record.
	EffectCreateDomainRecord EffectKind = "create_domain_record"
	// EffectRequestCommand asks Core to dispatch a command to a bound entity.
	EffectRequestCommand EffectKind = "request_command"
	// EffectScheduleJob asks Core to schedule a job.
	EffectScheduleJob EffectKind = "schedule_job"
	// EffectSendNotification asks Core to send a notification.
	EffectSendNotification EffectKind = "send_notification"
)

// Valid reports whether k is one of the allowed effect kinds.
func (k EffectKind) Valid() bool {
	switch k {
	case EffectCreateDomainRecord, EffectRequestCommand, EffectScheduleJob, EffectSendNotification:
		return true
	}
	return false
}

// Effect is the runtime's validated, Core-approved effect model. Exactly one
// of the typed payloads must be set for its Kind. Unknown SQL/shell/file
// operations cannot be represented by this type; callers that bypass the
// closed union must set an invalid Kind and are rejected by Validate.
type Effect struct {
	ID             string
	IdempotencyKey string
	TenantID       string
	Kind           EffectKind

	CreateDomainRecord *CreateDomainRecord `json:",omitempty"`
	RequestCommand     *RequestCommand     `json:",omitempty"`
	ScheduleJob        *ScheduleJob        `json:",omitempty"`
	SendNotification   *SendNotification   `json:",omitempty"`
}

// EffectExecutor is implemented by Core. It receives only effects that have
// already passed Effect.Validate and the instance tenant check.
type EffectExecutor interface {
	Execute(ctx context.Context, effect Effect) error
}

// CreateDomainRecord is the bounded payload for EffectCreateDomainRecord.
type CreateDomainRecord struct {
	RecordType string
	RecordID   string
	DataJSON   string
	Version    string
}

// RequestCommand is the bounded payload for EffectRequestCommand.
type RequestCommand struct {
	EntityID       string
	Action         string
	ArgsJSON       string
	IdempotencyKey string
	Deadline       string
}

// ScheduleJob is the bounded payload for EffectScheduleJob.
type ScheduleJob struct {
	ScheduleID  string
	Cron        string
	PayloadJSON string
}

// SendNotification is the bounded payload for EffectSendNotification.
type SendNotification struct {
	Title    string
	Body     string
	Severity string
}

// Size bounds for every effect field. Effects that exceed a bound are rejected
// before the executor is called.
const (
	maxEffectIDLen             = 256
	maxIdempotencyKeyLen       = 512
	maxRecordTypeLen           = 128
	maxRecordIDLen             = 256
	maxDataJSONBytes           = 64 * 1024
	maxVersionLen              = 64
	maxActionLen               = 128
	maxArgsJSONBytes           = 64 * 1024
	maxDeadlineLen             = 64
	maxScheduleIDLen           = 256
	maxCronLen                 = 256
	maxPayloadJSONBytes        = 64 * 1024
	maxNotificationTitleLen    = 256
	maxNotificationBodyLen     = 16 * 1024
	maxNotificationSeverityLen = 32
)

// EffectSource is the instance context used to authorize an SDK effect. The
// tenant is never read from the plugin; it is stamped from this source.
type EffectSource struct {
	PluginInstanceID string
	TenantID         string
	Bindings         []coreapplication.Binding
	Candidates       []coreapplication.Candidate
}

// EffectFromSDK converts and authorizes a raw SDK ApplicationEffect against the
// instance source. It rejects unknown effect variants, effects that spoof
// another instance, and RequestCommand effects that target an unbound or
// cross-tenant entity.
func EffectFromSDK(raw *sdkapplication.ApplicationEffect, src EffectSource) (Effect, error) {
	if raw == nil {
		return Effect{}, fmt.Errorf("%w: nil effect", ErrInvalidEffect)
	}
	if raw.PluginInstanceID != "" && raw.PluginInstanceID != src.PluginInstanceID {
		return Effect{}, fmt.Errorf("%w: effect instance %q does not match %q", ErrInvalidEffect, raw.PluginInstanceID, src.PluginInstanceID)
	}

	switch v := raw.Union.(type) {
	case *sdkapplication.UpsertDomainRecord:
		payload := &CreateDomainRecord{
			RecordType: v.RecordType,
			RecordID:   v.RecordID,
			DataJSON:   v.DataJSON,
			Version:    v.Version,
		}
		key := "domain:" + payload.RecordType + "/" + payload.RecordID
		return newEffect(EffectCreateDomainRecord, key, src.TenantID, payload)
	case *sdkapplication.RequestCommand:
		if !entityBound(src, v.EntityID) {
			return Effect{}, fmt.Errorf("%w: entity %q is not bound to this instance", ErrCrossTenantEffect, v.EntityID)
		}
		if !entityInTenant(src, v.EntityID) {
			return Effect{}, fmt.Errorf("%w: entity %q is not in tenant %q", ErrCrossTenantEffect, v.EntityID, src.TenantID)
		}
		payload := &RequestCommand{
			EntityID:       v.EntityID,
			Action:         v.Action,
			ArgsJSON:       v.ArgsJSON,
			IdempotencyKey: v.IdempotencyKey,
			Deadline:       v.Deadline,
		}
		key := "command:" + payload.IdempotencyKey
		return newEffect(EffectRequestCommand, key, src.TenantID, payload)
	case *sdkapplication.ScheduleTask:
		payload := &ScheduleJob{
			ScheduleID:  v.ScheduleID,
			Cron:        v.Cron,
			PayloadJSON: v.PayloadJSON,
		}
		key := "schedule:" + payload.ScheduleID
		return newEffect(EffectScheduleJob, key, src.TenantID, payload)
	case *sdkapplication.SendNotification:
		payload := &SendNotification{
			Title:    v.Title,
			Body:     v.Body,
			Severity: v.Severity,
		}
		key := "notification:" + notificationKey(payload)
		return newEffect(EffectSendNotification, key, src.TenantID, payload)
	default:
		return Effect{}, fmt.Errorf("%w: effect union %T is not in the allowed set", ErrUnknownEffect, raw.Union)
	}
}

// Validate checks the closed union, tenant stamp, idempotency key and every
// payload bound. It returns an error wrapping ErrInvalidEffect on failure.
func (e Effect) Validate() error {
	var errs []error
	if !e.Kind.Valid() {
		errs = append(errs, fmt.Errorf("effect kind %q is not allowed", e.Kind))
	}
	if e.TenantID == "" {
		errs = append(errs, errors.New("effect tenant must not be empty"))
	}
	if e.IdempotencyKey == "" || len(e.IdempotencyKey) > maxIdempotencyKeyLen {
		errs = append(errs, errors.New("effect idempotency key must be non-empty and bounded"))
	}
	if e.ID == "" || len(e.ID) > maxEffectIDLen {
		errs = append(errs, errors.New("effect id must be non-empty and bounded"))
	}

	payloadCount := 0
	switch e.Kind {
	case EffectCreateDomainRecord:
		if e.CreateDomainRecord == nil {
			errs = append(errs, errors.New("create_domain_record effect is missing its payload"))
		} else {
			errs = append(errs, validateCreateDomainRecord(e.CreateDomainRecord)...)
			payloadCount++
		}
		if e.RequestCommand != nil || e.ScheduleJob != nil || e.SendNotification != nil {
			errs = append(errs, errors.New("create_domain_record effect carries a mismatched payload"))
		}
	case EffectRequestCommand:
		if e.RequestCommand == nil {
			errs = append(errs, errors.New("request_command effect is missing its payload"))
		} else {
			errs = append(errs, validateRequestCommand(e.RequestCommand)...)
			payloadCount++
		}
		if e.CreateDomainRecord != nil || e.ScheduleJob != nil || e.SendNotification != nil {
			errs = append(errs, errors.New("request_command effect carries a mismatched payload"))
		}
	case EffectScheduleJob:
		if e.ScheduleJob == nil {
			errs = append(errs, errors.New("schedule_job effect is missing its payload"))
		} else {
			errs = append(errs, validateScheduleJob(e.ScheduleJob)...)
			payloadCount++
		}
		if e.CreateDomainRecord != nil || e.RequestCommand != nil || e.SendNotification != nil {
			errs = append(errs, errors.New("schedule_job effect carries a mismatched payload"))
		}
	case EffectSendNotification:
		if e.SendNotification == nil {
			errs = append(errs, errors.New("send_notification effect is missing its payload"))
		} else {
			errs = append(errs, validateSendNotification(e.SendNotification)...)
			payloadCount++
		}
		if e.CreateDomainRecord != nil || e.RequestCommand != nil || e.ScheduleJob != nil {
			errs = append(errs, errors.New("send_notification effect carries a mismatched payload"))
		}
	}
	if payloadCount == 0 && e.Kind.Valid() {
		errs = append(errs, errors.New("effect has no payload"))
	}
	return errors.Join(errs...)
}

// ErrUnknownEffect is returned when an SDK effect variant is outside the
// whitelist.
var ErrUnknownEffect = errors.New("appruntime: unknown application effect")

// ErrCrossTenantEffect is returned when an effect targets another tenant or an
// unbound entity.
var ErrCrossTenantEffect = errors.New("appruntime: cross-tenant application effect")

// ErrInvalidEffect is the base error for malformed effects.
var ErrInvalidEffect = errors.New("appruntime: invalid effect")

func entityBound(src EffectSource, entityID string) bool {
	for _, b := range src.Bindings {
		if b.EntityID == entityID {
			return true
		}
	}
	return false
}

func entityInTenant(src EffectSource, entityID string) bool {
	for _, c := range src.Candidates {
		if c.EntityID == entityID && c.TenantID == src.TenantID {
			return true
		}
	}
	return false
}

func newEffect(kind EffectKind, key, tenant string, payload any) (Effect, error) {
	e := Effect{Kind: kind, IdempotencyKey: key, TenantID: tenant}
	switch p := payload.(type) {
	case *CreateDomainRecord:
		e.CreateDomainRecord = p
	case *RequestCommand:
		e.RequestCommand = p
	case *ScheduleJob:
		e.ScheduleJob = p
	case *SendNotification:
		e.SendNotification = p
	default:
		return Effect{}, fmt.Errorf("%w: unsupported payload %T", ErrInvalidEffect, payload)
	}
	e.ID = effectID(kind, key, tenant, payload)
	if err := e.Validate(); err != nil {
		return Effect{}, err
	}
	return e, nil
}

func effectID(kind EffectKind, key, tenant string, payload any) string {
	h := sha256.New()
	_, _ = h.Write([]byte(tenant))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(kind))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(key))
	_, _ = h.Write([]byte{0})
	_ = json.NewEncoder(h).Encode(payload)
	return hex.EncodeToString(h.Sum(nil))
}

func notificationKey(n *SendNotification) string {
	h := sha256.New()
	_, _ = h.Write([]byte(n.Title))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(n.Body))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(n.Severity))
	return hex.EncodeToString(h.Sum(nil))
}

func validateCreateDomainRecord(p *CreateDomainRecord) []error {
	var errs []error
	if strings.TrimSpace(p.RecordType) == "" || len(p.RecordType) > maxRecordTypeLen {
		errs = append(errs, errors.New("record_type must be non-empty and bounded"))
	}
	if strings.TrimSpace(p.RecordID) == "" || len(p.RecordID) > maxRecordIDLen {
		errs = append(errs, errors.New("record_id must be non-empty and bounded"))
	}
	if len(p.DataJSON) > maxDataJSONBytes {
		errs = append(errs, errors.New("data_json exceeds size bound"))
	}
	if len(p.Version) > maxVersionLen {
		errs = append(errs, errors.New("version exceeds size bound"))
	}
	return errs
}

func validateRequestCommand(p *RequestCommand) []error {
	var errs []error
	if strings.TrimSpace(p.EntityID) == "" {
		errs = append(errs, errors.New("request_command entity_id must not be empty"))
	}
	if strings.TrimSpace(p.Action) == "" || len(p.Action) > maxActionLen {
		errs = append(errs, errors.New("request_command action must be non-empty and bounded"))
	}
	if len(p.ArgsJSON) > maxArgsJSONBytes {
		errs = append(errs, errors.New("request_command args_json exceeds size bound"))
	}
	if p.IdempotencyKey == "" || len(p.IdempotencyKey) > maxIdempotencyKeyLen {
		errs = append(errs, errors.New("request_command idempotency_key must be non-empty and bounded"))
	}
	if len(p.Deadline) > maxDeadlineLen {
		errs = append(errs, errors.New("request_command deadline exceeds size bound"))
	}
	return errs
}

func validateScheduleJob(p *ScheduleJob) []error {
	var errs []error
	if strings.TrimSpace(p.ScheduleID) == "" || len(p.ScheduleID) > maxScheduleIDLen {
		errs = append(errs, errors.New("schedule_id must be non-empty and bounded"))
	}
	if len(p.Cron) > maxCronLen {
		errs = append(errs, errors.New("cron exceeds size bound"))
	}
	if len(p.PayloadJSON) > maxPayloadJSONBytes {
		errs = append(errs, errors.New("payload_json exceeds size bound"))
	}
	return errs
}

func validateSendNotification(p *SendNotification) []error {
	var errs []error
	if len(p.Title) > maxNotificationTitleLen {
		errs = append(errs, errors.New("notification title exceeds size bound"))
	}
	if len(p.Body) > maxNotificationBodyLen {
		errs = append(errs, errors.New("notification body exceeds size bound"))
	}
	if len(p.Severity) > maxNotificationSeverityLen {
		errs = append(errs, errors.New("notification severity exceeds size bound"))
	}
	return errs
}
