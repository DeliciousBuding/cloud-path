package edge

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/device"
	"github.com/DeliciousBuding/cloud-path/internal/model"
	"github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/driver"
)

func TestDriverValueToAny(t *testing.T) {
	cases := []struct {
		v    driver.Value
		want any
	}{
		{driver.Value{Kind: driver.ValueNumber, NumberValue: 25.5}, 25.5},
		{driver.Value{Kind: driver.ValueInt, IntValue: 42}, int64(42)},
		{driver.Value{Kind: driver.ValueString, StringValue: "hello"}, "hello"},
		{driver.Value{Kind: driver.ValueBool, BoolValue: true}, true},
		{driver.Value{Kind: driver.ValueJSON, JSONValue: `{"a":1}`}, `{"a":1}`},
		{driver.Value{Kind: driver.ValueUnspecified}, nil},
	}
	for _, c := range cases {
		got := driverValueToAny(c.v)
		if got != c.want {
			t.Fatalf("driverValueToAny(%v) = %v (%T), want %v", c.v.Kind, got, got, c.want)
		}
	}
}

func TestDeviceStatusMapping(t *testing.T) {
	cases := map[driver.DeviceStatus]model.DeviceStatus{
		driver.DeviceStatusOnline:      model.DeviceOnline,
		driver.DeviceStatusOffline:     model.DeviceOffline,
		driver.DeviceStatusDegraded:    model.DeviceDegraded,
		driver.DeviceStatusUnavailable: model.DeviceUnavailable,
		driver.DeviceStatusUnspecified: model.DeviceUnavailable,
	}
	for in, want := range cases {
		if got := deviceStatus(in); got != want {
			t.Fatalf("deviceStatus(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestEntityCategoryMapping(t *testing.T) {
	cases := map[driver.EntityCategory]model.EntityCategory{
		driver.EntityCategorySensor:      model.EntitySensor,
		driver.EntityCategoryActuator:    model.EntityActuator,
		driver.EntityCategoryDiagnostic:  model.EntityDiagnostic,
		driver.EntityCategoryConfig:      model.EntityConfig,
		driver.EntityCategoryUnspecified: model.EntitySensor,
	}
	for in, want := range cases {
		if got := entityCategory(in); got != want {
			t.Fatalf("entityCategory(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestExternalDeviceAccumulatesAndProjects(t *testing.T) {
	d := &externalDevice{
		id:         "dev1",
		instanceID: "stcb",
		entities:   map[string]driver.Entity{},
		obs:        map[string]map[string]driver.Observation{},
		done:       make(chan struct{}),
	}

	d.applyMessage(&driver.DriverMessage{DeviceID: "dev1", Union: &driver.DeviceUpsert{Device: driver.Device{
		DeviceID: "dev1", ExternalID: "COM3", Manufacturer: "STC-B", Model: "IAP15F2K61S2",
		Status: driver.DeviceStatusOnline, DisplayName: "board",
	}}}, nil)
	d.applyMessage(&driver.DriverMessage{DeviceID: "dev1", Union: &driver.EntityUpsert{Entity: driver.Entity{
		EntityID: "temperature", DeviceID: "dev1", UniqueKey: "temperature", Name: "温度",
		Category: driver.EntityCategorySensor, Capabilities: []string{"cloudpath.dev/capability/temperature@1"},
	}}}, nil)
	at := time.Now().UTC()
	d.applyMessage(&driver.DriverMessage{DeviceID: "dev1", Union: &driver.Observation{
		EntityID: "temperature", Capability: "cloudpath.dev/capability/temperature@1",
		Property: "value", Value: driver.Value{Kind: driver.ValueNumber, NumberValue: 25.5},
		ObservedAt: at.Format(time.RFC3339), Quality: "good",
	}}, nil)

	st := d.Snapshot()
	if !st.Online {
		t.Fatal("expected online")
	}
	if st.Raw["temperature.value"] != 25.5 {
		t.Fatalf("raw = %v", st.Raw)
	}

	desc := d.Descriptor()
	if desc.Status != model.DeviceOnline || desc.ExternalID != "COM3" {
		t.Fatalf("desc meta = %+v", desc)
	}
	if len(desc.Entities) != 1 {
		t.Fatalf("entities = %d, want 1", len(desc.Entities))
	}
	e := desc.Entities[0]
	if e.EntityID != "temperature" || e.Category != model.EntitySensor {
		t.Fatalf("entity = %+v", e)
	}
	obs, ok := e.Observations["value"]
	if !ok || obs.Value != 25.5 || obs.Quality != model.QualityGood {
		t.Fatalf("observation = %+v", obs)
	}
}

// TestExternalDeviceDescriptorEntityOrderIsDeterministic 锁死 descriptor 实体顺序：
// entities 底层是 map，迭代顺序随机。2026-09-05 D3 真板实测：顺序抖动让 Binder
// 的 first-match（one-or-more/min-1）每次绑到不同实体——button-indicator 重启后
// 绑到 key2，用户按 K1（key1）的事件全部静默丢弃；同时 descriptor 指纹（JSON 含
// 顺序）每拍都变，diff 抑制失效导致整份 descriptor 每个 poll 周期重发。
// Descriptor() 必须按 EntityID 排序，跨调用、跨重启完全确定。
func TestExternalDeviceDescriptorEntityOrderIsDeterministic(t *testing.T) {
	d := &externalDevice{
		id:         "dev1",
		instanceID: "stcb",
		entities:   map[string]driver.Entity{},
		obs:        map[string]map[string]driver.Observation{},
		done:       make(chan struct{}),
	}
	// 故意乱序 upsert（模拟 driver 声明序与字母序不一致 + map 随机迭代）
	for _, id := range []string{"key3", "buzzer", "key1", "display", "key2", "clock"} {
		d.applyMessage(&driver.DriverMessage{DeviceID: "dev1", Union: &driver.EntityUpsert{Entity: driver.Entity{
			EntityID: id, DeviceID: "dev1", UniqueKey: id, Name: id,
			Category: driver.EntityCategorySensor, Capabilities: []string{"cloudpath.dev/capability/key@1"},
		}}}, nil)
	}

	want := []string{"buzzer", "clock", "display", "key1", "key2", "key3"}
	// 多次调用：Go 的 map 迭代顺序每次都随机，排序缺失时几乎必然在某次暴露
	for iter := 0; iter < 50; iter++ {
		desc := d.Descriptor()
		if len(desc.Entities) != len(want) {
			t.Fatalf("entities = %d, want %d", len(desc.Entities), len(want))
		}
		for i, e := range desc.Entities {
			if e.EntityID != want[i] {
				t.Fatalf("iter %d: entities[%d] = %q, want %q (full order: %v)",
					iter, i, e.EntityID, want[i], entityIDs(desc.Entities))
			}
		}
	}
}

func entityIDs(entities []model.Entity) []string {
	out := make([]string, 0, len(entities))
	for _, e := range entities {
		out = append(out, e.EntityID)
	}
	return out
}

func TestExternalInstanceConfigCarriesLocalBinding(t *testing.T) {
	cfg := device.Config{
		ID:    "stcb-1",
		Name:  "STC-B Board",
		Port:  "COM3",
		Baud:  9600,
		Extra: map[string]string{"poll_interval_s": "3"},
	}
	raw, err := externalInstanceConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["device_id"] != "stcb-1" || got["port"] != "COM3" {
		t.Fatalf("config = %s", raw)
	}
	extra, _ := got["extra"].(map[string]any)
	if extra["poll_interval_s"] != "3" {
		t.Fatalf("extra = %v", got["extra"])
	}
}

func TestExternalConnectionHintsCarryPerDeviceBinding(t *testing.T) {
	cfg := device.Config{ID: "board-2", Name: "Second board", Port: "COM4", Baud: 115200, Extra: map[string]string{"protocol": "v1"}}
	got := externalConnectionHints(cfg)
	if got["port"] != "COM4" || got["baud"] != "115200" || got["name"] != "Second board" || got["protocol"] != "v1" {
		t.Fatalf("hints=%v", got)
	}
}

func TestExternalDeviceInstanceIDIsPerDevice(t *testing.T) {
	if got := externalDeviceInstanceID("stcb", "board-2"); got != "stcb/board-2" {
		t.Fatalf("instance id=%q", got)
	}
}
