package saasify

import (
	"sync"
	"time"

	"github.com/fadymondy/hosbah/internal/tokens"
)

// HashForRateLimit buckets the rate limiter by a token's hash, never its plaintext (the same
// value ResolveKey looks up by, so a leaked bucket key is no more sensitive than the stored hash).
func HashForRateLimit(plainToken string) string { return tokens.Hash(plainToken) }

// keyRateLimiterMax/Window bound how many instance-lifecycle calls one product key may make —
// the same sliding-window shape as internal/rum and internal/apm's own ingest limiters, sized
// for a billing/signup system driving provisioning, not a request storm.
const (
	keyRateLimiterMax    = 120
	keyRateLimiterWindow = time.Minute
)

type rateLimiter struct {
	mu     sync.Mutex
	hits   map[string][]time.Time
	max    int
	window time.Duration
}

// KeyRateLimiter is the shared limiter for the public product-key endpoint, one bucket per key hash.
var KeyRateLimiter = &rateLimiter{hits: map[string][]time.Time{}, max: keyRateLimiterMax, window: keyRateLimiterWindow}

// Allow records one hit for key and reports whether it is within the limit.
func (l *rateLimiter) Allow(key string) bool {
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
