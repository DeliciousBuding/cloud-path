package pluginhost

import (
	"context"
	"time"
)

// Metrics is a factual point-in-time snapshot of one plugin instance. Fields
// the current platform cannot observe are reported as unavailable rather than
// fabricated.
type Metrics struct {
	// CPUTime is total CPU time consumed by the plugin process.
	CPUTime time.Duration
	// RSSBytes is resident set size in bytes (0 when unavailable).
	RSSBytes int64
	// Handles is the open handle count, or -1 when unavailable.
	Handles int
	// Goroutines is the live goroutine count, or -1 when unavailable.
	Goroutines int
	// MessageRate is messages per second over the observation window.
	MessageRate float64
	// RestartCount is the process restart count maintained by the Manager.
	RestartCount int
	// LastHealthy is the last time the health loop observed HEALTHY (zero when
	// never observed).
	LastHealthy time.Time
}

// MetricsTarget identifies the plugin instance whose metrics are collected.
type MetricsTarget struct {
	PluginID   string
	Version    string
	Tenant     string
	InstanceID string
}

// MetricsCollector observes process-level resource metrics for one instance.
// Implementations must return quickly and may report unavailable fields.
type MetricsCollector interface {
	Collect(ctx context.Context, target MetricsTarget) (Metrics, error)
}

// noopMetricsCollector is the default collector. It reports resource fields as
// unavailable rather than guessing, which keeps Metrics strictly factual.
type noopMetricsCollector struct{}

func (noopMetricsCollector) Collect(ctx context.Context, target MetricsTarget) (Metrics, error) {
	return Metrics{Handles: -1, Goroutines: -1}, nil
}
