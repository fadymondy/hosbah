package deploy

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	"github.com/fadymondy/hosbah/internal/brand"
	"github.com/fadymondy/hosbah/internal/cloudflare"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
The apps domain: every app gets a hostname under a domain the platform owns, with no DNS work for
the user — <prefix><app>.<domain>, e.g. shop.hosbah.com. It is configuration, not code, so moving
to a dedicated domain later is an .env change (each variable is also read as CLOUDY_…, its name
before the rename):

  HOSBAH_APPS_DOMAIN        the base domain (unset: the feature is off)
  HOSBAH_APPS_PREFIX        prepended to the app name (default "hosbah-app-"; "" gives <app>.<domain>)
  HOSBAH_APPS_TLS           auto (default) | internal | off — ignored once HOSBAH_APPS_EDGE is set (see below)
  HOSBAH_CLOUDFLARE_TOKEN   an API token with DNS:Edit on the domain's zone; without it Hosbah only
                            routes the hostname and the DNS (e.g. a wildcard) is the operator's job
  HOSBAH_APPS_PROXIED       true to put the records behind Cloudflare's proxy (orange cloud)
  HOSBAH_APPS_EDGE          the id of a Hosbah server whose Caddy is the internal "apps edge" for
                            platform hostnames — for a fleet of private-network servers behind one
                            public edge (a Cloudflare wildcard *.<domain> and an operator-run reverse
                            proxy pointed at this server's :80). Unset: off, every app's own Caddy
                            answers its platform hostname directly, as HOSBAH_APPS_TLS says.
                            Set: every platform hostname gets tls "off" (the edge, or whatever sits
                            in front of it, terminates TLS) and, unless the app is ON the edge server
                            itself, a route on the edge's own Caddy (agent proxy.set) to
                            "<the app's server IP>:80" — so a request for shop.hosbah.com reaches the
                            edge, which forwards it over the private network to the app's own server.

The default prefix was "cloudy-app-" before the rename. Where the prefix is not set, hostnames made
with that one are still the platform's (Owns); new apps get "hosbah-app-".

Because the platform controls the domain, the hostname is created already verified: no TXT dance.
*/

// AppsDomain is the platform's apps-domain configuration.
type AppsDomain struct {
	Domain string
	Prefix string
	// LegacyPrefix is the pre-rename default ("cloudy-app-"), set only when Prefix is the default.
	LegacyPrefix string
	TLS          string
	Proxied      bool
	DNS          *cloudflare.Client // nil: DNS is managed outside Hosbah
	// EdgeServerID: HOSBAH_APPS_EDGE — "" means there is no apps edge (see the doc comment above).
	EdgeServerID string
}

// AppsDomainFromEnv reads the configuration; Domain is "" when the feature is off.
func AppsDomainFromEnv() AppsDomain {
	d := AppsDomain{
		Domain:       strings.Trim(strings.ToLower(strings.TrimSpace(brand.Getenv("APPS_DOMAIN"))), "."),
		Prefix:       defaultAppsPrefix,
		LegacyPrefix: legacyAppsPrefix,
		TLS:          strings.TrimSpace(brand.Getenv("APPS_TLS")),
		Proxied:      strings.TrimSpace(brand.Getenv("APPS_PROXIED")) == "true",
		EdgeServerID: strings.TrimSpace(brand.Getenv("APPS_EDGE")),
	}
	if p, ok := brand.LookupEnv("APPS_PREFIX"); ok {
		d.Prefix, d.LegacyPrefix = strings.ToLower(strings.TrimSpace(p)), ""
	}
	if !agentproto.ValidTLS(d.TLS) {
		d.TLS = "auto"
	}
	if tok := strings.TrimSpace(brand.Getenv("CLOUDFLARE_TOKEN")); tok != "" {
		d.DNS = cloudflare.New(tok)
	}
	return d
}

// The default prefix, and the default before the rename.
const (
	defaultAppsPrefix = "hosbah-app-"
	legacyAppsPrefix  = "cloudy-app-"
)

// Enabled reports whether apps get a platform hostname.
func (d AppsDomain) Enabled() bool { return d.Domain != "" }

// EdgeConfigured reports whether HOSBAH_APPS_EDGE names an apps-edge server.
func (d AppsDomain) EdgeConfigured() bool { return d.EdgeServerID != "" }

// PlatformTLS is the TLS mode a platform hostname's own (app-server) Caddy route is created with:
// "off" once an apps edge is configured (it, or whatever fronts it, terminates TLS — see the doc
// comment above), HOSBAH_APPS_TLS otherwise.
func (d AppsDomain) PlatformTLS() string {
	if d.EdgeConfigured() {
		return "off"
	}
	return d.TLS
}

// edgeProxyIDSeed namespaces a platform hostname's route on the apps-edge server's Caddy.
const edgeProxyIDSeed = "hosbah/apps-edge/"

// EdgeProxyID is the deterministic proxy_id of a platform domain's route on the apps-edge server's
// Caddy: re-applying it (e.g. from the reconcile job) always targets the same Caddy route. It is a
// UUID because the agent's proxy.set accepts nothing else (agentproto.ValidID); name-based (version
// 5 bits over a SHA-256 of the seed), so it never equals a team's own random (v4) proxy_hosts id.
func EdgeProxyID(domainID string) string {
	sum := sha256.Sum256([]byte(edgeProxyIDSeed + domainID))
	b := sum[:16]
	b[6] = b[6]&0x0f | 0x50
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// edgeUpstream is what the apps-edge Caddy dials for an app on srv: the app's own Caddy on :80,
// reached over the private network. "" when srv has no address to route to.
func edgeUpstream(srv db.Server) string {
	if srv.IpAddress == nil || strings.TrimSpace(*srv.IpAddress) == "" {
		return ""
	}
	return net.JoinHostPort(strings.TrimSpace(*srv.IpAddress), "80")
}

// Hostname is the app's platform hostname ("" when off or when the result is not a valid name).
func (d AppsDomain) Hostname(app string) string {
	if !d.Enabled() {
		return ""
	}
	h := d.Prefix + app + "." + d.Domain
	if !agentproto.ValidHostname(h) || reservedLabels[d.Prefix+app] {
		return ""
	}
	return h
}

// reservedLabels are the platform's own names: with an empty prefix an app must not take them.
var reservedLabels = map[string]bool{"www": true, "api": true, "app": true, "admin": true, "mail": true,
	"docs": true, "status": true, "agent": true, "install": true, "mcp": true}

// Owns reports whether hostname is a platform hostname (users cannot add or remove those by hand).
func (d AppsDomain) Owns(hostname string) bool {
	prefixed := strings.HasPrefix(hostname, d.Prefix) || (d.LegacyPrefix != "" && strings.HasPrefix(hostname, d.LegacyPrefix))
	return d.Enabled() && strings.HasSuffix(hostname, "."+d.Domain) && prefixed &&
		!strings.Contains(strings.TrimSuffix(hostname, "."+d.Domain), ".") && !d.Reserved(hostname)
}

// Reserved reports whether hostname is one of the platform's own names under the apps domain
// (www.<domain>, app.<domain>, ...): no app is given one, and only a platform admin may route it,
// e.g. the marketing site on www. Other teams must not squat on them.
func (d AppsDomain) Reserved(hostname string) bool {
	if !d.Enabled() || !strings.HasSuffix(hostname, "."+d.Domain) {
		return false
	}
	return reservedLabels[strings.TrimSuffix(hostname, "."+d.Domain)]
}

// checkPlatformName refuses a hostname the apps domain keeps: platform hostnames always, reserved
// names unless the actor is a platform admin.
func (s *Service) checkPlatformName(ctx context.Context, q *db.Queries, actor Actor, hostname string) error {
	if s.apps.Reserved(hostname) {
		if u, err := q.UserByID(ctx, actor.UserID); err == nil && u.DisabledAt == nil && hasRoleCSV(u.Roles, "admin") {
			return nil
		}
		return invalid(hostname + " is reserved for the platform (a platform admin can route it)")
	}
	if s.apps.Owns(hostname) {
		return invalid("hostnames under " + s.apps.Domain + " are given to apps automatically")
	}
	return nil
}

func hasRoleCSV(csv, role string) bool {
	for _, r := range strings.Split(csv, ",") {
		if strings.TrimSpace(r) == role {
			return true
		}
	}
	return false
}

// dnsTimeout bounds a Cloudflare call made on the request path.
var dnsTimeout = 15 * time.Second

// addAppsHostname gives a new app its platform hostname, pre-verified. DNS is best effort: a
// Cloudflare hiccup must not fail the app's creation, so it is logged and the operator can re-run
// it (Sync on the next deploy points it again).
func (s *Service) addAppsHostname(ctx context.Context, q *db.Queries, teamID string, a db.App, srv db.Server) {
	host := s.apps.Hostname(a.Name)
	if host == "" {
		return
	}
	// Hostnames are unique across Hosbah and RLS hides other teams' rows, so ask in system scope
	// (a failed insert would abort the caller's transaction). Taken: add a short team suffix.
	if s.hostnameTaken(ctx, host) {
		host = s.apps.Hostname(a.Name + "-" + teamID[:6])
		if host == "" || s.hostnameTaken(ctx, host) {
			s.log.Warn("apps domain: hostname taken", "app", a.ID)
			return
		}
	}
	d, err := q.InsertDomain(ctx, db.InsertDomainParams{TeamID: teamID, AppID: a.ID, Hostname: host, Tls: s.apps.PlatformTLS(), VerifyToken: "platform"})
	if err != nil {
		s.log.Warn("apps domain: could not add the hostname", "app", a.ID, "host", host, "err", err)
		return
	}
	if err := q.MarkDomainVerified(ctx, db.MarkDomainVerifiedParams{ID: d.ID, TeamID: teamID}); err != nil {
		s.log.Warn("apps domain: could not mark verified", "host", host, "err", err)
	}
	s.pointAppsHostname(ctx, host, srv)
	s.enqueueEdgeProxySet(ctx, teamID, host, d.ID, srv, false)
}

// pointAppsHostname points the hostname at the server's public address in Cloudflare.
func (s *Service) pointAppsHostname(ctx context.Context, host string, srv db.Server) {
	if s.apps.DNS == nil || srv.IpAddress == nil {
		return
	}
	ip := net.ParseIP(*srv.IpAddress)
	if ip == nil || ip.IsPrivate() || ip.IsLoopback() {
		s.log.Warn("apps domain: the server has no public address; not creating DNS", "host", host, "ip", *srv.IpAddress)
		return
	}
	cctx, cancel := context.WithTimeout(ctx, dnsTimeout)
	defer cancel()
	if _, _, err := s.apps.DNS.PointHost(cctx, host, ip.String(), s.apps.Proxied); err != nil {
		s.log.Warn("apps domain: DNS failed", "host", host, "err", err)
	}
}

// platformHost is the app's platform hostname as stored (it may carry a team suffix), or "".
func platformHost(ctx context.Context, q *db.Queries, teamID string, a db.App) string {
	h, _ := platformDomain(ctx, q, teamID, a)
	return h
}

// platformDomain is the app's platform domain: its hostname and its domain id, or ("", "") when the
// app has none.
func platformDomain(ctx context.Context, q *db.Queries, teamID string, a db.App) (hostname, domainID string) {
	ds, _ := q.ListDomainsOfApp(ctx, db.ListDomainsOfAppParams{AppID: a.ID, TeamID: teamID})
	for _, d := range ds {
		if d.VerifyToken == "platform" {
			return d.Hostname, d.ID
		}
	}
	return "", ""
}

// removeAppsDNS deletes a platform hostname's records when its app goes.
func (s *Service) removeAppsDNS(ctx context.Context, host string) {
	if host == "" || s.apps.DNS == nil || !s.apps.Owns(host) {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, dnsTimeout)
	defer cancel()
	z, err := s.apps.DNS.ZoneFor(cctx, host)
	if err == nil {
		err = s.apps.DNS.DeleteManaged(cctx, z.ID, host)
	}
	if err != nil {
		s.log.Warn("apps domain: could not remove DNS", "host", host, "err", err)
	}
}

func (s *Service) hostnameTaken(ctx context.Context, host string) bool {
	var n int64
	err := tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{System: true}, func(q *db.Queries, _ *sql.Tx) error {
		var err error
		n, err = q.HostnameTaken(ctx, host)
		return err
	})
	return err != nil || n > 0
}

// edgeProxyOpID derives an operation id from (domain, the params being applied): re-applying the same
// route is the same operation (idempotent — the reconcile job calls this on every run), while a
// genuine change (e.g. Set -> Remove, or a new upstream after the app moves) naturally gets a fresh id
// instead of colliding with the old one (ops.Enqueue errors on the same id with different params).
func edgeProxyOpID(domainID string, params agentproto.ProxyParams) string {
	raw, _ := json.Marshal(params)
	// A seed of its own: "cloudy-step" (service.go) is frozen for the deploy pipeline's own op ids.
	sum := sha256.Sum256([]byte("hosbah-edge-proxy\n" + domainID + "\n" + string(raw)))
	b := sum[:16]
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// enqueueEdgeProxySet applies (remove: false) or removes (remove: true) a platform hostname's route
// on the apps-edge server's Caddy — see the HOSBAH_APPS_EDGE doc comment above. A no-op when there is
// no edge configured, when the app's own server IS the edge (nothing to route through), or, for an
// apply, when the app's server has no address to route to yet. teamID is the app's own team, recorded
// only in the audit entry: the operation is enqueued in the edge server's own team scope (operations
// has no system-scope INSERT, and the edge may belong to a different team than the app).
func (s *Service) enqueueEdgeProxySet(ctx context.Context, teamID, host, domainID string, srv db.Server, remove bool) {
	if !s.apps.EdgeConfigured() || srv.ID == s.apps.EdgeServerID {
		return
	}
	params := agentproto.ProxyParams{ProxyID: EdgeProxyID(domainID), Remove: true}
	if !remove {
		up := edgeUpstream(srv)
		if up == "" {
			return
		}
		params = agentproto.ProxyParams{ProxyID: EdgeProxyID(domainID), Hostname: host, TLS: "off", Upstream: up}
	}
	var edge db.Server
	if err := tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{System: true}, func(q *db.Queries, _ *sql.Tx) error {
		var err error
		edge, err = q.ServerByID(ctx, s.apps.EdgeServerID)
		return err
	}); err != nil {
		s.log.Warn("apps edge: could not load the edge server", "server", s.apps.EdgeServerID, "err", err)
		return
	}
	actor := Actor{UserID: "hosbah", Via: "system"}
	err := tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{System: true, TeamID: edge.TeamID}, func(q *db.Queries, _ *sql.Tx) error {
		if !remove {
			if err := s.ensureCaddy(ctx, q, edge.TeamID, actor, edge.ID); err != nil {
				return err
			}
		}
		raw, err := json.Marshal(params)
		if err != nil {
			return err
		}
		if _, _, err := s.ops.Enqueue(ctx, q, ops.EnqueueSpec{OpID: edgeProxyOpID(domainID, params), TeamID: edge.TeamID, ServerID: edge.ID,
			Kind: agentproto.KindProxySet, Params: raw, CreatedBy: actor.UserID}); err != nil {
			return err
		}
		return tenancy.Record(ctx, q, edge.TeamID, actor.UserID, "apps_edge.proxy_set", "server", edge.ID,
			map[string]any{"hostname": host, "app_team": teamID, "remove": remove})
	})
	if err != nil {
		s.log.Warn("apps edge: could not enqueue proxy.set", "host", host, "err", err)
	}
}
