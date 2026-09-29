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
A desktop browser's own hostname: never published directly, always a subdomain of a domain the
team already controls — its own Cloudflare zone if it has one connected, the platform's apps
domain otherwise. The label is fixed (browser-<name>), so there is nothing for the caller to choose
and nothing that could collide with an app's own hostname rules.
*/

// BrowsersAuthURL is Hosbah's own forward_auth target: every request to a desktop browser's
// hostname is checked here (GET, session cookie or the .hosbah/auth exchange) before Caddy ever
// dials the browser's container. See internal/api/browsers.go.
func (s *Service) BrowsersAuthURL() string { return s.ops.PublicURL() + "/api/browsers/auth" }

// DesktopBrowserHostname picks a hostname for a new desktop browser: browser-<name> under the
// team's own Cloudflare zone (first zone any of its saved credentials can see), or under the
// platform apps domain when the team has none. "" means neither is available — the caller must
// refuse to create a desktop browser until the team connects one.
func (s *Service) DesktopBrowserHostname(ctx context.Context, q *db.Queries, teamID, name string) (hostname string, teamOwned bool) {
	label := "browser-" + name
	if domain := s.teamPrimaryDomain(ctx, q, teamID); domain != "" {
		if h := label + "." + domain; agentproto.ValidHostname(h) && !s.hostnameTaken(ctx, h) {
			return h, true
		}
	}
	if s.apps.Enabled() {
		if h := label + "." + s.apps.Domain; agentproto.ValidHostname(h) && !s.apps.Reserved(h) && !s.hostnameTaken(ctx, h) {
			return h, false
		}
	}
	return "", false
}

// teamPrimaryDomain is the first zone name any of the team's saved Cloudflare credentials can see,
// "" when the team has none connected or none answered. Best effort: a Cloudflare hiccup here just
// means the caller falls back to the apps domain rather than fail the whole request.
func (s *Service) teamPrimaryDomain(ctx context.Context, q *db.Queries, teamID string) string {
	creds, err := q.ListCredentialsOfTeam(ctx, teamID)
	if err != nil {
		return ""
	}
	var dek []byte
	for _, c := range creds {
		if c.Provider != "cloudflare" {
			continue
		}
		if dek == nil {
			if dek, err = s.ops.TeamDEK(ctx, q, teamID); err != nil {
				return ""
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
		if err != nil || len(zones) == 0 {
			continue
		}
		return strings.ToLower(strings.TrimSuffix(zones[0].Name, "."))
	}
	return ""
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
