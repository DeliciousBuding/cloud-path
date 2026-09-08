package server

import (
	"fmt"
	"sort"
	"strings"

	coreapplication "github.com/DeliciousBuding/cloud-path/internal/application"
)

// exclusiveBindingCapabilities are actuator capabilities that have one physical
// effect surface. Multiple applications may observe a sensor/key, but two
// applications must not concurrently drive the same buzzer/LED/display/motor
// without an explicit future sharing policy. This is a capability-level rule,
// not a device- or Driver-specific special case.
var exclusiveBindingCapabilities = map[string]bool{
	"cloudpath.dev/capability/buzzer@1":                    true,
	"cloudpath.dev/capability/led@1":                       true,
	"cloudpath.dev/capability/display-text@1":              true,
	"cloudpath.dev/capability/motor@1":                     true,
	"io.github.deliciousbuding/capability/actuator-show@1": true,
}

// bindingConflicts returns human-readable conflicts for bindings that would
// drive an exclusive actuator already owned by another running application in
// the same tenant. It is intentionally evaluated at runtime, after the plugin
// has declared its requirements and the Binder has resolved stable entities.
func (h *AppHost) bindingConflicts(tenantID int64, selfInstanceID string, bindings []coreapplication.Binding, reqCap map[string]string) []string {
	if h == nil || len(bindings) == 0 {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	var conflicts []string
	for _, candidate := range bindings {
		capability := reqCap[candidate.RequirementID]
		if !exclusiveBindingCapabilities[capability] {
			continue
		}
		for key, run := range h.running {
			if key.tenantID != tenantID || key.instanceID == selfInstanceID {
				continue
			}
			for _, existing := range run.bindings {
				if existing.EntityID == candidate.EntityID && existing.Capability == capability {
					conflicts = append(conflicts, fmt.Sprintf(
						"entity %q capability %q is already bound by instance %q (requirement %q)",
						candidate.EntityID, capability, key.instanceID, existing.RequirementID))
				}
			}
		}
	}
	sort.Strings(conflicts)
	return conflicts
}

func bindingConflictError(conflicts []string) error {
	if len(conflicts) == 0 {
		return nil
	}
	return fmt.Errorf("exclusive actuator binding conflict: %s", strings.Join(conflicts, "; "))
}
