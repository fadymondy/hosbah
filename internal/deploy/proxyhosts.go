package deploy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// The Caddy proxy manager: proxy hosts put any upstream a server can reach behind a hostname with
// TLS, like Nginx Proxy Manager's "proxy hosts", on the Caddy the agent already runs. They share the
// hostname space with app domains (one hostname routes to one thing in Hosbah), the same TLS modes,
// and the same verification rule: a public certificate only once the hostname is proven — by the
// _cloudy-verify TXT record, or at once when the team's Cloudflare holds the zone.

// ProxyHostInput creates or changes a proxy host.
type ProxyHostInput struct {
	Server     string // create only: server id or name
	Hostname   string // create only
	Upstream   string
	TLS        string // auto | internal | off (default auto)
	Enabled    *bool  // default true
	Websockets *bool  // kept for the record; Caddy proxies websockets either way
	SkipVerify bool   // https upstream with a self-signed certificate
}

// CreateProxyHost adds a proxy host and applies it on its server.
func (s *Service) CreateProxyHost(ctx context.Context, q *db.Queries, teamID string, actor Actor, in ProxyHostInput) (db.ProxyHost, error) {
	host := NormalizeHostname(in.Hostname)
	if !agentproto.ValidHostname(host) {
		return db.ProxyHost{}, invalid("that is not a valid hostname (for example nas.example.com)")
	}
	if s.apps.Owns(host) {
		return db.ProxyHost{}, invalid("hostnames under " + s.apps.Domain + " are given to apps automatically")
	}
	tls := orDefault(in.TLS, "auto")
	if !agentproto.ValidTLS(tls) {
		return db.ProxyHost{}, invalid("tls must be auto, internal or off")
	}
	up := strings.TrimSpace(in.Upstream)
	if _, err := agentproto.ParseUpstream(up); err != nil {
		return db.ProxyHost{}, invalid(err.Error())
	}
	srv, err := q.ServerInTeamByRef(ctx, db.ServerInTeamByRefParams{TeamID: teamID, Ref: in.Server})
	if err != nil {
		return db.ProxyHost{}, notFound("server")
	}
	if srv.Status != "enrolled" {
		return db.ProxyHost{}, conflict("that server's agent has not enrolled yet")
	}
	if s.hostnameTaken(ctx, host) {
		return db.ProxyHost{}, conflict("that hostname is already routed in Hosbah")
	}
	tok := make([]byte, 16)
	_, _ = rand.Read(tok)
	p, err := q.CreateProxyHost(ctx, db.CreateProxyHostParams{TeamID: teamID, ServerID: srv.ID, Hostname: host, Upstream: up, Tls: tls,
		Enabled: boolOr(in.Enabled, true), Websockets: boolOr(in.Websockets, true), VerifyToken: "cloudy-verify=" + hex.EncodeToString(tok),
		CreatedBy: actor.UserID})
	if err != nil {
		if strings.Contains(err.Error(), "proxy_hosts_hostname_key") {
			return db.ProxyHost{}, conflict("that hostname is already routed in Hosbah")
		}
		return db.ProxyHost{}, err
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "proxy_host.created", "proxy_host", p.ID,
		actor.audit(map[string]any{"hostname": host, "upstream": up, "server": srv.Name, "tls": tls})); err != nil {
		return db.ProxyHost{}, err
	}
	if s.publishTunnelled(ctx, q, teamID, srv.ID, host) || s.wireTeamDNSTo(ctx, q, teamID, srv, host) {
		if err := q.MarkProxyHostVerified(ctx, db.MarkProxyHostVerifiedParams{ID: p.ID, TeamID: teamID}); err == nil {
			now := time.Now()
			p.VerifiedAt = &now
		}
	}
	return p, s.applyProxyHost(ctx, q, teamID, actor, p, in.SkipVerify)
}

// UpdateProxyHost changes a proxy host's upstream, TLS mode or enabled flag and re-applies it.
func (s *Service) UpdateProxyHost(ctx context.Context, q *db.Queries, teamID string, actor Actor, p db.ProxyHost, in ProxyHostInput) (db.ProxyHost, error) {
	up := p.Upstream
	if strings.TrimSpace(in.Upstream) != "" {
		up = strings.TrimSpace(in.Upstream)
		if _, err := agentproto.ParseUpstream(up); err != nil {
			return db.ProxyHost{}, invalid(err.Error())
		}
	}
	tls := orDefault(in.TLS, p.Tls)
	if !agentproto.ValidTLS(tls) {
		return db.ProxyHost{}, invalid("tls must be auto, internal or off")
	}
	u, err := q.UpdateProxyHost(ctx, db.UpdateProxyHostParams{ID: p.ID, TeamID: teamID, Upstream: up, Tls: tls,
		Enabled: boolOr(in.Enabled, p.Enabled), Websockets: boolOr(in.Websockets, p.Websockets)})
	if err != nil {
		return db.ProxyHost{}, notFound("proxy host")
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "proxy_host.updated", "proxy_host", p.ID,
		actor.audit(map[string]any{"hostname": u.Hostname, "upstream": up, "tls": tls, "enabled": u.Enabled})); err != nil {
		return db.ProxyHost{}, err
	}
	return u, s.applyProxyHost(ctx, q, teamID, actor, u, in.SkipVerify)
}

// DeleteProxyHost removes it from Caddy, its Cloudflare record (only one Hosbah made) and Hosbah.
func (s *Service) DeleteProxyHost(ctx context.Context, q *db.Queries, teamID string, actor Actor, p db.ProxyHost) error {
	raw, _ := json.Marshal(agentproto.ProxyParams{ProxyID: p.ID, Remove: true})
	if _, _, err := s.ops.Enqueue(ctx, q, ops.EnqueueSpec{TeamID: teamID, ServerID: p.ServerID, Kind: agentproto.KindProxySet, Params: raw, CreatedBy: actor.UserID}); err != nil {
		return err
	}
	if n, err := q.DeleteProxyHostInTeam(ctx, db.DeleteProxyHostInTeamParams{ID: p.ID, TeamID: teamID}); err != nil || n == 0 {
		return notFound("proxy host")
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "proxy_host.deleted", "proxy_host", p.ID,
		actor.audit(map[string]any{"hostname": p.Hostname})); err != nil {
		return err
	}
	s.unwireTeamDNS(ctx, q, teamID, p.Hostname)
	s.unpublishHost(ctx, q, teamID, p.Hostname)
	return nil
}

// VerifyProxyHost checks the _cloudy-verify TXT record, like an app domain.
func (s *Service) VerifyProxyHost(ctx context.Context, q *db.Queries, teamID string, actor Actor, p db.ProxyHost) (bool, string, error) {
	if p.VerifiedAt != nil {
		return true, p.Hostname + " is already verified.", nil
	}
	name := "_cloudy-verify." + p.Hostname
	lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	records, err := LookupTXT(lctx, name)
	for _, r := range records {
		if strings.TrimSpace(r) == p.VerifyToken {
			if err := q.MarkProxyHostVerified(ctx, db.MarkProxyHostVerifiedParams{ID: p.ID, TeamID: teamID}); err != nil {
				return false, "", err
			}
			return true, p.Hostname + " is verified.", tenancy.Record(ctx, q, teamID, actor.UserID, "proxy_host.verified", "proxy_host", p.ID,
				actor.audit(map[string]any{"hostname": p.Hostname}))
		}
	}
	why := "no TXT record found"
	if err == nil && len(records) > 0 {
		why = "the TXT record does not match"
	}
	return false, fmt.Sprintf("Not verified yet (%s). Add a TXT record %s with the value %s, then verify again.", why, name, p.VerifyToken), nil
}

// ProxyHostByRef resolves one of the team's proxy hosts by id or hostname.
func ProxyHostByRef(ctx context.Context, q *db.Queries, teamID, ref string) (db.ProxyHost, error) {
	p, err := q.ProxyHostInTeam(ctx, db.ProxyHostInTeamParams{TeamID: teamID, Ref: NormalizeHostname(ref)})
	if err != nil {
		if p, err = q.ProxyHostInTeam(ctx, db.ProxyHostInTeamParams{TeamID: teamID, Ref: strings.TrimSpace(ref)}); err != nil {
			return db.ProxyHost{}, notFound("proxy host")
		}
	}
	return p, nil
}

// applyProxyHost queues proxy.set on the host's server: the route when enabled, removal when not.
// A server that never deployed an app has no Caddy yet, so the (idempotent) caddy.install step goes
// first. proxy.set runs in the agent's host lane, like provisioning, so the install finishes first.
func (s *Service) applyProxyHost(ctx context.Context, q *db.Queries, teamID string, actor Actor, p db.ProxyHost, skipVerify bool) error {
	if p.Enabled {
		if err := s.ensureCaddy(ctx, q, teamID, actor, p.ServerID); err != nil {
			return err
		}
	}
	params := agentproto.ProxyParams{ProxyID: p.ID, Hostname: p.Hostname, TLS: p.Tls, Upstream: p.Upstream, SkipVerify: skipVerify}
	if !p.Enabled {
		params = agentproto.ProxyParams{ProxyID: p.ID, Remove: true}
	}
	raw, _ := json.Marshal(params)
	_, _, err := s.ops.Enqueue(ctx, q, ops.EnqueueSpec{TeamID: teamID, ServerID: p.ServerID, Kind: agentproto.KindProxySet, Params: raw, CreatedBy: actor.UserID})
	return err
}

func boolOr(b *bool, def bool) bool {
	if b == nil {
		return def
	}
	return *b
}

func orDefault(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return strings.TrimSpace(s)
}

// ensureCaddy queues the idempotent caddy.install step on a server (a no-op when Caddy is there).
func (s *Service) ensureCaddy(ctx context.Context, q *db.Queries, teamID string, actor Actor, serverID string) error {
	srv, err := q.ServerInTeam(ctx, db.ServerInTeamParams{ID: serverID, TeamID: teamID})
	if err != nil {
		return notFound("server")
	}
	arch := "amd64"
	if srv.Arch != nil && *srv.Arch != "" {
		arch = *srv.Arch
	}
	caddy, err := CaddyTool(arch)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(agentproto.ProvisionParams{Step: agentproto.StepCaddy, Tool: caddy, AskURL: s.AskURL()})
	_, _, err = s.ops.Enqueue(ctx, q, ops.EnqueueSpec{TeamID: teamID, ServerID: serverID, Kind: agentproto.KindProvisionStep, Params: raw, CreatedBy: actor.UserID})
	return err
}
