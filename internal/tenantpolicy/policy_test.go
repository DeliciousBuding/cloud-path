package tenantpolicy

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDefaultPolicyValid(t *testing.T) {
	if err := Defaults().Validate(); err != nil {
		t.Fatalf("defaults invalid: %v", err)
	}
}

func TestPolicyRejectsUnlimitedOrInvalid(t *testing.T) {
	p := Defaults()
	p.Quotas.Devices = 0
	if !errors.Is(p.Validate(), ErrInvalidPolicy) {
		t.Fatal("zero device quota must be rejected")
	}
	p = Defaults()
	p.Retention.Audit = 1
	if !errors.Is(p.Validate(), ErrInvalidPolicy) {
		t.Fatal("audit retention below 7 days must be rejected")
	}
}

func TestEnforcerRejectsAtLimit(t *testing.T) {
	counter := NewCounter()
	release, err := counter.Acquire("tenant-a", ResourceEdges, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	_, err = counter.Acquire("tenant-a", ResourceEdges, 1)
	var quotaErr *QuotaError
	if !errors.As(err, &quotaErr) || quotaErr.Code() != "quota_edges" {
		t.Fatalf("err=%v, want quota_edges", err)
	}
	if _, err := counter.Acquire("tenant-b", ResourceEdges, 1); err != nil {
		t.Fatalf("tenant-b must have independent quota: %v", err)
	}
}

func TestCounterReleaseIsIdempotent(t *testing.T) {
	counter := NewCounter()
	release, err := counter.Acquire("tenant-a", ResourceBrowserWS, 1)
	if err != nil {
		t.Fatal(err)
	}
	release()
	release()
	if got := counter.Usage("tenant-a", ResourceBrowserWS); got != 0 {
		t.Fatalf("usage=%d, want 0", got)
	}
}

func TestEnforcerConcurrentAtomic(t *testing.T) {
	counter := NewCounter()
	const limit = 20
	var admitted atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := counter.Acquire("tenant-a", ResourceDevices, limit); err == nil {
				admitted.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if got := admitted.Load(); got != limit {
		t.Fatalf("admitted=%d, want exactly %d", got, limit)
	}
	if got := counter.Usage("tenant-a", ResourceDevices); got != limit {
		t.Fatalf("usage=%d, want %d", got, limit)
	}
}

func TestWindowLimiterTenantIsolationAndReset(t *testing.T) {
	now := time.Unix(1000, 0)
	limiter := NewWindowLimiter(time.Minute, func() time.Time { return now })
	if err := limiter.Allow("tenant-a", ResourceEventsPerMin, 1); err != nil {
		t.Fatal(err)
	}
	if err := limiter.Allow("tenant-a", ResourceEventsPerMin, 1); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("second event err=%v, want quota", err)
	}
	if err := limiter.Allow("tenant-b", ResourceEventsPerMin, 1); err != nil {
		t.Fatalf("tenant-b must be independent: %v", err)
	}
	now = now.Add(time.Minute)
	if err := limiter.Allow("tenant-a", ResourceEventsPerMin, 1); err != nil {
		t.Fatalf("new window must reset quota: %v", err)
	}
}
