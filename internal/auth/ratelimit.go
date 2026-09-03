package auth

import (
	"sync"
	"time"
)

// defaultMaxRateLimitKeys 是登录限流器内存中的最大 key 数：攻击者伪造大量
// 来源地址时，map 收敛到该上界，防止内存无界增长。
const defaultMaxRateLimitKeys = 10000

// RateLimiter 是滑动窗口限流器（登录限流：默认 5 次/分/IP）。
// 除了每 key 滑窗外，还带 TTL 清理与最大容量驱逐。
type RateLimiter struct {
	mu      sync.Mutex
	per     int
	window  time.Duration
	maxKeys int
	hits    map[string][]time.Time
}

// NewRateLimiter 构造限流器；per<=0 归一为 1。可选第二个参数为最大 key 数。
func NewRateLimiter(per int, maxKeys ...int) *RateLimiter {
	if per <= 0 {
		per = 1
	}
	cap := defaultMaxRateLimitKeys
	if len(maxKeys) > 0 && maxKeys[0] > 0 {
		cap = maxKeys[0]
	}
	return &RateLimiter{per: per, window: time.Minute, maxKeys: cap, hits: map[string][]time.Time{}}
}

// Allow 记录一次命中并判定是否放行。放行返回 (true, 0)；超限返回 (false, retryAfter)。
// 命中序列单调递增，kept[0] 即窗口内最早命中。
func (l *RateLimiter) Allow(key string) (bool, time.Duration) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	l.pruneLocked(now)

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
	if _, exists := l.hits[key]; !exists {
		l.evictIfNeededLocked(key)
	}
	l.hits[key] = append(kept, now)
	return true, 0
}

// Len 返回当前内存中的 key 数（测试用）。
func (l *RateLimiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.hits)
}

// pruneLocked 删除已无窗口内命中的 key（TTL 清理）。调用方持锁。
func (l *RateLimiter) pruneLocked(now time.Time) {
	for k, window := range l.hits {
		fresh := false
		for _, t := range window {
			if now.Sub(t) < l.window {
				fresh = true
				break
			}
		}
		if !fresh {
			delete(l.hits, k)
		}
	}
}

// evictIfNeededLocked 在达到容量上界时驱逐窗口最早过期的旧 key，保证 key 数有界。
func (l *RateLimiter) evictIfNeededLocked(key string) {
	if len(l.hits) < l.maxKeys {
		return
	}
	var victim string
	var oldest time.Time
	for k, window := range l.hits {
		if k == key || len(window) == 0 {
			continue
		}
		if victim == "" || window[0].Before(oldest) {
			victim, oldest = k, window[0]
		}
	}
	if victim != "" {
		delete(l.hits, victim)
	}
}
