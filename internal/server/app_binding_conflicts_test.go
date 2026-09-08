package server

import (
	"strings"
	"testing"

	"github.com/DeliciousBuding/cloud-path/internal/api"
	coreapplication "github.com/DeliciousBuding/cloud-path/internal/application"
	"github.com/DeliciousBuding/cloud-path/internal/store"
)

func TestBindingConflictsExclusiveActuatorAcrossInstances(t *testing.T) {
	h := &AppHost{running: map[appInstKey]*appInstanceRun{
		{1, "box-prod"}: {
			row: store.PluginInstanceRow{TenantID: 1, InstanceID: "box-prod"},
			bindings: []api.AppBindingView{{
				RequirementID: "reminder-output",
				Capability:    "cloudpath.dev/capability/buzzer@1",
				EntityID:      "buzzer",
			}},
		},
	}}
	conflicts := h.bindingConflicts(1, "music", []coreapplication.Binding{{
		RequirementID: "sound", EntityID: "buzzer",
	}}, map[string]string{"sound": "cloudpath.dev/capability/buzzer@1"})
	if len(conflicts) != 1 || !strings.Contains(conflicts[0], "box-prod") {
		t.Fatalf("conflicts = %v, want box-prod conflict", conflicts)
	}
	if err := bindingConflictError(conflicts); err == nil {
		t.Fatal("bindingConflictError returned nil")
	}
}

func TestBindingConflictsAllowSharedSensorsAndDifferentTenants(t *testing.T) {
	h := &AppHost{running: map[appInstKey]*appInstanceRun{
		{1, "env-a"}: {
			row: store.PluginInstanceRow{TenantID: 1, InstanceID: "env-a"},
			bindings: []api.AppBindingView{{
				RequirementID: "temperature",
				Capability:    "cloudpath.dev/capability/temperature@1",
				EntityID:      "temperature",
			}},
		},
		{2, "box-b"}: {
			row: store.PluginInstanceRow{TenantID: 2, InstanceID: "box-b"},
			bindings: []api.AppBindingView{{
				RequirementID: "reminder-output",
				Capability:    "cloudpath.dev/capability/buzzer@1",
				EntityID:      "buzzer",
			}},
		},
	}}
	if got := h.bindingConflicts(1, "env-b", []coreapplication.Binding{{
		RequirementID: "temperature", EntityID: "temperature",
	}}, map[string]string{"temperature": "cloudpath.dev/capability/temperature@1"}); len(got) != 0 {
		t.Fatalf("shared sensor conflicts = %v, want none", got)
	}
	if got := h.bindingConflicts(1, "music", []coreapplication.Binding{{
		RequirementID: "sound", EntityID: "buzzer",
	}}, map[string]string{"sound": "cloudpath.dev/capability/buzzer@1"}); len(got) != 0 {
		t.Fatalf("cross-tenant conflicts = %v, want none", got)
	}
}

func TestBindingConflictsIgnoreSelf(t *testing.T) {
	h := &AppHost{running: map[appInstKey]*appInstanceRun{
		{1, "box-prod"}: {
			row: store.PluginInstanceRow{TenantID: 1, InstanceID: "box-prod"},
			bindings: []api.AppBindingView{{
				RequirementID: "reminder-output",
				Capability:    "cloudpath.dev/capability/buzzer@1",
				EntityID:      "buzzer",
			}},
		},
	}}
	if got := h.bindingConflicts(1, "box-prod", []coreapplication.Binding{{
		RequirementID: "reminder-output", EntityID: "buzzer",
	}}, map[string]string{"reminder-output": "cloudpath.dev/capability/buzzer@1"}); len(got) != 0 {
		t.Fatalf("self conflicts = %v, want none", got)
	}
}
