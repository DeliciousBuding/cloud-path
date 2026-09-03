package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func sampleDescriptor() Descriptor {
	return Descriptor{
		DeviceID:     "stcb-001",
		ExternalID:   "serial/stcb-001",
		Manufacturer: "IAP",
		Model:        "STC-B",
		Status:       DeviceOnline,
		Entities: []Entity{
			{
				EntityID:     "stcb-001/compartment-1",
				UniqueKey:    "compartment.1",
				Name:         "Compartment 1",
				Category:     EntitySensor,
				Capabilities: []string{"cloudpath.dev/capability/contact@1"},
				Observations: map[string]Observation{
					"opened": {
						Capability: "cloudpath.dev/capability/contact@1",
						Property:   "opened",
						Value:      true,
						Unit:       "bool",
						Quality:    QualityGood,
						ObservedAt: time.Date(2026, 9, 3, 8, 0, 0, 0, time.UTC),
						ReceivedAt: time.Date(2026, 9, 3, 8, 0, 1, 0, time.UTC),
						Sequence:   42,
					},
				},
			},
			{
				EntityID:     "stcb-001/temperature",
				UniqueKey:    "temperature",
				Category:     EntitySensor,
				Capabilities: []string{"cloudpath.dev/capability/temperature@1"},
				Observations: map[string]Observation{
					"value": {
						Capability: "cloudpath.dev/capability/temperature@1",
						Property:   "value",
						Value:      24.7,
						Unit:       "Cel",
						Quality:    QualityGood,
						ObservedAt: time.Date(2026, 9, 3, 8, 0, 0, 0, time.UTC),
						ReceivedAt: time.Date(2026, 9, 3, 8, 0, 1, 0, time.UTC),
						Sequence:   7,
					},
				},
			},
		},
	}
}

func TestDescriptorRoundtrip(t *testing.T) {
	d := sampleDescriptor()
	if err := d.Validate(); err != nil {
		t.Fatalf("sample descriptor should be valid: %v", err)
	}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Descriptor
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	b2, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	if string(b) != string(b2) {
		t.Fatalf("roundtrip not stable:\nwant %s\ngot  %s", b, b2)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("unmarshaled descriptor should be valid: %v", err)
	}
	if got.DeviceID != d.DeviceID || got.Status != d.Status || len(got.Entities) != len(d.Entities) {
		t.Fatalf("top-level field mismatch: %+v", got)
	}
	obs := got.Entities[0].Observations["opened"]
	if obs.Quality != QualityGood || obs.Sequence != 42 ||
		!obs.ObservedAt.Equal(d.Entities[0].Observations["opened"].ObservedAt) ||
		!obs.ReceivedAt.Equal(d.Entities[0].Observations["opened"].ReceivedAt) {
		t.Fatalf("observation mismatch: %+v", obs)
	}
}

func TestDescriptorValidation(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Descriptor)
		wantErr string
	}{
		{"valid", func(*Descriptor) {}, ""},
		{"missing device_id", func(d *Descriptor) { d.DeviceID = "" }, "device_id"},
		{"missing external_id", func(d *Descriptor) { d.ExternalID = "" }, "external_id"},
		{"invalid status", func(d *Descriptor) { d.Status = "weird" }, "status"},
		{"missing entities", func(d *Descriptor) { d.Entities = nil }, "entities"},
		{"invalid category", func(d *Descriptor) { d.Entities[0].Category = "widget" }, "category"},
		{"empty entity_id", func(d *Descriptor) { d.Entities[0].EntityID = "" }, "entity_id"},
		{"empty unique_key", func(d *Descriptor) { d.Entities[0].UniqueKey = "" }, "unique_key"},
		{"empty capability ref", func(d *Descriptor) { d.Entities[0].Capabilities = []string{""} }, "capabilities"},
		{"invalid observation quality", func(d *Descriptor) {
			obs := d.Entities[0].Observations["opened"]
			obs.Quality = "fuzzy"
			d.Entities[0].Observations["opened"] = obs
		}, "quality"},
		{"duplicate entity id", func(d *Descriptor) { d.Entities[1].EntityID = d.Entities[0].EntityID }, "duplicate"},
		{"observation missing value", func(d *Descriptor) {
			obs := d.Entities[0].Observations["opened"]
			obs.Value = nil
			d.Entities[0].Observations["opened"] = obs
		}, "value"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := sampleDescriptor()
			tt.mutate(&d)
			err := d.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("expected valid, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}
