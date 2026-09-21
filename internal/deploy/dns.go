package deploy

import (
	"context"
	"net"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	"github.com/fadymondy/hosbah/internal/cloudflare"
	"github.com/fadymondy/hosbah/internal/crypto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/provisioning"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
The "DNS & SSL" section of a site.

DNS: for each of the app's hostnames, the Cloudflare record it has, set against the one it should
have — an A/AAAA record at the app's server's public address, exactly what wireTeamDNS creates when
a domain is added. Only the team's own Cloudflare credentials are used, and only the app's own
hostnames are ever addressed: a hostname that is not one of the app's domains is "not found", so
these actions cannot reach any other record of the zone. A record Hosbah did not create (no
"managed by Hosbah" comment) is never rewritten without takeover and never deleted.

SSL: caddy.certs, a read-only agent operation (see internal/agent/certs.go).
*/

// SetCloudflareOptions configures every team Cloudflare client built from here on (tests point
// them at a fake API).
func (s *Service) SetCloudflareOptions(opts ...cloudflare.Option) { s.cfOpts = opts }

// DNS statuses of a hostname.
const (
	DNSOK        = "ok"        // the record points at the server
	DNSMissing   = "missing"   // the zone is the team's, no record yet
	DNSMismatch  = "mismatch"  // a record exists and points elsewhere
	DNSUnmanaged = "unmanaged" // none of the team's Cloudflare credentials holds the zone
	DNSPrivate   = "private"   // the server has only a private address: no public record applies
)

// DNSRow is one hostname of an app, as the team's Cloudflare sees it.
type DNSRow struct {
	Hostname        string
	Zone            string // "" when no credential holds it
	CredentialID    string
	CredentialName  string
	Record          *cloudflare.Record
	RecordManaged   bool // Hosbah created Record
	ExpectedType    string
	ExpectedContent string
	Status          string
	Err             string

	cf     *cloudflare.Client
	zoneID string
}

// AppDNS is the DNS of every hostname of an app.
type AppDNS struct {
	Server db.Server
	Rows   []DNSRow
}

// teamCF is one Cloudflare credential of the team, its zones listed once per request.
type teamCF struct {
	id, name string
	cf       *cloudflare.Client
	zones    []cloudflare.Zone
	err      error
	loaded   bool
}

func (s *Service) teamCloudflares(ctx context.Context, q *db.Queries, teamID string) ([]*teamCF, error) {
	creds, err := q.ListCredentialsOfTeam(ctx, teamID)
	if err != nil {
		return nil, err
	}
	var (
		out []*teamCF
		dek []byte
	)
	for _, c := range creds {
		if c.Provider != "cloudflare" {
			continue
		}
		if dek == nil {
			if dek, err = s.ops.TeamDEK(ctx, q, teamID); err != nil {
				return nil, err
			}
		}
		tok, err := crypto.Open(dek, teamID, provisioning.CredentialContext(c.ID), c.SecretSealed)
		if err != nil {
			continue
		}
		out = append(out, &teamCF{id: c.ID, name: c.Name, cf: s.cloudflare(string(tok))})
	}
	return out, nil
}

// zoneOf is the longest zone of the credential that is hostname or a parent of it.
func (t *teamCF) zoneOf(ctx context.Context, hostname string) (cloudflare.Zone, bool) {
	if !t.loaded {
		cctx, cancel := context.WithTimeout(ctx, dnsTimeout)
		t.zones, t.err = t.cf.Zones(cctx)
		cancel()
		t.loaded = true
	}
	var best cloudflare.Zone
	for _, z := range t.zones {
		name := strings.ToLower(z.Name)
		if (hostname == name || strings.HasSuffix(hostname, "."+name)) && len(name) > len(best.Name) {
			best = z
		}
	}
	return best, best.ID != ""
}

// expectedRecord is what a hostname of an app on srv should point at: the server's public address
// (the rule wireTeamDNSTo follows). ok is false when the server has none.
func expectedRecord(srv db.Server) (typ, content string, ok bool) {
	if srv.IpAddress == nil {
		return "", "", false
	}
	ip := net.ParseIP(strings.TrimSpace(*srv.IpAddress))
	if ip == nil || ip.IsPrivate() || ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() {
		return "", "", false
	}
	if ip.To4() != nil {
		return "A", ip.String(), true
	}
	return "AAAA", ip.String(), true
}

func (s *Service) dnsRow(ctx context.Context, cfs []*teamCF, srv db.Server, hostname string) DNSRow {
	row := DNSRow{Hostname: hostname}
	typ, content, public := expectedRecord(srv)
	row.ExpectedType, row.ExpectedContent = typ, content

	var lookupErr error
	for _, t := range cfs {
		z, ok := t.zoneOf(ctx, hostname)
		if !ok {
			if t.err != nil && lookupErr == nil {
				lookupErr = t.err
			}
			continue
		}
		row.Zone, row.zoneID, row.cf, row.CredentialID, row.CredentialName = z.Name, z.ID, t.cf, t.id, t.name
		break
	}
	if row.cf == nil {
		row.Status = DNSUnmanaged
		if !public {
			row.Status = DNSPrivate
		}
		if lookupErr != nil {
			row.Err = dnsErrText(lookupErr)
		}
		return row
	}

	cctx, cancel := context.WithTimeout(ctx, dnsTimeout)
	recs, err := row.cf.Records(cctx, row.zoneID, hostname, "")
	cancel()
	if err != nil {
		// The zone is the team's but its records could not be read: the status is not known, and
		// "missing" would invite a write. Say what is known and carry the error.
		row.Status, row.Err = DNSMismatch, dnsErrText(err)
		if !public {
			row.Status = DNSPrivate
		}
		return row
	}
	var first *cloudflare.Record
	for i := range recs {
		r := recs[i]
		if r.Type != "A" && r.Type != "AAAA" && r.Type != "CNAME" {
			continue
		}
		if public && r.Type == typ && sameAddress(r.Content, content) {
			first = &recs[i]
			break
		}
		if first == nil {
			first = &recs[i]
		}
	}
	row.Record = first
	if first != nil {
		row.RecordManaged = cloudflare.Managed(*first)
	}
	switch {
	case !public:
		row.Status = DNSPrivate
	case first == nil:
		row.Status = DNSMissing
	case first.Type == typ && sameAddress(first.Content, content):
		row.Status = DNSOK
	default:
		row.Status = DNSMismatch
	}
	return row
}

func sameAddress(a, b string) bool {
	ia, ib := net.ParseIP(a), net.ParseIP(b)
	return ia != nil && ib != nil && ia.Equal(ib)
}

// dnsErrText is a Cloudflare failure as a member may read it (the client never puts a token in it).
func dnsErrText(err error) string { return clipRunes(err.Error(), 300) }

// DNSOfApp lists the DNS of every hostname of the app. A Cloudflare failure is that row's Err.
func (s *Service) DNSOfApp(ctx context.Context, q *db.Queries, teamID string, a db.App) (AppDNS, error) {
	srv, err := q.ServerInTeam(ctx, db.ServerInTeamParams{ID: a.ServerID, TeamID: teamID})
	if err != nil {
		return AppDNS{}, notFound("server")
	}
	out := AppDNS{Server: srv, Rows: []DNSRow{}}
	domains, err := q.ListDomainsOfApp(ctx, db.ListDomainsOfAppParams{AppID: a.ID, TeamID: teamID})
	if err != nil {
		return out, err
	}
	if len(domains) == 0 {
		return out, nil
	}
	cfs, err := s.teamCloudflares(ctx, q, teamID)
	if err != nil {
		return out, err
	}
	for _, d := range domains {
		out.Rows = append(out.Rows, s.dnsRow(ctx, cfs, srv, d.Hostname))
	}
	return out, nil
}

// appHostname resolves ref to one of the app's OWN hostnames (exact, after normalising) — the only
// names the DNS actions may touch.
func appHostname(ctx context.Context, q *db.Queries, teamID string, a db.App, ref string) (string, error) {
	host := NormalizeHostname(ref)
	if !agentproto.ValidHostname(host) {
		return "", notFound("hostname")
	}
	domains, err := q.ListDomainsOfApp(ctx, db.ListDomainsOfAppParams{AppID: a.ID, TeamID: teamID})
	if err != nil {
		return "", err
	}
	for _, d := range domains {
		if d.Hostname == host {
			return host, nil
		}
	}
	return "", notFound("hostname")
}

func (s *Service) dnsRowOf(ctx context.Context, q *db.Queries, teamID string, a db.App, hostname string) (DNSRow, error) {
	host, err := appHostname(ctx, q, teamID, a, hostname)
	if err != nil {
		return DNSRow{}, err
	}
	srv, err := q.ServerInTeam(ctx, db.ServerInTeamParams{ID: a.ServerID, TeamID: teamID})
	if err != nil {
		return DNSRow{}, notFound("server")
	}
	cfs, err := s.teamCloudflares(ctx, q, teamID)
	if err != nil {
		return DNSRow{}, err
	}
	row := s.dnsRow(ctx, cfs, srv, host)
	if row.cf == nil && row.Err == "" {
		return row, invalid("none of the team's Cloudflare credentials holds the zone of " + host + " — add one (Credentials), or manage this record at your DNS provider")
	}
	if row.Err != "" {
		return row, &ActionError{Status: 502, Msg: "Cloudflare did not answer for " + host + ": " + row.Err}
	}
	return row, nil
}

// SetDNSRecord makes hostname (one of the app's) point at the app's server: it creates the record
// when missing, repairs it when it points elsewhere, and applies proxied when given. A record that
// points elsewhere and is not Hosbah's is only rewritten with takeover. Audited.
func (s *Service) SetDNSRecord(ctx context.Context, q *db.Queries, teamID string, actor Actor, a db.App, hostname string, proxied *bool, takeover bool) (DNSRow, error) {
	row, err := s.dnsRowOf(ctx, q, teamID, a, hostname)
	if err != nil {
		return row, err
	}
	if row.Status == DNSPrivate {
		return row, conflict("the app's server has only a private address, so no public DNS record applies — reach it through a tunnel instead")
	}
	if row.Status == DNSMismatch && !row.RecordManaged && !takeover {
		return row, conflict(row.Hostname + " already has a " + row.Record.Type + " record (" + row.Record.Content +
			") that Hosbah did not create — pass takeover to replace it")
	}
	cctx, cancel := context.WithTimeout(ctx, 2*dnsTimeout)
	defer cancel()
	action := "unchanged"
	mark := cloudflare.ManagedComment
	switch {
	case row.Record == nil:
		p := false
		if proxied != nil {
			p = *proxied
		}
		rec, err := row.cf.CreateRecord(cctx, row.zoneID, cloudflare.Record{Type: row.ExpectedType, Name: row.Hostname, Content: row.ExpectedContent, Proxied: &p})
		if err != nil {
			return row, &ActionError{Status: 502, Msg: "Cloudflare refused the record: " + dnsErrText(err)}
		}
		row.Record, action = &rec, "created"
	case row.Status == DNSMismatch && row.Record.Type != row.ExpectedType:
		// A CNAME cannot become an A record in place (and cannot sit beside one): replace it.
		p := row.Record.Proxied
		if proxied != nil {
			p = proxied
		}
		if err := row.cf.DeleteRecord(cctx, row.zoneID, row.Record.ID); err != nil {
			return row, &ActionError{Status: 502, Msg: "Cloudflare refused removing the old record: " + dnsErrText(err)}
		}
		rec, err := row.cf.CreateRecord(cctx, row.zoneID, cloudflare.Record{Type: row.ExpectedType, Name: row.Hostname, Content: row.ExpectedContent, Proxied: p})
		if err != nil {
			return row, &ActionError{Status: 502, Msg: "Cloudflare refused the record: " + dnsErrText(err)}
		}
		row.Record, action = &rec, "replaced"
	case row.Status == DNSMismatch:
		content := row.ExpectedContent
		rec, err := row.cf.PatchRecord(cctx, row.zoneID, row.Record.ID, cloudflare.RecordPatch{Content: &content, Proxied: proxied, Comment: &mark})
		if err != nil {
			return row, &ActionError{Status: 502, Msg: "Cloudflare refused the record: " + dnsErrText(err)}
		}
		row.Record, action = &rec, "repaired"
	case proxied != nil && (row.Record.Proxied == nil || *row.Record.Proxied != *proxied):
		// Only the proxy switch of a record that already points here: whose it is does not change.
		rec, err := row.cf.PatchRecord(cctx, row.zoneID, row.Record.ID, cloudflare.RecordPatch{Proxied: proxied})
		if err != nil {
			return row, &ActionError{Status: 502, Msg: "Cloudflare refused the record: " + dnsErrText(err)}
		}
		row.Record, action = &rec, "proxied"
	}
	row.RecordManaged, row.Status = cloudflare.Managed(*row.Record), DNSOK
	extra := map[string]any{"hostname": row.Hostname, "zone": row.Zone, "type": row.Record.Type, "content": row.Record.Content, "action": action}
	if row.Record.Proxied != nil {
		extra["proxied"] = *row.Record.Proxied
	}
	if takeover {
		extra["takeover"] = true
	}
	return row, tenancy.Record(ctx, q, teamID, actor.UserID, "dns.record_set", "app", a.ID, actor.audit(extra))
}

// RemoveDNSRecord deletes hostname's record, only if Hosbah created it. Audited.
func (s *Service) RemoveDNSRecord(ctx context.Context, q *db.Queries, teamID string, actor Actor, a db.App, hostname string) error {
	row, err := s.dnsRowOf(ctx, q, teamID, a, hostname)
	if err != nil {
		return err
	}
	if row.Record == nil {
		return notFound("DNS record")
	}
	if !row.RecordManaged {
		return conflict(row.Hostname + " has a record Hosbah did not create; it is left alone — remove it at Cloudflare")
	}
	cctx, cancel := context.WithTimeout(ctx, dnsTimeout)
	defer cancel()
	if err := row.cf.DeleteRecord(cctx, row.zoneID, row.Record.ID); err != nil {
		return &ActionError{Status: 502, Msg: "Cloudflare refused removing the record: " + dnsErrText(err)}
	}
	return tenancy.Record(ctx, q, teamID, actor.UserID, "dns.record_removed", "app", a.ID,
		actor.audit(map[string]any{"hostname": row.Hostname, "zone": row.Zone, "type": row.Record.Type, "content": row.Record.Content}))
}

// ---- certificates -----------------------------------------------------------------------------------

// AppCertificates queues caddy.certs for the app's hostnames on its server. No operation (an empty
// id) when the app has no hostnames.
func (s *Service) AppCertificates(ctx context.Context, q *db.Queries, teamID string, actor Actor, a db.App) (db.Operation, []string, error) {
	domains, err := q.ListDomainsOfApp(ctx, db.ListDomainsOfAppParams{AppID: a.ID, TeamID: teamID})
	if err != nil {
		return db.Operation{}, nil, err
	}
	hosts := []string{}
	for _, d := range domains {
		if len(hosts) < agentproto.MaxCertHostnames {
			hosts = append(hosts, d.Hostname)
		}
	}
	if len(hosts) == 0 {
		return db.Operation{}, hosts, nil
	}
	op, err := s.ServerCertificates(ctx, q, teamID, actor, a.ServerID, hosts)
	return op, hosts, err
}

// ServerCertificates queues caddy.certs on a server of the team. Hostnames are validated here, so a
// bad one never reaches the agent (which validates again).
func (s *Service) ServerCertificates(ctx context.Context, q *db.Queries, teamID string, actor Actor, serverID string, hostnames []string) (db.Operation, error) {
	p := agentproto.CaddyCertsParams{Hostnames: hostnames}
	if err := p.Validate(); err != nil {
		return db.Operation{}, invalid(err.Error())
	}
	srv, err := q.ServerInTeam(ctx, db.ServerInTeamParams{ID: serverID, TeamID: teamID})
	if err != nil {
		return db.Operation{}, notFound("server")
	}
	if err := s.githubServer(srv); err != nil {
		return db.Operation{}, err
	}
	if !s.ops.Connected(srv.ID) && !ops.Online(srv.LastSeenAt, time.Now()) {
		return db.Operation{}, conflict(srv.Name + " is offline — its certificates can be read once its agent is connected")
	}
	return s.githubEnqueue(ctx, q, teamID, actor, srv, agentproto.KindCaddyCerts, p)
}

// CleanCerts keeps what an agent reported only for the hostnames asked, one entry each, in order.
func CleanCerts(hostnames []string, in []agentproto.CaddyCert) []agentproto.CaddyCert {
	by := map[string]agentproto.CaddyCert{}
	for _, c := range in {
		if _, seen := by[c.Hostname]; !seen {
			by[c.Hostname] = c
		}
	}
	out := make([]agentproto.CaddyCert, 0, len(hostnames))
	for _, h := range hostnames {
		c, ok := by[h]
		if !ok {
			c = agentproto.CaddyCert{Hostname: h}
		}
		if !c.Found {
			c.Issuer, c.NotBefore, c.NotAfter, c.Managed = "", nil, nil, false
		}
		c.Issuer, c.Error = clipRunes(c.Issuer, 200), clipRunes(c.Error, 300)
		out = append(out, c)
	}
	return out
}
