package pluginhost

import "context"

// Health is the coarse health grade reported by a HealthChecker for one plugin
// instance. It is deliberately separate from the Supervisor process State:
// a process can be alive (Healthy process state) while its protocol health is
// Degraded.
type Health uint8

const (
	HealthUnknown Health = iota
	HealthHealthy
	HealthDegraded
)

// String returns the canonical uppercase health name.
func (h Health) String() string {
	switch h {
	case HealthHealthy:
		return "HEALTHY"
	case HealthDegraded:
		return "DEGRADED"
	default:
		return "UNKNOWN"
	}
}

// HealthTarget identifies the plugin instance being probed.
type HealthTarget struct {
	PluginID   string
	Version    string
	Tenant     string
	InstanceID string
}

// HealthChecker probes one plugin instance. Implementations must return
// quickly and are called periodically by the Manager, outside the Manager
// lock.
type HealthChecker interface {
	Check(ctx context.Context, target HealthTarget) (Health, error)
}

// HealthFailurePolicy selects the Manager action once an instance accumulates
// HealthFailureThreshold consecutive failed probes.
type HealthFailurePolicy uint8

const (
	// HealthPolicyDisable stops the process. It is the default and does not
	// touch the Supervisor crash budget.
	HealthPolicyDisable HealthFailurePolicy = iota
	// HealthPolicyRestart performs a supervised in-place restart without
	// incrementing the Supervisor crash/restart budget.
	HealthPolicyRestart
)

// String returns a stable, human-readable policy name.
func (p HealthFailurePolicy) String() string {
	switch p {
	case HealthPolicyRestart:
		return "restart"
	default:
		return "disable"
	}
}
