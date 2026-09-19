package deploy

import (
	"context"
	"database/sql"
	"net"
	"os"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	"github.com/fadymondy/hosbah/internal/cloudflare"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
The apps domain: every app gets a hostname under a domain the platform owns, with no DNS work for
the user — <prefix><app>.<domain>, e.g. cloudy-app-healthdebug.fadymondy.com. It is configuration,
not code, so moving to a dedicated domain later is an .env change:

  CLOUDY_APPS_DOMAIN        the base domain (unset: the feature is off)
  CLOUDY_APPS_PREFIX        prepended to the app name (default "cloudy-app-"; "" gives <app>.<domain>)
  CLOUDY_APPS_TLS           auto (default) | internal | off
  CLOUDY_CLOUDFLARE_TOKEN   an API token with DNS:Edit on the domain's zone; without it Hosbah only
                            routes the hostname and the DNS (e.g. a wildcard) is the operator's job
  CLOUDY_APPS_PROXIED       true to put the records behind Cloudflare's proxy (orange cloud)

Because the platform controls the domain, the hostname is created already verified: no TXT dance.
*/

// AppsDomain is the platform's apps-domain configuration.
type AppsDomain struct {
	Domain  string
	Prefix  string
	TLS     string
	Proxied bool
	DNS     *cloudflare.Client // nil: DNS is managed outside Hosbah
}

// AppsDomainFromEnv reads the configuration; Domain is "" when the feature is off.
func AppsDomainFromEnv() AppsDomain {
	d := AppsDomain{
		Domain:  strings.Trim(strings.ToLower(strings.TrimSpace(os.Getenv("CLOUDY_APPS_DOMAIN"))), "."),
		Prefix:  "cloudy-app-",
		TLS:     strings.TrimSpace(os.Getenv("CLOUDY_APPS_TLS")),
		Proxied: strings.TrimSpace(os.Getenv("CLOUDY_APPS_PROXIED")) == "true",
	}
	if p, ok := os.LookupEnv("CLOUDY_APPS_PREFIX"); ok {
		d.Prefix = strings.ToLower(strings.TrimSpace(p))
	}
	if !agentproto.ValidTLS(d.TLS) {
		d.TLS = "auto"
	}
	if tok := strings.TrimSpace(os.Getenv("CLOUDY_CLOUDFLARE_TOKEN")); tok != "" {
		d.DNS = cloudflare.New(tok)
	}
	return d
}

// Enabled reports whether apps get a platform hostname.
func (d AppsDomain) Enabled() bool { return d.Domain != "" }

// Hostname is the app's platform hostname ("" when off or when the result is not a valid name).
func (d AppsDomain) Hostname(app string) string {
	if !d.Enabled() {
		return ""
	}
	h := d.Prefix + app + "." + d.Domain
	if !agentproto.ValidHostname(h) {
		return ""
	}
	return h
}

// Owns reports whether hostname is a platform hostname (users cannot add or remove those by hand).
func (d AppsDomain) Owns(hostname string) bool {
	return d.Enabled() && strings.HasSuffix(hostname, "."+d.Domain) && strings.HasPrefix(hostname, d.Prefix) &&
		!strings.Contains(strings.TrimSuffix(hostname, "."+d.Domain), ".")
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
	d, err := q.InsertDomain(ctx, db.InsertDomainParams{TeamID: teamID, AppID: a.ID, Hostname: host, Tls: s.apps.TLS, VerifyToken: "platform"})
	if err != nil {
		s.log.Warn("apps domain: could not add the hostname", "app", a.ID, "host", host, "err", err)
		return
	}
	if err := q.MarkDomainVerified(ctx, db.MarkDomainVerifiedParams{ID: d.ID, TeamID: teamID}); err != nil {
		s.log.Warn("apps domain: could not mark verified", "host", host, "err", err)
	}
	s.pointAppsHostname(ctx, host, srv)
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
	ds, _ := q.ListDomainsOfApp(ctx, db.ListDomainsOfAppParams{AppID: a.ID, TeamID: teamID})
	for _, d := range ds {
		if d.VerifyToken == "platform" {
			return d.Hostname
		}
	}
	return ""
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
