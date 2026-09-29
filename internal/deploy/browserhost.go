package deploy

import (
	"context"
	"crypto/rand"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	"github.com/fadymondy/hosbah/internal/crypto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/provisioning"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
A desktop browser's own hostname: never published directly, always inside a domain the team already
controls. By default it follows the app's own domain — browser.<zone of the app's first verified
custom domain> (app.moharrik.com → browser.moharrik.com), browser-<name>.<that zone> when that is
taken — then browser-<name> under the team's first Cloudflare zone, then under the platform's apps
domain. A caller may ask for a hostname of its own; it must be a subdomain (never the apex) of one of
the team's Cloudflare zones, or under the apps domain, and not already routed in Hosbah.
*/

// BrowsersAuthURL is Hosbah's own forward_auth target: every request to a desktop browser's
// hostname is checked here (GET, session cookie or the .hosbah/auth exchange) before Caddy ever
// dials the browser's container. See internal/api/browsers.go.
func (s *Service) BrowsersAuthURL() string { return s.ops.PublicURL() + "/api/browsers/auth" }

// DesktopBrowserHostname picks (requested "") or checks (requested set) the hostname of a new
// desktop browser of app. teamOwned is true for a hostname in one of the team's Cloudflare zones,
// false for one under the platform apps domain. A requested hostname Hosbah may not route is an
// error; "" with no error means nothing is available — the caller refuses until the team connects a
// domain.
func (s *Service) DesktopBrowserHostname(ctx context.Context, q *db.Queries, teamID, appID, name, requested string) (hostname string, teamOwned bool, err error) {
	zones := s.teamZones(ctx, q, teamID)
	if requested != "" {
		h := NormalizeHostname(requested)
		if !agentproto.ValidHostname(h) {
			return "", false, invalid("that is not a valid hostname")
		}
		zone := zoneOf(zones, h)
		switch {
		case zone != "" && zone == h:
			return "", false, invalid("a browser takes a subdomain of " + zone + ", never the domain itself")
		case zone != "":
			teamOwned = true
		case s.apps.Enabled() && s.apps.Owns(h) && !s.apps.Reserved(h):
		default:
			return "", false, invalid(h + " is not in any of the team's Cloudflare zones — connect its zone first")
		}
		if s.hostnameTaken(ctx, h) {
			return "", false, conflict(h + " is already routed in Hosbah")
		}
		return h, teamOwned, nil
	}
	label := "browser-" + name
	if domains, err := q.ListDomainsOfApp(ctx, db.ListDomainsOfAppParams{AppID: appID, TeamID: teamID}); err == nil {
		for _, d := range domains {
			zone := zoneOf(zones, d.Hostname)
			if d.VerifiedAt == nil || zone == "" {
				continue
			}
			for _, h := range []string{"browser." + zone, label + "." + zone} {
				if agentproto.ValidHostname(h) && !s.hostnameTaken(ctx, h) {
					return h, true, nil
				}
			}
		}
	}
	if len(zones) > 0 {
		if h := label + "." + zones[0]; agentproto.ValidHostname(h) && !s.hostnameTaken(ctx, h) {
			return h, true, nil
		}
	}
	if s.apps.Enabled() {
		if h := label + "." + s.apps.Domain; agentproto.ValidHostname(h) && !s.apps.Reserved(h) && !s.hostnameTaken(ctx, h) {
			return h, false, nil
		}
	}
	return "", false, nil
}

// zoneOf is the longest of zones that host is, or is a subdomain of; "" when none.
func zoneOf(zones []string, host string) string {
	best := ""
	for _, z := range zones {
		if (host == z || strings.HasSuffix(host, "."+z)) && len(z) > len(best) {
			best = z
		}
	}
	return best
}

// teamZones is every zone name the team's saved Cloudflare credentials can see, in credential then
// zone order; empty when none is connected or none answered. Best effort: a Cloudflare hiccup here
// just means the caller falls back to the apps domain rather than fail the whole request.
func (s *Service) teamZones(ctx context.Context, q *db.Queries, teamID string) []string {
	creds, err := q.ListCredentialsOfTeam(ctx, teamID)
	if err != nil {
		return nil
	}
	var dek []byte
	var out []string
	for _, c := range creds {
		if c.Provider != "cloudflare" {
			continue
		}
		if dek == nil {
			if dek, err = s.ops.TeamDEK(ctx, q, teamID); err != nil {
				return nil
			}
		}
		tok, err := crypto.Open(dek, teamID, provisioning.CredentialContext(c.ID), c.SecretSealed)
		if err != nil {
			continue
		}
		cf := s.cloudflare(string(tok))
		cctx, cancel := context.WithTimeout(ctx, dnsTimeout)
		zones, err := cf.Zones(cctx)
		cancel()
		if err != nil {
			continue
		}
		for _, z := range zones {
			out = append(out, strings.ToLower(strings.TrimSuffix(z.Name, ".")))
		}
	}
	return out
}

// wireDesktopBrowserDNS points a desktop browser's hostname at its server, through the team's
// Cloudflare when the hostname is in one of its zones, or the platform's own DNS for an apps-domain
// hostname. Mirrors addAppsHostname/wireTeamDNSTo; best effort, never fatal.
func (s *Service) wireDesktopBrowserDNS(ctx context.Context, q *db.Queries, teamID string, srv db.Server, hostname string, teamOwned bool) bool {
	if teamOwned {
		return s.wireTeamDNSTo(ctx, q, teamID, srv, hostname)
	}
	s.pointAppsHostname(ctx, hostname, srv)
	return true // a platform hostname is created already verified, like an app's
}

// unwireDesktopBrowserDNS removes what wireDesktopBrowserDNS created.
func (s *Service) unwireDesktopBrowserDNS(ctx context.Context, q *db.Queries, teamID, hostname string) {
	if s.apps.Owns(hostname) {
		s.removeAppsDNS(ctx, hostname)
		return
	}
	s.unwireTeamDNS(ctx, q, teamID, hostname)
}

// CreateDesktopBrowserProxyHost creates and applies the proxy host that exposes a desktop browser:
// its own auto-picked hostname, routed (with WebSocket passthrough, like every proxy host) to its
// loopback upstream on its own server, and always forward_auth'd to BrowsersAuthURL — there is no
// way to create one of these without the auth gate, unlike a normal proxy host. Mirrors
// CreateProxyHost (proxyhosts.go); the differences are the fixed forward_auth, the browser_id link,
// and that the hostname was already picked and checked by DesktopBrowserHostname, not given by a
// caller.
func (s *Service) CreateDesktopBrowserProxyHost(ctx context.Context, q *db.Queries, teamID string, actor Actor, srv db.Server, browserID, hostname, upstream string, teamOwned bool) (db.ProxyHost, error) {
	tok := make([]byte, 16)
	_, _ = rand.Read(tok)
	p, err := q.CreateBrowserProxyHost(ctx, db.CreateBrowserProxyHostParams{
		TeamID: teamID, ServerID: srv.ID, Hostname: hostname, Upstream: upstream,
		VerifyToken: newVerifyToken(tok), CreatedBy: actor.UserID,
		BrowserID: &browserID, ForwardAuth: strptr(s.BrowsersAuthURL()),
	})
	if err != nil {
		if strings.Contains(err.Error(), "proxy_hosts_hostname_key") {
			return db.ProxyHost{}, conflict("that hostname is already routed in Hosbah")
		}
		return db.ProxyHost{}, err
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "proxy_host.created", "proxy_host", p.ID,
		actor.audit(map[string]any{"hostname": hostname, "server": srv.Name, "browser": browserID})); err != nil {
		return db.ProxyHost{}, err
	}
	if s.wireDesktopBrowserDNS(ctx, q, teamID, srv, hostname, teamOwned) {
		if err := q.MarkProxyHostVerified(ctx, db.MarkProxyHostVerifiedParams{ID: p.ID, TeamID: teamID}); err == nil {
			now := time.Now()
			p.VerifiedAt = &now
		}
	}
	return p, s.applyProxyHost(ctx, q, teamID, actor, p, false)
}

// DeleteDesktopBrowserProxyHost removes a desktop browser's proxy host and its DNS record — the
// counterpart to CreateDesktopBrowserProxyHost, called when the browser itself is removed.
func (s *Service) DeleteDesktopBrowserProxyHost(ctx context.Context, q *db.Queries, teamID string, actor Actor, p db.ProxyHost) error {
	return s.DeleteProxyHost(ctx, q, teamID, actor, p)
}
