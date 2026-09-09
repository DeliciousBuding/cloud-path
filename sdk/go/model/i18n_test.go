package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestI18nJSONRoundTripAndLegacyCompatibility(t *testing.T) {
	entity := Entity{
		EntityID:     "entity-1",
		UniqueKey:    "temperature",
		Name:         "温度",
		I18n:         I18n{"zh-CN": "温度", "en-US": "Temperature"},
		Category:     EntitySensor,
		Capabilities: []string{"cloudpath.dev/capability/temperature@1"},
	}
	encoded, err := json.Marshal(entity)
	if err != nil {
		t.Fatalf("marshal entity: %v", err)
	}
	if !strings.Contains(string(encoded), `"i18n"`) {
		t.Fatalf("i18n field missing: %s", encoded)
	}
	var decoded Entity
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal entity: %v", err)
	}
	if decoded.I18n["en-US"] != "Temperature" {
		t.Fatalf("i18n mismatch: %+v", decoded.I18n)
	}

	legacy, err := json.Marshal(Entity{EntityID: "e", UniqueKey: "u", Category: EntitySensor})
	if err != nil {
		t.Fatalf("marshal legacy entity: %v", err)
	}
	if strings.Contains(string(legacy), "i18n") {
		t.Fatalf("legacy entity must not emit i18n: %s", legacy)
	}
}

func TestCapabilityI18nRoundTrip(t *testing.T) {
	capability := sampleCapability()
	capability.Metadata.I18n = I18n{"zh-CN": "温度", "en-US": "Temperature"}
	action := capability.Spec.Actions["calibrate"]
	action.Title = "标定"
	action.Description = "更新偏移量"
	action.I18n = I18n{
		"zh-CN":             "标定",
		"zh-CN.description": "更新偏移量",
		"en-US":             "Calibrate",
		"en-US.description": "Update the offset",
	}
	capability.Spec.Actions["calibrate"] = action

	encoded, err := json.Marshal(capability)
	if err != nil {
		t.Fatalf("marshal capability: %v", err)
	}
	var decoded Capability
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal capability: %v", err)
	}
	if decoded.Metadata.I18n["en-US"] != "Temperature" {
		t.Fatalf("metadata i18n mismatch: %+v", decoded.Metadata.I18n)
	}
	if decoded.Spec.Actions["calibrate"].I18n["en-US.description"] != "Update the offset" {
		t.Fatalf("action i18n mismatch: %+v", decoded.Spec.Actions["calibrate"].I18n)
	}
}
