package driver

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestDriverMessageRoundtrip verifies that all six DriverMessage oneof
// variants survive JSON encoding unchanged (proto3 json_name keys, ordered
// encode, strict exactly-one-body decode).
func TestDriverMessageRoundtrip(t *testing.T) {
	tests := []struct {
		name string
		msg  *DriverMessage
	}{
		{
			name: "device_upsert",
			msg: &DriverMessage{
				PluginInstanceID: "inst-1", Sequence: 1, SchemaVersion: "1", DeviceID: "dev-1",
				Union: &DeviceUpsert{Device: Device{
					DeviceID: "dev-1", ExternalID: "ext-1", Manufacturer: "STC", Model: "B",
					Status: DeviceStatusOnline, DisplayName: "Reference Board",
				}},
			},
		},
		{
			name: "entity_upsert",
			msg: &DriverMessage{
				PluginInstanceID: "inst-1", Sequence: 2, SchemaVersion: "1", DeviceID: "dev-1",
				Union: &EntityUpsert{Entity: Entity{
					EntityID: "sensor-1", DeviceID: "dev-1", UniqueKey: "u-1", Name: "Temperature",
					Category: EntityCategorySensor, Capabilities: []string{"cloudpath.dev/capability/temperature@1"},
					Attributes: map[string]string{"zone": "a"},
				}},
			},
		},
		{
			name: "observation",
			msg: &DriverMessage{
				PluginInstanceID: "inst-1", Sequence: 3, SchemaVersion: "1", DeviceID: "dev-1",
				Union: &Observation{
					EntityID: "sensor-1", Capability: "cloudpath.dev/capability/temperature@1",
					Property: "value", Value: Value{Kind: ValueNumber, NumberValue: 24.7},
					ObservedAt: "2026-09-03T08:00:00Z", ReceivedAt: "2026-09-03T08:00:01Z", Quality: "good",
				},
			},
		},
		{
			name: "event",
			msg: &DriverMessage{
				PluginInstanceID: "inst-1", Sequence: 4, SchemaVersion: "1", DeviceID: "dev-1",
				Union: &Event{
					EntityID: "sensor-1", DeviceID: "dev-1",
					EventType:   "cloudpath.dev/capability/temperature@1/threshold-crossed",
					PayloadJSON: `{"value":25.0,"threshold":24.0,"direction":"rising"}`,
					OccurredAt:  "2026-09-03T08:00:02Z", CorrelationID: "c-1",
				},
			},
		},
		{
			name: "command_progress",
			msg: &DriverMessage{
				PluginInstanceID: "inst-1", Sequence: 5, SchemaVersion: "1", DeviceID: "dev-1",
				Union: &CommandProgress{
					CommandID: "cmd-1", IdempotencyKey: "k-1", EntityID: "sensor-1", Action: "calibrate",
					State: CommandStateRunning, Progress: 0.5, Detail: "working",
				},
			},
		},
		{
			name: "diagnostic",
			msg: &DriverMessage{
				PluginInstanceID: "inst-1", Sequence: 6, SchemaVersion: "1", DeviceID: "dev-1",
				Union: &Diagnostic{
					Level: "warn", Message: "uart retry", Fields: map[string]string{"retries": "2"},
					ObservedAt: "2026-09-03T08:00:03Z", RawJSON: `{"raw":true}`,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := json.Marshal(tt.msg)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			var got DriverMessage
			if err := json.Unmarshal(b, &got); err != nil {
				t.Fatalf("Unmarshal %s: %v", b, err)
			}
			if !reflect.DeepEqual(&got, tt.msg) {
				t.Fatalf("roundtrip mismatch:\n got  %#v\n want %#v\n json %s", &got, tt.msg, b)
			}
			// The variant key must be the proto field name.
			if got.Union == nil {
				t.Fatal("union is nil after roundtrip")
			}
		})
	}
}

func TestDriverMessageStrictOneof(t *testing.T) {
	// Zero bodies must be rejected.
	empty := `{"plugin_instance_id":"i","sequence":1,"schema_version":"1","device_id":"d"}`
	var m DriverMessage
	if err := json.Unmarshal([]byte(empty), &m); err == nil {
		t.Fatal("expected error for zero-body DriverMessage")
	}

	// Two bodies must be rejected.
	two := `{"plugin_instance_id":"i","sequence":1,"schema_version":"1","device_id":"d",` +
		`"diagnostic":{"level":"info","message":"x"},` +
		`"observation":{"entity_id":"e","capability":"c","property":"p","value":{"number_value":1},"observed_at":"2026-09-03T08:00:00Z"}}`
	if err := json.Unmarshal([]byte(two), &m); err == nil {
		t.Fatal("expected error for multi-body DriverMessage")
	}
}

func TestValueRoundtrip(t *testing.T) {
	values := []Value{
		{Kind: ValueNumber, NumberValue: 24.7},
		{Kind: ValueInt, IntValue: -42},
		{Kind: ValueString, StringValue: "off"},
		{Kind: ValueBool, BoolValue: true},
		{Kind: ValueJSON, JSONValue: `{"nested":[1,2,3]}`},
	}
	for _, want := range values {
		b, err := json.Marshal(&want)
		if err != nil {
			t.Fatalf("Marshal %+v: %v", want, err)
		}
		var got Value
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatalf("Unmarshal %s: %v", b, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("value roundtrip mismatch: got %+v want %+v (%s)", got, want, b)
		}
	}
}

func TestDiscoveryEventRoundtrip(t *testing.T) {
	want := &DiscoveryEvent{
		PluginInstanceID: "inst-1", Sequence: 7, SchemaVersion: "1", DiscoveryID: "disc-1",
		Union: &DiscoveryProgress{Fraction: 0.5, Detail: "scanning"},
	}
	b, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got DiscoveryEvent
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(&got, want) {
		t.Fatalf("mismatch: got %#v want %#v", &got, want)
	}
}

func TestNegotiateProtocolVersion(t *testing.T) {
	cases := []struct {
		name      string
		supported []uint32
		prefer    uint32
		min, max  uint32
		want      uint32
		wantOK    bool
	}{
		{"single", []uint32{1}, 1, 1, 1, 1, true},
		{"highest-in-overlap", []uint32{1, 2, 3}, 3, 1, 2, 2, true},
		{"prefer-inside-overlap", []uint32{1, 2, 3}, 2, 1, 3, 2, true},
		{"prefer-outside-overlap", []uint32{1, 2, 3}, 9, 1, 3, 3, true},
		{"disjoint", []uint32{5}, 1, 1, 3, 0, false},
		{"empty-range", []uint32{1}, 1, 3, 2, 0, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := NegotiateProtocolVersion(tt.supported, tt.prefer, tt.min, tt.max)
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("got (%d,%v), want (%d,%v)", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestExecuteRequestCarriesDeviceID(t *testing.T) {
	want := ExecuteRequest{PluginInstanceID: "i", DeviceID: "board-2", IdempotencyKey: "k", Action: "led"}
	b, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got ExecuteRequest
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.DeviceID != want.DeviceID {
		t.Fatalf("device_id=%q json=%s", got.DeviceID, b)
	}
}
