package auth

import (
	"sync"
	"time"
)

// RateLimiter 是滑动窗口限流器（登录限流：默认 5 次/分/IP）。
type RateLimiter struct {
	mu     sync.Mutex
	per    int
	window time.Duration
	hits   map[string][]time.Time
}

// NewRateLimiter 构造限流器；per<=0 归一为 1。
func NewRateLimiter(per int) *RateLimiter {
	if per <= 0 {
		per = 1
	}
	return &RateLimiter{per: per, window: time.Minute, hits: map[string][]time.Time{}}
}

// Allow 记录一次命中并判定是否放行。放行返回 (true, 0)；超限返回 (false, retryAfter)。
// 命中序列单调递增，kept[0] 即窗口内最早命中。
func (l *RateLimiter) Allow(key string) (bool, time.Duration) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	window := l.hits[key]
	kept := window[:0]
	for _, t := range window {
		if now.Sub(t) < l.window {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.per {
		l.hits[key] = kept
		retry := kept[0].Add(l.window).Sub(now)
		if retry < time.Second {
			retry = time.Second
		}
		return false, retry
	}
	l.hits[key] = append(kept, now)
	return true, 0
}
