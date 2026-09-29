package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	"github.com/fadymondy/hosbah/internal/crypto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/npm"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/provisioning"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
Tunnels: servers without a public address (a customer box behind NAT, a laptop, Docker Desktop).

  frp        the agent runs frpc and exposes the server's Caddy (:80) on a port Hosbah allocates on
             the team's frps. Every domain routed on that server then gets, through the team's other
             credentials, a proxied Cloudflare record at the frps public IP and an NPM proxy host
             forwarding the domain to edge_host:port. The server's own Caddy routes it by Host.
  tailscale  the agent joins the team's tailnet with a reusable auth key; the tunnel's address is
             the server's tailnet IP.
*/

// FrpcTool is tunnel.frp's pinned frpc download (the frp release tarball).
func FrpcTool(arch string) (*agentproto.ToolInstall, error) {
	sum, ok := map[string]string{
		"amd64": "52ced8c5fdf772f48a9909da4c10c7568c061861946ac9af7a86eeaf14b7e6d5",
		"arm64": "d3e2be696d70b8e8dc475a81b8b5e4089f3a2acde5e42e47d6fc020fe3ddb180",
	}[arch]
	if !ok {
		return nil, fmt.Errorf("no frpc build pinned for %q", arch)
	}
	return &agentproto.ToolInstall{Version: agentproto.FrpcVersion, SHA256: sum,
		URL: fmt.Sprintf("https://github.com/fatedier/frp/releases/download/v%[1]s/frp_%[1]s_linux_%[2]s.tar.gz", agentproto.FrpcVersion, arch)}, nil
}

// tunnelTimeout bounds waiting for the agent to bring a tunnel up.
var tunnelTimeout = 6 * time.Minute

// credentialOf opens one of the team's credentials: its settings and its secret.
func (s *Service) credentialOf(ctx context.Context, q *db.Queries, teamID, ref string) (db.Credential, map[string]string, string, error) {
	creds, err := q.ListCredentialsOfTeam(ctx, teamID)
	if err != nil {
		return db.Credential{}, nil, "", err
	}
	for _, c := range creds {
		if c.ID != ref && !strings.EqualFold(c.Name, ref) {
			continue
		}
		dek, err := s.ops.TeamDEK(ctx, q, teamID)
		if err != nil {
			return db.Credential{}, nil, "", err
		}
		sec, err := crypto.Open(dek, teamID, provisioning.CredentialContext(c.ID), c.SecretSealed)
		if err != nil {
			return db.Credential{}, nil, "", errors.New("the credential's secret could not be opened")
		}
		cfg := map[string]string{}
		_ = json.Unmarshal([]byte(c.Config), &cfg)
		return c, cfg, string(sec), nil
	}
	return db.Credential{}, nil, "", notFound("credential")
}

// EnableTunnel opens a tunnel for a server with one of the team's frp or tailscale credentials.
func (s *Service) EnableTunnel(ctx context.Context, q *db.Queries, teamID string, actor Actor, srv db.Server, credRef string) (db.Tunnel, error) {
	if srv.Status != "enrolled" || srv.AgentPublicKey == nil {
		return db.Tunnel{}, conflict("that server's agent has not enrolled yet")
	}
	cred, cfg, secret, err := s.credentialOf(ctx, q, teamID, credRef)
	if err != nil {
		return db.Tunnel{}, err
	}
	kind := cred.Provider
	if kind != "frp" && kind != "tailscale" {
		return db.Tunnel{}, invalid("choose an frp or tailscale credential")
	}
	var port sql.NullInt32
	if kind == "frp" {
		p, err := freePort(ctx, q, teamID, cred.ID, cfg)
		if err != nil {
			return db.Tunnel{}, err
		}
		port = sql.NullInt32{Int32: int32(p), Valid: true}
	}
	t, err := q.CreateTunnel(ctx, db.CreateTunnelParams{TeamID: teamID, ServerID: srv.ID, CredentialID: cred.ID, Kind: kind,
		RemotePort: port, Status: "pending", CreatedBy: actor.UserID})
	if err != nil {
		if strings.Contains(err.Error(), "tunnels_server_kind_key") {
			return db.Tunnel{}, conflict("this server already has a " + kind + " tunnel")
		}
		return db.Tunnel{}, err
	}
	params, err := tunnelParams(t, srv, cfg, secret)
	if err != nil {
		return db.Tunnel{}, err
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "tunnel.enabled", "server", srv.ID,
		actor.audit(map[string]any{"kind": kind, "credential": cred.Name, "remote_port": port.Int32})); err != nil {
		return db.Tunnel{}, err
	}
	opKind := agentproto.KindTunnelFrp
	if kind == "tailscale" {
		opKind = agentproto.KindTunnelTailscale
	}
	raw, _ := json.Marshal(params)
	op, _, err := s.ops.Enqueue(ctx, q, ops.EnqueueSpec{TeamID: teamID, ServerID: srv.ID, Kind: opKind, Params: raw, CreatedBy: actor.UserID})
	if err != nil {
		return db.Tunnel{}, err
	}
	s.settleTunnel(teamID, t, op.ID, srv.ID)
	return t, nil
}

// DisableTunnel tears a tunnel down on the server and forgets it.
func (s *Service) DisableTunnel(ctx context.Context, q *db.Queries, teamID string, actor Actor, t db.Tunnel) error {
	var raw []byte
	kind := agentproto.KindTunnelFrp
	if t.Kind == "tailscale" {
		kind = agentproto.KindTunnelTailscale
		raw, _ = json.Marshal(agentproto.TailscaleParams{TunnelID: t.ID, Remove: true})
	} else {
		raw, _ = json.Marshal(agentproto.FrpParams{TunnelID: t.ID, Remove: true})
	}
	if _, _, err := s.ops.Enqueue(ctx, q, ops.EnqueueSpec{TeamID: teamID, ServerID: t.ServerID, Kind: kind, Params: raw, CreatedBy: actor.UserID}); err != nil {
		return err
	}
	if n, err := q.DeleteTunnelInTeam(ctx, db.DeleteTunnelInTeamParams{ID: t.ID, TeamID: teamID}); err != nil || n == 0 {
		return notFound("tunnel")
	}
	return tenancy.Record(ctx, q, teamID, actor.UserID, "tunnel.disabled", "server", t.ServerID, actor.audit(map[string]any{"kind": t.Kind}))
}

func tunnelParams(t db.Tunnel, srv db.Server, cfg map[string]string, secret string) (any, error) {
	if t.Kind == "tailscale" {
		box, err := agentproto.SealBox(*srv.AgentPublicKey, agentproto.DBSecretContext(t.ID, "tailscale"), mustJSON(map[string]string{"authkey": secret}))
		if err != nil {
			return nil, err
		}
		return agentproto.TailscaleParams{TunnelID: t.ID, Hostname: agentproto.TailnetHostname(srv.Name), Secret: box}, nil
	}
	arch := "amd64"
	if srv.Arch != nil && *srv.Arch != "" {
		arch = *srv.Arch
	}
	tool, err := FrpcTool(arch)
	if err != nil {
		return nil, err
	}
	box, err := agentproto.SealBox(*srv.AgentPublicKey, agentproto.DBSecretContext(t.ID, "frp"), mustJSON(map[string]string{"token": secret}))
	if err != nil {
		return nil, err
	}
	sp, _ := strconv.Atoi(orDefault(cfg["server_port"], "7000"))
	return agentproto.FrpParams{TunnelID: t.ID, ServerAddr: cfg["server_addr"], ServerPort: sp, RemotePort: int(t.RemotePort.Int32),
		LocalPort: 80, Tool: tool, Secret: box}, nil
}

// freePort picks the lowest port in the credential's range not used by another of its tunnels.
func freePort(ctx context.Context, q *db.Queries, teamID, credID string, cfg map[string]string) (int, error) {
	from, _ := strconv.Atoi(orDefault(cfg["port_from"], "47000"))
	to, _ := strconv.Atoi(orDefault(cfg["port_to"], "47999"))
	if from < 1 || to > 65535 || from > to {
		return 0, invalid("the frp credential's port range is invalid")
	}
	used, err := q.UsedTunnelPorts(ctx, db.UsedTunnelPortsParams{CredentialID: credID, TeamID: teamID})
	if err != nil {
		return 0, err
	}
	taken := map[int]bool{}
	for _, p := range used {
		taken[int(p)] = true
	}
	for p := from; p <= to; p++ {
		if !taken[p] {
			return p, nil
		}
	}
	return 0, conflict("no free port left in the frp credential's range")
}

// settleTunnel waits for the agent in the background and records the outcome in team scope. Once
// an frp tunnel is up, the domains already routed on the server are published at the edge.
func (s *Service) settleTunnel(teamID string, t db.Tunnel, opID, serverID string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), tunnelTimeout)
		defer cancel()
		s.ops.Wake()
		done, err := s.ops.Await(ctx, opID, serverID, nil)
		status, msg, addr := "active", (*string)(nil), (*string)(nil)
		switch {
		case err != nil:
			status, msg = "failed", strptr(err.Error())
		case done.Status != "ok":
			status, msg = "failed", strptr(orDefault(derefStr(done.Error), done.Status))
		default:
			var r agentproto.TunnelResult
			if done.Result != nil && json.Unmarshal([]byte(*done.Result), &r) == nil && r.Address != "" {
				addr = &r.Address
			}
		}
		_ = tenancy.Tx(context.WithoutCancel(ctx), s.a.SQLDB, tenancy.Scope{TeamID: teamID}, func(q *db.Queries, _ *sql.Tx) error {
			if err := q.SetTunnelState(ctx, db.SetTunnelStateParams{ID: t.ID, TeamID: teamID, Status: status, Address: addr, Error: msg}); err != nil {
				return err
			}
			if status == "active" && t.Kind == "frp" {
				s.publishServer(ctx, q, teamID, serverID)
			}
			return nil
		})
	}()
}

// ---- the edge: DNS + NPM for domains on a tunnelled server ---------------------------------------

// frpEdge is where a server behind an frp tunnel is reachable from the edge, if it has one.
type frpEdge struct {
	publicIP, edgeHost string
	port               int
}

func (s *Service) edgeOf(ctx context.Context, q *db.Queries, teamID, serverID string) (*frpEdge, bool) {
	ts, err := q.TunnelsOfServer(ctx, db.TunnelsOfServerParams{ServerID: serverID, TeamID: teamID})
	if err != nil {
		return nil, false
	}
	for _, t := range ts {
		if t.Kind != "frp" || t.Status != "active" || !t.RemotePort.Valid {
			continue
		}
		_, cfg, _, err := s.credentialOf(ctx, q, teamID, t.CredentialID)
		if err != nil {
			return nil, false
		}
		edge := cfg["edge_host"]
		if edge == "" {
			edge = cfg["server_addr"]
		}
		return &frpEdge{publicIP: cfg["public_ip"], edgeHost: edge, port: int(t.RemotePort.Int32)}, true
	}
	return nil, false
}

// publishHost makes hostname reachable through the server's tunnel: a proxied Cloudflare record at
// the frps public IP (team Cloudflare credential) and an NPM proxy host to the tunnel port (team
// npm credential). Reports whether DNS was handled (so the caller need not point it elsewhere).
func (s *Service) publishHost(ctx context.Context, q *db.Queries, teamID, serverID, hostname string) bool {
	if agentproto.IsWildcardHost(hostname) {
		return false // a wildcard's DNS is the operator's (AddDomain)
	}
	e, ok := s.edgeOf(ctx, q, teamID, serverID)
	if !ok {
		return false
	}
	// Only hostnames in one of the team's Cloudflare zones are public: a test name (*.localhost,
	// *.test) never reaches DNS or the edge.
	cf, _, err := s.teamZone(ctx, q, teamID, hostname)
	if err != nil {
		return false
	}
	if e.publicIP != "" {
		cctx, cancel := context.WithTimeout(ctx, dnsTimeout)
		if _, _, err := cf.PointHost(cctx, hostname, e.publicIP, true); err != nil {
			s.log.Warn("tunnel edge: DNS failed", "host", hostname, "err", err)
		}
		cancel()
	}
	// NPM reloads nginx on every change, which can take a while: do it after the request returns.
	if c, cert := s.teamNPM(ctx, q, teamID); c != nil {
		edge := *e
		go func() {
			cctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			if err := c.Route(cctx, hostname, edge.edgeHost, edge.port, cert); err != nil {
				s.log.Warn("tunnel edge: NPM failed", "host", hostname, "err", err)
			}
		}()
	}
	return true
}

// unpublishHost removes what publishHost made (only Hosbah's own records and proxy hosts).
func (s *Service) unpublishHost(ctx context.Context, q *db.Queries, teamID, hostname string) {
	if c, _ := s.teamNPM(ctx, q, teamID); c != nil {
		go func() {
			cctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			if err := c.Unroute(cctx, hostname); err != nil {
				s.log.Warn("tunnel edge: NPM unroute failed", "host", hostname, "err", err)
			}
		}()
	}
}

// publishServer publishes every hostname already routed on a server (after its tunnel came up).
func (s *Service) publishServer(ctx context.Context, q *db.Queries, teamID, serverID string) {
	apps, err := q.ListAppsOfTeam(ctx, teamID)
	if err == nil {
		for _, a := range apps {
			if a.ServerID != serverID {
				continue
			}
			ds, _ := q.ListDomainsOfApp(ctx, db.ListDomainsOfAppParams{AppID: a.ID, TeamID: teamID})
			for _, d := range ds {
				s.publishHost(ctx, q, teamID, serverID, d.Hostname)
			}
		}
	}
	if ps, err := q.ListProxyHostsOfServer(ctx, db.ListProxyHostsOfServerParams{ServerID: serverID, TeamID: teamID}); err == nil {
		for _, p := range ps {
			s.publishHost(ctx, q, teamID, serverID, p.Hostname)
		}
	}
}

// teamNPM returns a logged-in-on-demand NPM client for the team's npm credential, and the
// certificate id to put on proxy hosts.
func (s *Service) teamNPM(ctx context.Context, q *db.Queries, teamID string) (*npm.Client, int) {
	creds, err := q.ListCredentialsOfTeam(ctx, teamID)
	if err != nil {
		return nil, 0
	}
	for _, c := range creds {
		if c.Provider != "npm" {
			continue
		}
		_, cfg, secret, err := s.credentialOf(ctx, q, teamID, c.ID)
		if err != nil {
			return nil, 0
		}
		cert, _ := strconv.Atoi(cfg["certificate_id"])
		return npm.New(cfg["api_url"], cfg["email"], secret), cert
	}
	return nil, 0
}


func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func strptr(s string) *string { return &s }

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// publishTunnelled publishes hostname through the server's tunnel, if it has one, and reports
// whether it may count as verified: published, and its zone is in the team's Cloudflare.
func (s *Service) publishTunnelled(ctx context.Context, q *db.Queries, teamID, serverID, hostname string) bool {
	// publishHost only publishes hostnames whose zone the team's Cloudflare holds, so published is verified.
	return s.publishHost(ctx, q, teamID, serverID, hostname)
}
