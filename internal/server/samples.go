package server

import (
	"encoding/json"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/auth"
	"github.com/DeliciousBuding/cloud-path/internal/model"
	"github.com/DeliciousBuding/cloud-path/internal/store"
)

// observationSamplesFromState 把一次 state 上报归一为通用数值采样。
//
// raw 字段只按“顶层数值”保存，不推断实体/业务语义；typed observation 用
// "<entity_id>.<property>" 作为稳定键。两类键冲突时 typed observation 优先。
// 同一设备、同一序列、同一秒最多一条，由 store 的主键与 upsert 保证。
func observationSamplesFromState(st *api.StateData, fallbackTS int64) []store.ObservationSampleInput {
	if st == nil {
		return nil
	}
	ts := st.UpdatedAt
	if ts <= 0 {
		ts = fallbackTS
	}
	if ts <= 0 {
		ts = time.Now().Unix()
	}
	quality := "good"
	if !st.Online {
		quality = "unavailable"
	}
	byKey := make(map[string]store.ObservationSampleInput)
	for rawKey, raw := range st.Raw {
		key := strings.TrimSpace(rawKey)
		value, ok := numericValue(raw)
		if !ok || key == "" {
			continue
		}
		byKey[key] = store.ObservationSampleInput{Key: key, TS: ts, Value: value, Quality: quality}
	}
	for _, set := range st.Observations {
		entityID := strings.TrimSpace(set.EntityID)
		if entityID == "" {
			continue
		}
		for rawProperty, obs := range set.Observations {
			property := strings.TrimSpace(rawProperty)
			value, ok := numericValue(obs.Value)
			if !ok || property == "" {
				continue
			}
			sampleTS := observationTimestamp(obs, ts)
			q := strings.TrimSpace(string(obs.Quality))
			if q == "" {
				q = "good"
			}
			key := entityID + "." + property
			byKey[key] = store.ObservationSampleInput{Key: key, TS: sampleTS, Value: value, Quality: q}
		}
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]store.ObservationSampleInput, 0, len(keys))
	for _, key := range keys {
		out = append(out, byKey[key])
	}
	return out
}

func observationTimestamp(obs model.Observation, fallback int64) int64 {
	if !obs.ObservedAt.IsZero() {
		return obs.ObservedAt.Unix()
	}
	if !obs.ReceivedAt.IsZero() {
		return obs.ReceivedAt.Unix()
	}
	return fallback
}

func numericValue(v any) (float64, bool) {
	var f float64
	switch n := v.(type) {
	case float64:
		f = n
	case float32:
		f = float64(n)
	case int:
		f = float64(n)
	case int8:
		f = float64(n)
	case int16:
		f = float64(n)
	case int32:
		f = float64(n)
	case int64:
		f = float64(n)
	case uint:
		f = float64(n)
	case uint8:
		f = float64(n)
	case uint16:
		f = float64(n)
	case uint32:
		f = float64(n)
	case uint64:
		f = float64(n)
	case json.Number:
		parsed, err := n.Float64()
		if err != nil {
			return 0, false
		}
		f = parsed
	default:
		return 0, false
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

// persistObservationSamples 在 state 落库后写数值历史。失败只记日志，不阻断实时状态。
func (s *Server) persistObservationSamples(tenantID int64, deviceID string, st *api.StateData, fallbackTS int64) {
	if s.cfg.Store == nil {
		return
	}
	samples := observationSamplesFromState(st, fallbackTS)
	if len(samples) == 0 {
		return
	}
	if err := s.cfg.Store.AddObservationSamplesTenant(tenantID, deviceID, samples); err != nil {
		slog.Warn("store observation samples", "err", err, "device", deviceID)
	}
}

// handleListSeriesSamples 返回设备某条数值序列的历史采样，供趋势详情页使用。
// 设备离线不影响读取：历史是 Server 侧持久事实，与实时通道解耦。
func (s *Server) handleListSeriesSamples(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	if key == "" {
		writeAPIError(w, r, http.StatusBadRequest, api.APIErrInvalidRequest, "key is required")
		return
	}
	deviceKey := api.DeviceKey(chi.URLParam(r, "edgeID"), chi.URLParam(r, "deviceID"))
	s.mu.RLock()
	_, ok := s.devices[deviceKey]
	allowed := s.allowedDescriptor(r, deviceKey)
	s.mu.RUnlock()
	if !ok || !allowed {
		writeAPIError(w, r, http.StatusNotFound, api.APIErrDeviceNotFound, "device not found", deviceErrorParams(r))
		return
	}
	if s.cfg.Store == nil {
		writeJSON(w, http.StatusOK, api.SeriesSamplesView{Samples: []api.SeriesSampleView{}})
		return
	}

	p := auth.FromContext(r.Context())
	var tenantID int64
	if p != nil {
		tenantID = p.TenantID
	}
	from := queryInt64(r, "from")
	to := queryInt64(r, "to")
	before := queryInt64(r, "before")
	limit := queryInt(r, "limit", 1000, 5000)
	rows, nextBefore, err := s.cfg.Store.ListObservationSamples(tenantID, deviceKey, key, from, to, before, limit)
	if err != nil {
		slog.Warn("request failed", "err", err, "path", r.URL.Path)
		writeAPIError(w, r, http.StatusInternalServerError, api.APIErrInternal, "internal error")
		return
	}
	out := make([]api.SeriesSampleView, 0, len(rows))
	for _, row := range rows {
		out = append(out, api.SeriesSampleView{
			DeviceID: row.DeviceID,
			Key:      row.Key,
			Ts:       row.TS,
			Value:    row.Value,
			Quality:  row.Quality,
		})
	}
	writeJSON(w, http.StatusOK, api.SeriesSamplesView{Samples: out, NextBefore: nextBefore})
}

func queryInt64(r *http.Request, name string) int64 {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return 0
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
