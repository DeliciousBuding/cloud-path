package application

import (
	"encoding/json"
	"fmt"
)

type applicationEventWire struct {
	PluginInstanceID  string             `json:"plugin_instance_id"`
	Sequence          uint64             `json:"sequence"`
	SchemaVersion     string             `json:"schema_version"`
	CapabilityEvent   *CapabilityEvent   `json:"capability_event,omitempty"`
	ScheduleTick      *ScheduleTick      `json:"schedule_tick,omitempty"`
	RequestCompleted  *RequestCompleted  `json:"request_completed,omitempty"`
	InstanceLifecycle *InstanceLifecycle `json:"instance_lifecycle,omitempty"`
}

// MarshalJSON implements the ApplicationEvent oneof.
func (e *ApplicationEvent) MarshalJSON() ([]byte, error) {
	w := applicationEventWire{
		PluginInstanceID: e.PluginInstanceID,
		Sequence:         e.Sequence,
		SchemaVersion:    e.SchemaVersion,
	}
	switch v := e.Union.(type) {
	case *CapabilityEvent:
		w.CapabilityEvent = v
	case *ScheduleTick:
		w.ScheduleTick = v
	case *RequestCompleted:
		w.RequestCompleted = v
	case *InstanceLifecycle:
		w.InstanceLifecycle = v
	case nil:
		return nil, fmt.Errorf("application: ApplicationEvent has no union body")
	default:
		return nil, fmt.Errorf("application: unsupported ApplicationEvent union type %T", e.Union)
	}
	return json.Marshal(w)
}

// UnmarshalJSON implements the ApplicationEvent oneof.
func (e *ApplicationEvent) UnmarshalJSON(data []byte) error {
	var w applicationEventWire
	if err := json.Unmarshal(data, &w); err != nil {
		return fmt.Errorf("application: decode ApplicationEvent: %w", err)
	}
	e.PluginInstanceID = w.PluginInstanceID
	e.Sequence = w.Sequence
	e.SchemaVersion = w.SchemaVersion

	count := 0
	switch {
	case w.CapabilityEvent != nil:
		e.Union = w.CapabilityEvent
		count++
	}
	switch {
	case w.ScheduleTick != nil:
		e.Union = w.ScheduleTick
		count++
	}
	switch {
	case w.RequestCompleted != nil:
		e.Union = w.RequestCompleted
		count++
	}
	switch {
	case w.InstanceLifecycle != nil:
		e.Union = w.InstanceLifecycle
		count++
	}
	if count == 0 {
		return fmt.Errorf("application: ApplicationEvent must contain exactly one body, got 0")
	}
	if count > 1 {
		return fmt.Errorf("application: ApplicationEvent must contain exactly one body, got %d", count)
	}
	return nil
}

type applicationEffectWire struct {
	PluginInstanceID string               `json:"plugin_instance_id"`
	Sequence         uint64               `json:"sequence"`
	SchemaVersion    string               `json:"schema_version"`
	UpsertRecord     *UpsertDomainRecord  `json:"upsert_record,omitempty"`
	DeleteRecord     *DeleteDomainRecord  `json:"delete_record,omitempty"`
	RequestCommand   *RequestCommand      `json:"request_command,omitempty"`
	ScheduleTask     *ScheduleTask        `json:"schedule_task,omitempty"`
	CancelScheduled  *CancelScheduledTask `json:"cancel_scheduled_task,omitempty"`
	SendNotification *SendNotification    `json:"send_notification,omitempty"`
}

// MarshalJSON implements the ApplicationEffect oneof.
func (e *ApplicationEffect) MarshalJSON() ([]byte, error) {
	w := applicationEffectWire{
		PluginInstanceID: e.PluginInstanceID,
		Sequence:         e.Sequence,
		SchemaVersion:    e.SchemaVersion,
	}
	switch v := e.Union.(type) {
	case *UpsertDomainRecord:
		w.UpsertRecord = v
	case *DeleteDomainRecord:
		w.DeleteRecord = v
	case *RequestCommand:
		w.RequestCommand = v
	case *ScheduleTask:
		w.ScheduleTask = v
	case *CancelScheduledTask:
		w.CancelScheduled = v
	case *SendNotification:
		w.SendNotification = v
	case nil:
		return nil, fmt.Errorf("application: ApplicationEffect has no union body")
	default:
		return nil, fmt.Errorf("application: unsupported ApplicationEffect union type %T", e.Union)
	}
	return json.Marshal(w)
}

// UnmarshalJSON implements the ApplicationEffect oneof.
func (e *ApplicationEffect) UnmarshalJSON(data []byte) error {
	var w applicationEffectWire
	if err := json.Unmarshal(data, &w); err != nil {
		return fmt.Errorf("application: decode ApplicationEffect: %w", err)
	}
	e.PluginInstanceID = w.PluginInstanceID
	e.Sequence = w.Sequence
	e.SchemaVersion = w.SchemaVersion

	count := 0
	switch {
	case w.UpsertRecord != nil:
		e.Union = w.UpsertRecord
		count++
	}
	switch {
	case w.DeleteRecord != nil:
		e.Union = w.DeleteRecord
		count++
	}
	switch {
	case w.RequestCommand != nil:
		e.Union = w.RequestCommand
		count++
	}
	switch {
	case w.ScheduleTask != nil:
		e.Union = w.ScheduleTask
		count++
	}
	switch {
	case w.CancelScheduled != nil:
		e.Union = w.CancelScheduled
		count++
	}
	switch {
	case w.SendNotification != nil:
		e.Union = w.SendNotification
		count++
	}
	if count == 0 {
		return fmt.Errorf("application: ApplicationEffect must contain exactly one body, got 0")
	}
	if count > 1 {
		return fmt.Errorf("application: ApplicationEffect must contain exactly one body, got %d", count)
	}
	return nil
}

// String returns a stable name for the command state.
func (s CommandState) String() string {
	switch s {
	case CommandStateCreated:
		return "CREATED"
	case CommandStateDispatched:
		return "DISPATCHED"
	case CommandStateAccepted:
		return "ACCEPTED"
	case CommandStateRunning:
		return "RUNNING"
	case CommandStateSucceeded:
		return "SUCCEEDED"
	case CommandStateFailed:
		return "FAILED"
	case CommandStateTimedOut:
		return "TIMED_OUT"
	case CommandStateCancelled:
		return "CANCELLED"
	default:
		return "UNSPECIFIED"
	}
}
