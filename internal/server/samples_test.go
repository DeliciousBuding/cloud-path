package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/model"
	"github.com/DeliciousBuding/cloud-path/internal/store"
)

func TestObservationSamplesFromState(t *testing.T) {
	observedAt := time.Unix(90, 0).UTC()
	samples := observationSamplesFromState(&api.StateData{
		Online:    true,
		UpdatedAt: 100,
		Raw: map[string]any{
			"temperature": 21.5,
			"label":       "ignored",
			"nested":      map[string]any{"x": 1},
		},
		Observations: []api.EntityObservationSet{{
			EntityID: "sensor",
			Observations: map[string]model.Observation{
				"temperature": {
					Value: 22.5, Quality: model.QualityBad, ObservedAt: observedAt,
				},
			},
		}},
	}, 0)
	byKey := map[string]store.ObservationSampleInput{}
	for _, sample := range samples {
		byKey[sample.Key] = sample
	}
	if len(byKey) != 2 {
		t.Fatalf("samples = %+v", samples)
	}
	if byKey["temperature"].Value != 21.5 || byKey["temperature"].TS != 100 || byKey["temperature"].Quality != "good" {
		t.Fatalf("raw sample = %+v", byKey["temperature"])
	}
	if byKey["sensor.temperature"].Value != 22.5 || byKey["sensor.temperature"].TS != 90 || byKey["sensor.temperature"].Quality != "bad" {
		t.Fatalf("typed sample = %+v", byKey["sensor.temperature"])
	}
}

func TestSeriesSamplesEndpointReadsOfflineHistory(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/samples.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.UpsertDevice("e1/d1", "e1", "stcb", "d1", "COM3"); err != nil {
		t.Fatal(err)
	}
	if err := st.AddObservationSamplesTenant(0, "e1/d1", []store.ObservationSampleInput{
		{Key: "temperature", TS: 10, Value: 1, Quality: "good"},
		{Key: "temperature", TS: 20, Value: 2, Quality: "good"},
		{Key: "temperature", TS: 30, Value: 3, Quality: "good"},
	}); err != nil {
		t.Fatal(err)
	}
	srv := New(Config{Store: st})
	if srv.devices["e1/d1"].Online {
		t.Fatal("hydrated device must be offline")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/devices/e1/d1/samples?key=temperature&limit=2", nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got api.SeriesSamplesView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Samples) != 2 || got.Samples[0].Ts != 20 || got.Samples[1].Ts != 30 || got.NextBefore != 20 {
		t.Fatalf("response = %+v", got)
	}
	if got.Samples[1].Value != 3 || got.Samples[1].Quality != "good" {
		t.Fatalf("sample = %+v", got.Samples[1])
	}
}

func TestSeriesSamplesEndpointRequiresKey(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/samples-key.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.UpsertDevice("e1/d1", "e1", "stcb", "d1", "COM3"); err != nil {
		t.Fatal(err)
	}
	srv := New(Config{Store: st})
	req := httptest.NewRequest(http.MethodGet, "/api/devices/e1/d1/samples", nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body api.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != api.APIErrInvalidRequest {
		t.Fatalf("code = %q", body.Code)
	}
}
