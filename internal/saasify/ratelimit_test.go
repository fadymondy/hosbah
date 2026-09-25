package saasify

import (
	"testing"
	"time"
)

// A fresh limiter allows up to max hits per key within the window, then refuses the next one —
// and never touches an unrelated key's bucket.
func TestRateLimiterAllowsUpToMaxThenRefuses(t *testing.T) {
	l := &rateLimiter{hits: map[string][]time.Time{}, max: 3, window: time.Minute}

	for i := 0; i < 3; i++ {
		if !l.Allow("k1") {
			t.Fatalf("hit %d: expected allowed within the limit", i+1)
		}
	}
	if l.Allow("k1") {
		t.Fatal("expected the 4th hit within the window to be refused")
	}
	if !l.Allow("k2") {
		t.Fatal("a different key must have its own, unaffected bucket")
	}
}

// A hit older than the window no longer counts against the limit.
func TestRateLimiterForgetsHitsOutsideTheWindow(t *testing.T) {
	l := &rateLimiter{hits: map[string][]time.Time{}, max: 1, window: time.Minute}
	l.hits["k"] = []time.Time{time.Now().Add(-2 * time.Minute)}

	if !l.Allow("k") {
		t.Fatal("a hit older than the window should not count against the limit")
	}
}

// HashForRateLimit buckets by the token's hash, not its plaintext, and is deterministic.
func TestHashForRateLimitIsDeterministicAndNotThePlaintext(t *testing.T) {
	tok := "hbk_abcdefghijklmnopqrstuvwxyz"
	h1, h2 := HashForRateLimit(tok), HashForRateLimit(tok)
	if h1 != h2 {
		t.Fatalf("HashForRateLimit is not deterministic: %s != %s", h1, h2)
	}
	if h1 == tok {
		t.Fatal("HashForRateLimit returned the plaintext token unchanged")
	}
	if HashForRateLimit("hbk_something-else") == h1 {
		t.Fatal("HashForRateLimit collided two different tokens")
	}
}
