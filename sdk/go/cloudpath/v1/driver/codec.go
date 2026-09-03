package driver

import (
	"encoding/json"
	"fmt"
)

// The wire shapes below mirror the proto oneof exactly: the envelope carries
// the variant key and at most one variant body. json_name keys are used.

type driverMessageWire struct {
	PluginInstanceID string           `json:"plugin_instance_id"`
	Sequence         uint64           `json:"sequence"`
	SchemaVersion    string           `json:"schema_version"`
	DeviceID         string           `json:"device_id"`
	DeviceUpsert     *DeviceUpsert    `json:"device_upsert,omitempty"`
	EntityUpsert     *EntityUpsert    `json:"entity_upsert,omitempty"`
	Observation      *Observation     `json:"observation,omitempty"`
	Event            *Event           `json:"event,omitempty"`
	CommandProgress  *CommandProgress `json:"command_progress,omitempty"`
	Diagnostic       *Diagnostic      `json:"diagnostic,omitempty"`
}

// MarshalJSON implements the DriverMessage oneof.
func (m *DriverMessage) MarshalJSON() ([]byte, error) {
	w := driverMessageWire{
		PluginInstanceID: m.PluginInstanceID,
		Sequence:         m.Sequence,
		SchemaVersion:    m.SchemaVersion,
		DeviceID:         m.DeviceID,
	}
	switch v := m.Union.(type) {
	case *DeviceUpsert:
		w.DeviceUpsert = v
	case *EntityUpsert:
		w.EntityUpsert = v
	case *Observation:
		w.Observation = v
	case *Event:
		w.Event = v
	case *CommandProgress:
		w.CommandProgress = v
	case *Diagnostic:
		w.Diagnostic = v
	case nil:
		return nil, fmt.Errorf("driver: DriverMessage has no union body")
	default:
		return nil, fmt.Errorf("driver: unsupported DriverMessage union type %T", m.Union)
	}
	return json.Marshal(w)
}

// UnmarshalJSON implements the DriverMessage oneof and rejects messages with
// zero or multiple bodies.
func (m *DriverMessage) UnmarshalJSON(data []byte) error {
	var w driverMessageWire
	if err := json.Unmarshal(data, &w); err != nil {
		return fmt.Errorf("driver: decode DriverMessage: %w", err)
	}
	m.PluginInstanceID = w.PluginInstanceID
	m.Sequence = w.Sequence
	m.SchemaVersion = w.SchemaVersion
	m.DeviceID = w.DeviceID

	count := 0
	switch {
	case w.DeviceUpsert != nil:
		m.Union = w.DeviceUpsert
		count++
	}
	switch {
	case w.EntityUpsert != nil:
		m.Union = w.EntityUpsert
		count++
	}
	switch {
	case w.Observation != nil:
		m.Union = w.Observation
		count++
	}
	switch {
	case w.Event != nil:
		m.Union = w.Event
		count++
	}
	switch {
	case w.CommandProgress != nil:
		m.Union = w.CommandProgress
		count++
	}
	switch {
	case w.Diagnostic != nil:
		m.Union = w.Diagnostic
		count++
	}
	if count == 0 {
		return fmt.Errorf("driver: DriverMessage must contain exactly one body, got 0")
	}
	if count > 1 {
		return fmt.Errorf("driver: DriverMessage must contain exactly one body, got %d", count)
	}
	return nil
}

type discoveryEventWire struct {
	PluginInstanceID string                `json:"plugin_instance_id"`
	Sequence         uint64                `json:"sequence"`
	SchemaVersion    string                `json:"schema_version"`
	DiscoveryID      string                `json:"discovery_id"`
	Started          *DiscoveryStarted     `json:"started,omitempty"`
	FoundDevice      *DiscoveryFoundDevice `json:"found_device,omitempty"`
	Progress         *DiscoveryProgress    `json:"progress,omitempty"`
	Finished         *DiscoveryFinished    `json:"finished,omitempty"`
	Failed           *DiscoveryFailed      `json:"failed,omitempty"`
}

// MarshalJSON implements the DiscoveryEvent oneof.
func (e *DiscoveryEvent) MarshalJSON() ([]byte, error) {
	w := discoveryEventWire{
		PluginInstanceID: e.PluginInstanceID,
		Sequence:         e.Sequence,
		SchemaVersion:    e.SchemaVersion,
		DiscoveryID:      e.DiscoveryID,
	}
	switch v := e.Union.(type) {
	case *DiscoveryStarted:
		w.Started = v
	case *DiscoveryFoundDevice:
		w.FoundDevice = v
	case *DiscoveryProgress:
		w.Progress = v
	case *DiscoveryFinished:
		w.Finished = v
	case *DiscoveryFailed:
		w.Failed = v
	case nil:
		return nil, fmt.Errorf("driver: DiscoveryEvent has no union body")
	default:
		return nil, fmt.Errorf("driver: unsupported DiscoveryEvent union type %T", e.Union)
	}
	return json.Marshal(w)
}

// UnmarshalJSON implements the DiscoveryEvent oneof.
func (e *DiscoveryEvent) UnmarshalJSON(data []byte) error {
	var w discoveryEventWire
	if err := json.Unmarshal(data, &w); err != nil {
		return fmt.Errorf("driver: decode DiscoveryEvent: %w", err)
	}
	e.PluginInstanceID = w.PluginInstanceID
	e.Sequence = w.Sequence
	e.SchemaVersion = w.SchemaVersion
	e.DiscoveryID = w.DiscoveryID

	count := 0
	switch {
	case w.Started != nil:
		e.Union = w.Started
		count++
	}
	switch {
	case w.FoundDevice != nil:
		e.Union = w.FoundDevice
		count++
	}
	switch {
	case w.Progress != nil:
		e.Union = w.Progress
		count++
	}
	switch {
	case w.Finished != nil:
		e.Union = w.Finished
		count++
	}
	switch {
	case w.Failed != nil:
		e.Union = w.Failed
		count++
	}
	if count == 0 {
		return fmt.Errorf("driver: DiscoveryEvent must contain exactly one body, got 0")
	}
	if count > 1 {
		return fmt.Errorf("driver: DiscoveryEvent must contain exactly one body, got %d", count)
	}
	return nil
}

type valueWire struct {
	NumberValue *float64 `json:"number_value,omitempty"`
	IntValue    *int64   `json:"int_value,omitempty"`
	StringValue *string  `json:"string_value,omitempty"`
	BoolValue   *bool    `json:"bool_value,omitempty"`
	JSONValue   *string  `json:"json_value,omitempty"`
}

// MarshalJSON implements the Value oneof.
func (v *Value) MarshalJSON() ([]byte, error) {
	switch v.Kind {
	case ValueNumber:
		n := v.NumberValue
		return json.Marshal(valueWire{NumberValue: &n})
	case ValueInt:
		n := v.IntValue
		return json.Marshal(valueWire{IntValue: &n})
	case ValueString:
		s := v.StringValue
		return json.Marshal(valueWire{StringValue: &s})
	case ValueBool:
		b := v.BoolValue
		return json.Marshal(valueWire{BoolValue: &b})
	case ValueJSON:
		s := v.JSONValue
		return json.Marshal(valueWire{JSONValue: &s})
	default:
		return nil, fmt.Errorf("driver: Value has unknown kind %d", v.Kind)
	}
}

// UnmarshalJSON implements the Value oneof.
func (v *Value) UnmarshalJSON(data []byte) error {
	var w valueWire
	if err := json.Unmarshal(data, &w); err != nil {
		return fmt.Errorf("driver: decode Value: %w", err)
	}
	count := 0
	switch {
	case w.NumberValue != nil:
		v.Kind = ValueNumber
		v.NumberValue = *w.NumberValue
		count++
	}
	switch {
	case w.IntValue != nil:
		v.Kind = ValueInt
		v.IntValue = *w.IntValue
		count++
	}
	switch {
	case w.StringValue != nil:
		v.Kind = ValueString
		v.StringValue = *w.StringValue
		count++
	}
	switch {
	case w.BoolValue != nil:
		v.Kind = ValueBool
		v.BoolValue = *w.BoolValue
		count++
	}
	switch {
	case w.JSONValue != nil:
		v.Kind = ValueJSON
		v.JSONValue = *w.JSONValue
		count++
	}
	if count == 0 {
		return fmt.Errorf("driver: Value must contain exactly one kind, got 0")
	}
	if count > 1 {
		return fmt.Errorf("driver: Value must contain exactly one kind, got %d", count)
	}
	return nil
}

// String returns a stable, human-readable name for the state.
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

// String returns a stable, human-readable name for the device status.
func (s DeviceStatus) String() string {
	switch s {
	case DeviceStatusOnline:
		return "online"
	case DeviceStatusOffline:
		return "offline"
	case DeviceStatusUnavailable:
		return "unavailable"
	case DeviceStatusDegraded:
		return "degraded"
	default:
		return "unspecified"
	}
}
