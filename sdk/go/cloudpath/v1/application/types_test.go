package application

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestApplicationMessageRoundtrip checks every ApplicationEvent and
// ApplicationEffect variant through JSON encoding/decoding. The effect set
// must stay closed: only the six Core-approved operations exist.
func TestApplicationMessageRoundtrip(t *testing.T) {
	events := []*ApplicationEvent{
		{
			PluginInstanceID: "app-1", Sequence: 1, SchemaVersion: "1",
			Union: &CapabilityEvent{
				RequirementID: "compartments", EntityID: "stcb-001/compartment-1",
				EventType:   "cloudpath.dev/capability/contact@1/opened",
				PayloadJSON: `{"slot":1}`, OccurredAt: "2026-09-03T08:00:00Z",
			},
		},
		{
			PluginInstanceID: "app-1", Sequence: 2, SchemaVersion: "1",
			Union: &ScheduleTick{ScheduleID: "s-1", OccurredAt: "2026-09-03T08:00:00Z", WindowJSON: `{"start":"08:00"}`},
		},
		{
			PluginInstanceID: "app-1", Sequence: 3, SchemaVersion: "1",
			Union: &RequestCompleted{
				RequestID: "r-1", EntityID: "stcb-001/alarm", Action: "trigger",
				State: CommandStateSucceeded, ResultJSON: `{"ok":true}`,
			},
		},
		{
			PluginInstanceID: "app-1", Sequence: 4, SchemaVersion: "1",
			Union: &InstanceLifecycle{State: "started", Detail: "configured"},
		},
	}
	for i, want := range events {
		b, err := json.Marshal(want)
		if err != nil {
			t.Fatalf("event %d Marshal: %v", i, err)
		}
		var got ApplicationEvent
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatalf("event %d Unmarshal %s: %v", i, b, err)
		}
		if !reflect.DeepEqual(&got, want) {
			t.Fatalf("event %d mismatch:\n got  %#v\n want %#v", i, &got, want)
		}
	}

	effects := []*ApplicationEffect{
		{
			PluginInstanceID: "app-1", Sequence: 1, SchemaVersion: "1",
			Union: &UpsertDomainRecord{RecordType: "schedule", RecordID: "s-1", DataJSON: `{"slot":1}`, Version: "1"},
		},
		{
			PluginInstanceID: "app-1", Sequence: 2, SchemaVersion: "1",
			Union: &DeleteDomainRecord{RecordType: "schedule", RecordID: "s-1", Version: "2"},
		},
		{
			PluginInstanceID: "app-1", Sequence: 3, SchemaVersion: "1",
			Union: &RequestCommand{EntityID: "stcb-001/alarm", Action: "trigger", ArgsJSON: `{"duration":200}`, IdempotencyKey: "k-1", Deadline: "2026-09-03T08:05:00Z"},
		},
		{
			PluginInstanceID: "app-1", Sequence: 4, SchemaVersion: "1",
			Union: &ScheduleTask{ScheduleID: "s-2", Cron: "0 8 * * *", PayloadJSON: `{"slot":1}`},
		},
		{
			PluginInstanceID: "app-1", Sequence: 5, SchemaVersion: "1",
			Union: &CancelScheduledTask{ScheduleID: "s-2"},
		},
		{
			PluginInstanceID: "app-1", Sequence: 6, SchemaVersion: "1",
			Union: &SendNotification{Title: "Reminder", Body: "take slot 1", Severity: "info"},
		},
	}
	for i, want := range effects {
		b, err := json.Marshal(want)
		if err != nil {
			t.Fatalf("effect %d Marshal: %v", i, err)
		}
		var got ApplicationEffect
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatalf("effect %d Unmarshal %s: %v", i, b, err)
		}
		if !reflect.DeepEqual(&got, want) {
			t.Fatalf("effect %d mismatch:\n got  %#v\n want %#v", i, &got, want)
		}
	}
}

// TestApplicationEffectClosedSet guards the design constraint: effects are a
// closed set and must never grow a generic escape hatch.
func TestApplicationEffectClosedSet(t *testing.T) {
	// The wire shape carries exactly the six approved keys; an unknown key
	// must be rejected as "no body" rather than silently accepted.
	bad := `{"plugin_instance_id":"app-1","sequence":1,"schema_version":"1","run_sql":"DROP TABLE"}`
	var e ApplicationEffect
	if err := json.Unmarshal([]byte(bad), &e); err == nil {
		t.Fatal("unknown effect key must be rejected")
	}
}
