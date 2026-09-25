package agentproto

import (
	"errors"
	"regexp"
)

// The edge cache (MH-576 D8): a Varnish container the agent owns, one per server, sitting between
// Caddy and every cache-enabled app's upstream. Caddy's own release (a plain tarball, see
// caddy.install — not built with xcaddy) has no cache handler module, so Varnish is the only option
// that works with the install this agent already has. See internal/agent/cache.go for the design
// note on why Varnish never needs reconfiguring across a deploy's ephemeral upstream port.

// KindCacheSet turns an app's edge cache on or off (and changes its settings while it is on).
const KindCacheSet = "cache.set"

// KindCachePurge empties the edge cache of one app's hostnames (a ban per hostname).
const KindCachePurge = "cache.purge"

// cacheBypassPathRE / cacheBypassCookieRE: kept deliberately narrow. These strings are interpolated
// into generated VCL (a program Varnish compiles and runs); anything a quote or a newline could
// reach is refused rather than escaped.
var (
	cacheBypassPathRE   = regexp.MustCompile(`^/[A-Za-z0-9/_.\-]*$`)
	cacheBypassCookieRE = regexp.MustCompile(`^[A-Za-z0-9_\-]+$`)
)

// MaxCacheBypassPaths / MaxCacheBypassCookies bound how much VCL one app's settings can generate.
const (
	MaxCacheBypassPaths   = 20
	MaxCacheBypassCookies = 20
)

// CacheParams: cache.set. The app must already have a route (route.set) — the agent looks its
// hosts and upstream up in the route table rather than being told them again.
type CacheParams struct {
	AppID   string `json:"app_id"`
	Enabled bool   `json:"enabled"`
	// TTLSeconds is how long a cacheable response is served from Varnish before it is refetched.
	TTLSeconds int64 `json:"ttl_seconds"`
	// BypassPaths: a request whose path starts with one of these is never cached (e.g. /admin, /api).
	BypassPaths []string `json:"bypass_paths,omitempty"`
	// BypassCookies: a request carrying any of these cookies is never cached (e.g. session).
	BypassCookies []string `json:"bypass_cookies,omitempty"`
}

// ValidCacheBypassPath reports whether s is narrow enough to interpolate into a VCL regex. Exported
// so both sides of the wire (this Validate, and the agent's renderVCL, which re-checks defensively
// right before it writes VCL to disk) use exactly one definition of "safe".
func ValidCacheBypassPath(s string) bool { return cacheBypassPathRE.MatchString(s) }

// ValidCacheBypassCookie reports whether s is narrow enough to interpolate into a VCL regex.
func ValidCacheBypassCookie(s string) bool { return cacheBypassCookieRE.MatchString(s) }

// Validate checks a cache.set on both sides of the wire.
func (p CacheParams) Validate() error {
	if !ValidID(p.AppID) {
		return errors.New("bad app id")
	}
	if !p.Enabled {
		return nil
	}
	if p.TTLSeconds < 1 || p.TTLSeconds > 86400 {
		return errors.New("ttl_seconds must be between 1 and 86400")
	}
	if len(p.BypassPaths) > MaxCacheBypassPaths {
		return errors.New("too many bypass paths")
	}
	for _, path := range p.BypassPaths {
		if !ValidCacheBypassPath(path) {
			return errors.New("bypass paths must start with / and hold only letters, numbers, - _ . /")
		}
	}
	if len(p.BypassCookies) > MaxCacheBypassCookies {
		return errors.New("too many bypass cookies")
	}
	for _, c := range p.BypassCookies {
		if !ValidCacheBypassCookie(c) {
			return errors.New("bypass cookie names must hold only letters, numbers, - and _")
		}
	}
	return nil
}

// CachePurgeParams: cache.purge.
type CachePurgeParams struct {
	AppID string `json:"app_id"`
}

// Validate checks a cache.purge on both sides of the wire.
func (p CachePurgeParams) Validate() error {
	if !ValidID(p.AppID) {
		return errors.New("bad app id")
	}
	return nil
}

// CacheStats is a cheap, opportunistic reading off varnishstat — not a live counter, refreshed
// whenever cache.set or cache.purge runs.
type CacheStats struct {
	Hits   int64 `json:"hits"`
	Misses int64 `json:"misses"`
}
