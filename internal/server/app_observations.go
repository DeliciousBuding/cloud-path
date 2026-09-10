package server

import (
	"encoding/json"
	"time"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	"github.com/DeliciousBuding/cloud-path/internal/model"
	sdkapplication "github.com/DeliciousBuding/cloud-path/sdk/go/cloudpath/v1/application"
)

type observationDelivery struct {
	route routedEvent
	event *sdkapplication.CapabilityEvent
}

// observationDeliveries validates the device/descriptor boundary before routing
// samples. A state packet cannot impersonate an entity on another device, even
// when the sending edge owns both devices. Legacy raw maps are never inferred.
func (h *AppHost) observationDeliveries(tenantID int64, deviceKey string, online bool, sets []api.EntityObservationSet) []observationDelivery {
	if h == nil || h.srv == nil || len(sets) == 0 {
		return nil
	}
	declared := map[string]map[string]bool{}
	h.srv.mu.Lock()
	if desc, ok := h.srv.descriptors[deviceKey]; ok {
		for _, entity := range desc.Entities {
			caps := map[string]bool{}
			for _, cap := range entity.Capabilities {
				caps[cap] = true
			}
			declared[entity.EntityID] = caps
		}
	}
	h.srv.mu.Unlock()
	var out []observationDelivery
	seen := map[string]bool{}
	for _, set := range sets {
		caps := declared[set.EntityID]
		if caps == nil {
			continue
		}
		for property, obs := range set.Observations {
			if property != obs.Property || !caps[obs.Capability] || obs.Validate() != nil {
				continue
			}
			dedup := set.EntityID + "\x00" + obs.Capability + "\x00" + property
			if seen[dedup] {
				continue
			}
			seen[dedup] = true
			if !online {
				obs.Quality = model.QualityUnavailable
			}
			data, err := json.Marshal(obs)
			if err != nil {
				continue
			}
			occurred := obs.ObservedAt
			if occurred.IsZero() {
				occurred = obs.ReceivedAt
			}
			if occurred.IsZero() {
				occurred = time.Now()
			}
			for _, route := range h.routeDeviceEvent(tenantID, deviceKey, set.EntityID) {
				// One entity may expose several capabilities; a binding only authorizes
				// the requirement's capability, not all of the entity's other properties.
				matches := false
				for _, binding := range route.run.bindings {
					if binding.EntityID == set.EntityID && binding.RequirementID == route.req && binding.Capability == obs.Capability {
						matches = true
						break
					}
				}
				if !matches {
					continue
				}
				out = append(out, observationDelivery{route: route, event: &sdkapplication.CapabilityEvent{
					RequirementID: route.req, EntityID: set.EntityID,
					EventType:   sdkapplication.PropertyObservedEvent,
					PayloadJSON: string(data), OccurredAt: occurred.UTC().Format(time.RFC3339Nano),
				}})
			}
		}
	}
	return out
}

// DispatchDeviceObservations delivers real property samples to bound apps.
// Values/quality/sampling timestamps remain intact; offline transport is never
// promoted to good data. No domain event, sensor name or driver id is assumed.
func (h *AppHost) DispatchDeviceObservations(tenantID int64, deviceKey string, online bool, sets []api.EntityObservationSet) {
	for _, delivery := range h.observationDeliveries(tenantID, deviceKey, online, sets) {
		r := delivery.route.run
		if err := h.rt.DispatchEvent(h.ctxOrBackground(), r.tenantStr, r.row.InstanceID, &sdkapplication.ApplicationEvent{Union: delivery.event}); err != nil {
			h.logger.Warn("apphost dispatch observation", "instance", r.row.InstanceID, "entity", delivery.event.EntityID, "err", err)
		}
	}
}
