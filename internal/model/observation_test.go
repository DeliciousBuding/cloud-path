package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestObservationQuality(t *testing.T) {
	t.Run("valid qualities", func(t *testing.T) {
		for _, q := range []Quality{QualityGood, QualityUncertain, QualityBad, QualityUnavailable} {
			o := Observation{
				Capability: "cloudpath.dev/capability/temperature@1",
				Property:   "value",
				Value:      24.7,
				Unit:       "Cel",
				Quality:    q,
				ObservedAt: time.Date(2026, 9, 3, 8, 0, 0, 0, time.UTC),
				ReceivedAt: time.Date(2026, 9, 3, 8, 0, 1, 0, time.UTC),
				Sequence:   42,
			}
			if err := o.Validate(); err != nil {
				t.Fatalf("quality %q should be valid: %v", q, err)
			}
			if !q.Valid() {
				t.Fatalf("quality %q should report Valid()", q)
			}
		}
	})

	t.Run("invalid quality", func(t *testing.T) {
		if Quality("fuzzy").Valid() {
			t.Fatal("unknown quality must not be valid")
		}
		o := Observation{
			Capability: "cloudpath.dev/capability/temperature@1",
			Property:   "value",
			Value:      24.7,
			Quality:    "fuzzy",
		}
		err := o.Validate()
		if err == nil || !strings.Contains(err.Error(), "quality") {
			t.Fatalf("expected quality error, got %v", err)
		}
	})

	t.Run("required fields", func(t *testing.T) {
		tests := []struct {
			name    string
			mutate  func(*Observation)
			wantErr string
		}{
			{"missing capability", func(o *Observation) { o.Capability = "" }, "capability"},
			{"missing property", func(o *Observation) { o.Property = "" }, "property"},
			{"missing value", func(o *Observation) { o.Value = nil }, "value"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				o := Observation{
					Capability: "cloudpath.dev/capability/temperature@1",
					Property:   "value",
					Value:      24.7,
				}
				tt.mutate(&o)
				err := o.Validate()
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
			})
		}
	})

	t.Run("json roundtrip", func(t *testing.T) {
		o := Observation{
			EntityID:   "stcb-001/temperature",
			Capability: "cloudpath.dev/capability/temperature@1",
			Property:   "value",
			Value:      24.7,
			Unit:       "Cel",
			Quality:    QualityUncertain,
			ObservedAt: time.Date(2026, 9, 3, 8, 0, 0, 0, time.UTC),
			ReceivedAt: time.Date(2026, 9, 3, 8, 0, 1, 0, time.UTC),
			Sequence:   42,
		}
		b, err := json.Marshal(o)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var got Observation
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
		if got.Quality != QualityUncertain || got.Sequence != 42 || got.Value != 24.7 {
			t.Fatalf("field mismatch: %+v", got)
		}
	})

	t.Run("bad datetime rejected at decode", func(t *testing.T) {
		bad := `{"capability":"cloudpath.dev/capability/temperature@1","property":"value","value":1,"observed_at":"not-a-time"}`
		var o Observation
		if err := json.Unmarshal([]byte(bad), &o); err == nil {
			t.Fatal("expected RFC3339 decode error, got nil")
		}
	})
}
