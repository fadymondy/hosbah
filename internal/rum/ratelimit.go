package rum

import (
	"sync"
	"time"
)

// rateLimiterMax and rateLimiterWindow bound how many samples one ingest key may submit — generous
// for a real visitor session (one beacon per page load), tight enough to blunt a script hammering a
// leaked (non-secret) key.
const (
	rateLimiterMax    = 120
	rateLimiterWindow = time.Minute
)

// rateLimiter is a per-key sliding window, the same shape as internal/account's login limiter.
type rateLimiter struct {
	mu     sync.Mutex
	hits   map[string][]time.Time
	max    int
	window time.Duration
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{hits: map[string][]time.Time{}, max: rateLimiterMax, window: rateLimiterWindow}
}

// allow records one hit for key and reports whether it is within the limit.
func (l *rateLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	cut := now.Add(-l.window)
	kept := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.max {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, now)
	if len(l.hits) > 50_000 {
		for k, ts := range l.hits {
			if len(ts) == 0 || ts[len(ts)-1].Before(cut) {
				delete(l.hits, k)
			}
		}
	}
	return true
}
