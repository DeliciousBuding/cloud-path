package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func sampleCapability() Capability {
	return Capability{
		APIVersion: CapabilityAPIVersion,
		Kind:       CapabilityKind,
		Metadata: CapabilityMetadata{
			ID:      "cloudpath.dev/capability/temperature@1",
			Version: 1,
			Title:   "Temperature",
		},
		Spec: CapabilitySpec{
			Properties: map[string]Property{
				"value": {
					Type:    "number",
					Unit:    "Cel",
					Access:  PropertyRead,
					Quality: []Quality{QualityGood, QualityUncertain, QualityBad, QualityUnavailable},
				},
			},
			Events: map[string]EventDecl{
				"threshold-crossed": {PayloadSchema: map[string]any{
					"type":     "object",
					"required": []any{"value", "threshold", "direction"},
				}},
			},
			Actions: map[string]ActionDecl{
				"calibrate": {InputSchema: map[string]any{
					"type":     "object",
					"required": []any{"offset"},
				}},
			},
			Presentation: map[string]any{
				"primaryProperty": "value",
				"defaultWidget":   "gauge",
			},
		},
	}
}

func TestCapabilityValidation(t *testing.T) {
	t.Run("roundtrip", func(t *testing.T) {
		c := sampleCapability()
		if err := c.Validate(); err != nil {
			t.Fatalf("sample capability should be valid: %v", err)
		}
		b, err := json.Marshal(c)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var got Capability
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
			t.Fatalf("unmarshaled capability should be valid: %v", err)
		}
	})

	tests := []struct {
		name    string
		mutate  func(*Capability)
		wantErr string
	}{
		{"valid", func(*Capability) {}, ""},
		{"wrong apiVersion", func(c *Capability) { c.APIVersion = "capabilities.cloudpath.dev/v2" }, "apiVersion"},
		{"wrong kind", func(c *Capability) { c.Kind = "CapabilitySet" }, "kind"},
		{"missing id", func(c *Capability) { c.Metadata.ID = "" }, "metadata.id"},
		{"id missing version suffix", func(c *Capability) { c.Metadata.ID = "cloudpath.dev/capability/temperature" }, "metadata.id"},
		{"id missing publisher", func(c *Capability) { c.Metadata.ID = "/capability/temperature@1" }, "metadata.id"},
		{"id missing name", func(c *Capability) { c.Metadata.ID = "cloudpath.dev/capability/@1" }, "metadata.id"},
		{"negative version", func(c *Capability) { c.Metadata.Version = -1 }, "metadata.version"},
		{"invalid access", func(c *Capability) {
			p := c.Spec.Properties["value"]
			p.Access = "nope"
			c.Spec.Properties["value"] = p
		}, "access"},
		{"invalid quality", func(c *Capability) {
			p := c.Spec.Properties["value"]
			p.Quality = []Quality{"fuzzy"}
			c.Spec.Properties["value"] = p
		}, "quality"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := sampleCapability()
			tt.mutate(&c)
			err := c.Validate()
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
