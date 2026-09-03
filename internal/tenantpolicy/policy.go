// Package tenantpolicy defines CloudPath's v0.1 anti-abuse limits and
// tenant-scoped retention policy. It is a pure domain package; persistence and
// HTTP/WS wiring live in store/server.
package tenantpolicy

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrInvalidPolicy = errors.New("tenantpolicy: invalid policy")
	ErrQuotaExceeded = errors.New("tenantpolicy: quota exceeded")
)

// Resource is a stable machine identifier used by HTTP error codes and audit.
type Resource string

const (
	ResourceDevices         Resource = "devices"
	ResourceEdges           Resource = "edges"
	ResourceBrowserWS       Resource = "browser_ws"
	ResourceTokens          Resource = "tokens"
	ResourceUsers           Resource = "users"
	ResourceEventsPerMin    Resource = "events_per_min"
	ResourcePluginInstances Resource = "plugin_instances"
)

// RetentionDays contains resolved, non-null retention values.
type RetentionDays struct {
	Events             int
	TerminalCommands   int
	Audit              int
	RevokedTokens      int
	PluginObservations int
}

// Quotas contains resolved hard limits. Zero/unlimited is deliberately invalid.
type Quotas struct {
	Devices         int
	Edges           int
	BrowserWS       int
	Tokens          int
	Users           int
	EventsPerMinute int
	PluginInstances int
}

// Policy is the resolved policy used by runtime enforcement.
type Policy struct {
	Retention RetentionDays
	Quotas    Quotas
}

// Defaults returns the fail-safe v0.1 policy.
func Defaults() Policy {
	return Policy{
		Retention: RetentionDays{Events: 30, TerminalCommands: 30, Audit: 90, RevokedTokens: 7, PluginObservations: 30},
		Quotas:    Quotas{Devices: 200, Edges: 50, BrowserWS: 20, Tokens: 100, Users: 100, EventsPerMinute: 600, PluginInstances: 100},
	}
}

// Validate rejects unlimited, negative, or unreasonably large settings.
func (p Policy) Validate() error {
	for name, days := range map[string]int{
		"events": p.Retention.Events, "terminal_commands": p.Retention.TerminalCommands,
		"audit": p.Retention.Audit, "revoked_tokens": p.Retention.RevokedTokens,
		"plugin_observations": p.Retention.PluginObservations,
	} {
		min := 1
		if name == "audit" {
			min = 7
		}
		if days < min || days > 3650 {
			return fmt.Errorf("%w: retention %s=%d outside %d..3650", ErrInvalidPolicy, name, days, min)
		}
	}
	for resource, limit := range p.quotaMap() {
		if limit <= 0 || limit > 1_000_000 {
			return fmt.Errorf("%w: quota %s=%d outside 1..1000000", ErrInvalidPolicy, resource, limit)
		}
	}
	return nil
}

// Limit returns the resolved limit for resource.
func (p Policy) Limit(resource Resource) (int, error) {
	limit, ok := p.quotaMap()[resource]
	if !ok {
		return 0, fmt.Errorf("%w: unknown resource %q", ErrInvalidPolicy, resource)
	}
	if limit <= 0 {
		return 0, fmt.Errorf("%w: invalid limit for %s", ErrInvalidPolicy, resource)
	}
	return limit, nil
}

func (p Policy) quotaMap() map[Resource]int {
	return map[Resource]int{
		ResourceDevices: p.Quotas.Devices, ResourceEdges: p.Quotas.Edges,
		ResourceBrowserWS: p.Quotas.BrowserWS, ResourceTokens: p.Quotas.Tokens,
		ResourceUsers: p.Quotas.Users, ResourceEventsPerMin: p.Quotas.EventsPerMinute,
		ResourcePluginInstances: p.Quotas.PluginInstances,
	}
}

// QuotaError carries only non-sensitive usage facts.
type QuotaError struct {
	Tenant   string
	Resource Resource
	Limit    int
	Usage    int
}

func (e *QuotaError) Error() string {
	return fmt.Sprintf("%s: tenant %s resource %s usage %d limit %d", ErrQuotaExceeded, e.Tenant, e.Resource, e.Usage, e.Limit)
}
func (e *QuotaError) Unwrap() error { return ErrQuotaExceeded }
func (e *QuotaError) Code() string  { return "quota_" + string(e.Resource) }

// Counter atomically admits and releases concurrent resources such as Edge or
// browser WebSocket connections. A release function is idempotent.
type Counter struct {
	mu    sync.Mutex
	usage map[counterKey]int
}

type counterKey struct {
	tenant   string
	resource Resource
}

func NewCounter() *Counter { return &Counter{usage: make(map[counterKey]int)} }

func (c *Counter) Acquire(tenant string, resource Resource, limit int) (release func(), err error) {
	if tenant == "" || resource == "" || limit <= 0 {
		return nil, ErrInvalidPolicy
	}
	key := counterKey{tenant: tenant, resource: resource}
	c.mu.Lock()
	current := c.usage[key]
	if current >= limit {
		c.mu.Unlock()
		return nil, &QuotaError{Tenant: tenant, Resource: resource, Limit: limit, Usage: current}
	}
	c.usage[key] = current + 1
	c.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			if n := c.usage[key]; n <= 1 {
				delete(c.usage, key)
			} else {
				c.usage[key] = n - 1
			}
			c.mu.Unlock()
		})
	}, nil
}

func (c *Counter) Usage(tenant string, resource Resource) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.usage[counterKey{tenant: tenant, resource: resource}]
}

// WindowLimiter is an in-memory fixed-window limiter for high-rate resources
// such as events. It is tenant/resource scoped and uses an injectable clock.
type WindowLimiter struct {
	mu     sync.Mutex
	now    func() time.Time
	window time.Duration
	hits   map[counterKey]windowState
}

type windowState struct {
	start time.Time
	count int
}

func NewWindowLimiter(window time.Duration, now func() time.Time) *WindowLimiter {
	if window <= 0 {
		window = time.Minute
	}
	if now == nil {
		now = time.Now
	}
	return &WindowLimiter{now: now, window: window, hits: make(map[counterKey]windowState)}
}

func (l *WindowLimiter) Allow(tenant string, resource Resource, limit int) error {
	if tenant == "" || resource == "" || limit <= 0 {
		return ErrInvalidPolicy
	}
	key := counterKey{tenant: tenant, resource: resource}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	state := l.hits[key]
	if state.start.IsZero() || now.Sub(state.start) >= l.window || now.Before(state.start) {
		state = windowState{start: now}
	}
	if state.count >= limit {
		return &QuotaError{Tenant: tenant, Resource: resource, Limit: limit, Usage: state.count}
	}
	state.count++
	l.hits[key] = state
	return nil
}
