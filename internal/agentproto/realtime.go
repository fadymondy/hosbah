package agentproto

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
)

/*
Realtime: a site's websocket server runs as one worker process of its release, and Caddy sends a
path of the site's hostnames to it.

  - app.start / app.rollback: StartParams.Realtime on ONE worker makes the agent publish that
    container port on 127.0.0.1 at a port the kernel picks — the code path web uses — and report
    it back as StartResult.HostPort.
  - route.set: RouteParams.Realtime puts a `path <path>*` route in front of the app's catch-all.
    route.set describes the app's whole route, so a route.set without Realtime removes it.
  - realtime.route: changes only the realtime part of an app's route, at once — removing it when
    the section is switched off, or pointing a changed path at the port that is already published.

Everything is validated on both sides of the wire: the process name, the port and the path.
*/

// KindRealtimeRoute adds, moves or removes the realtime route of an app that is already routed.
const KindRealtimeRoute = "realtime.route"

// MaxRealtimePath bounds a realtime path.
const MaxRealtimePath = 100

var (
	realtimeProcessRE = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,30}$`)
	realtimePathRE    = regexp.MustCompile(`^/[A-Za-z0-9._/-]*$`)
	localUpstreamRE   = regexp.MustCompile(`^127\.0\.0\.1:([1-9][0-9]{0,4})$`)
)

// ValidRealtimeProcess: the bare name of a worker ("reverb" is cloudy.toml's "worker:reverb"). A
// letter first, so it can never read as an option; "web" is not a worker.
func ValidRealtimeProcess(s string) bool { return realtimeProcessRE.MatchString(s) && s != "web" }

// RealtimeWorker is the process name the release knows the realtime process by.
func RealtimeWorker(process string) string { return "worker:" + process }

// CleanRealtimePath returns the path Caddy matches on: absolute, of [A-Za-z0-9._/-], normalised
// (so /ws/../admin is /admin, and is judged as that), without a trailing slash, at most
// MaxRealtimePath long. "/" — the whole site — is its own decision: see ValidRealtimePath.
func CleanRealtimePath(p string) (string, error) {
	if p == "" || !strings.HasPrefix(p, "/") {
		return "", errors.New("the path must start with /")
	}
	if len(p) > MaxRealtimePath {
		return "", fmt.Errorf("the path is at most %d characters", MaxRealtimePath)
	}
	if !realtimePathRE.MatchString(p) {
		return "", errors.New("the path may hold letters, digits, '.', '_', '-' and '/' only")
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." || seg == "." {
			return "", errors.New("the path must not contain . or .. segments")
		}
	}
	return path.Clean(p), nil
}

// ValidRealtimePath: p is already clean (what CleanRealtimePath returns). wholeSite admits "/".
func ValidRealtimePath(p string, wholeSite bool) error {
	clean, err := CleanRealtimePath(p)
	if err != nil {
		return err
	}
	if clean != p {
		return fmt.Errorf("the path is not normalised (%s)", clean)
	}
	if p == "/" && !wholeSite {
		return errors.New("the path / would send the whole site to the realtime process")
	}
	return nil
}

// RealtimePublish rides on the StartParams of the realtime worker: publish Port (what the process
// listens on inside its container) on an ephemeral 127.0.0.1 port. Path is what will be routed to
// it — carried so the agent validates the pair it is about to serve, and logs it.
type RealtimePublish struct {
	Port      int    `json:"port"`
	Path      string `json:"path"`
	WholeSite bool   `json:"whole_site,omitempty"` // Path is "/" on purpose (kind custom)
}

// Validate checks a RealtimePublish on both sides of the wire.
func (r RealtimePublish) Validate() error {
	if r.Port < 1 || r.Port > 65535 {
		return errors.New("the realtime port must be 1–65535")
	}
	return ValidRealtimePath(r.Path, r.WholeSite)
}

// RealtimeRoute is the realtime part of an app's route: requests of the app's hostnames whose path
// starts with Path go to Upstream (the realtime worker's published port) instead of web.
type RealtimeRoute struct {
	Path      string `json:"path"`
	Upstream  string `json:"upstream"` // 127.0.0.1:<port>
	WholeSite bool   `json:"whole_site,omitempty"`
}

// Validate checks a RealtimeRoute on both sides of the wire.
func (r RealtimeRoute) Validate() error {
	if !ValidLocalUpstream(r.Upstream) {
		return errors.New("the realtime upstream is a local port (127.0.0.1:<port>)")
	}
	return ValidRealtimePath(r.Path, r.WholeSite)
}

// ValidLocalUpstream: exactly 127.0.0.1:<1–65535>.
func ValidLocalUpstream(s string) bool {
	m := localUpstreamRE.FindStringSubmatch(s)
	if m == nil {
		return false
	}
	n, err := strconv.Atoi(m[1])
	return err == nil && n >= 1 && n <= 65535
}

// RealtimeRouteParams: realtime.route. Enabled false removes the realtime route of the app (the
// rest of its route stays); true sets it — the app must already be routed on this server.
type RealtimeRouteParams struct {
	AppID   string         `json:"app_id"`
	Enabled bool           `json:"enabled"`
	Route   *RealtimeRoute `json:"route,omitempty"`
}

// Validate checks a realtime.route on both sides of the wire.
func (p RealtimeRouteParams) Validate() error {
	if !ValidID(p.AppID) {
		return errors.New("bad app id")
	}
	if !p.Enabled {
		if p.Route != nil {
			return errors.New("a realtime.route that disables carries no route")
		}
		return nil
	}
	if p.Route == nil {
		return errors.New("a realtime.route that enables needs a route")
	}
	return p.Route.Validate()
}

// RealtimeRouteResult is what realtime.route answers with.
type RealtimeRouteResult struct {
	Routed bool `json:"routed"`
	Hosts  int  `json:"hosts"`
}

// RealtimeRouteID is the Caddy @id of an app's realtime route (suffix: -http or -https).
func RealtimeRouteID(appID string) string { return "cloudy-realtime-" + appID }
