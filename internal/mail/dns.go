package mail

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/cloudflare"
	"github.com/fadymondy/hosbah/internal/crypto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/provisioning"
)

// MH-577 I3: the DNS records a mail domain needs (MX, SPF, DKIM, DMARC) plus a checker that resolves
// each one and a publisher that writes them through the team's Cloudflare credential, the same way
// internal/deploy/teamdns.go wires an app's custom domain.

// Resolver is the net.Resolver surface this package needs; overridable in tests.
type Resolver interface {
	LookupTXT(ctx context.Context, name string) ([]string, error)
	LookupMX(ctx context.Context, name string) ([]*net.MX, error)
	LookupAddr(ctx context.Context, addr string) ([]string, error)
}

// resolver is the Resolver the service uses; net.DefaultResolver unless a test overrides it.
var resolver Resolver = net.DefaultResolver

const dnsTimeout = 8 * time.Second

// Record is one DNS record a mail domain needs, and (after Check) its live status.
type Record struct {
	Type     string `json:"type" enum:"MX,TXT"`
	Name     string `json:"name" doc:"The record's owner name, e.g. example.com or _dmarc.example.com."`
	Value    string `json:"value"`
	Priority int    `json:"priority,omitempty"`
	Purpose  string `json:"purpose" enum:"mx,spf,dkim,dmarc"`
	Status   string `json:"status" enum:"unknown,ok,missing,mismatch"`
	Found    string `json:"found,omitempty" doc:"What DNS actually holds, when it differs from value."`
}

// DomainDNS is Records() + the PTR check, as the API shows it.
type DomainDNS struct {
	Records []Record `json:"records"`
	PTR     PTRCheck `json:"ptr"`
}

// PTRCheck is the reverse-DNS check of the mail server's IP against its own hostname.
type PTRCheck struct {
	ServerIP string `json:"server_ip,omitempty"`
	Hostname string `json:"hostname"`
	Status   string `json:"status" enum:"unknown,ok,missing,mismatch"`
	Found    string `json:"found,omitempty"`
}

// dkimRecordName is the DKIM TXT record's owner name.
func dkimRecordName(selector, domain string) string { return selector + "._domainkey." + domain }

// Records builds the DNS records dom needs, from the domain's own state (its DKIM key once Stalwart
// generated it) and the mail server's hostname.
func Records(dom db.MailDomain, mailHostname string) []Record {
	recs := []Record{
		{Type: "MX", Name: dom.Domain, Value: mailHostname + ".", Priority: 10, Purpose: "mx", Status: "unknown"},
		{Type: "TXT", Name: dom.Domain, Value: "v=spf1 mx ~all", Purpose: "spf", Status: "unknown"},
		{Type: "TXT", Name: "_dmarc." + dom.Domain, Value: "v=DMARC1; p=quarantine; rua=mailto:postmaster@" + dom.Domain, Purpose: "dmarc", Status: "unknown"},
	}
	if dom.DkimSelector != nil && dom.DkimPublicKey != nil {
		recs = append(recs, Record{
			Type: "TXT", Name: dkimRecordName(*dom.DkimSelector, dom.Domain),
			Value: "v=DKIM1; k=ed25519; p=" + *dom.DkimPublicKey, Purpose: "dkim", Status: "unknown",
		})
	}
	return recs
}

// CheckDNS resolves every record Records() lists and reports ok/missing/mismatch for each, plus the
// PTR check of the mail server's IP against its hostname.
func CheckDNS(ctx context.Context, dom db.MailDomain, mailHostname, serverIP string) DomainDNS {
	recs := Records(dom, mailHostname)
	for i := range recs {
		checkRecord(ctx, &recs[i])
	}
	return DomainDNS{Records: recs, PTR: checkPTR(ctx, serverIP, mailHostname)}
}

func checkRecord(ctx context.Context, r *Record) {
	cctx, cancel := context.WithTimeout(ctx, dnsTimeout)
	defer cancel()
	switch r.Type {
	case "MX":
		mxs, err := resolver.LookupMX(cctx, r.Name)
		if err != nil || len(mxs) == 0 {
			r.Status = "missing"
			return
		}
		want := strings.TrimSuffix(strings.ToLower(r.Value), ".")
		for _, mx := range mxs {
			if strings.TrimSuffix(strings.ToLower(mx.Host), ".") == want {
				r.Status = "ok"
				return
			}
		}
		r.Status = "mismatch"
		r.Found = mxs[0].Host
	case "TXT":
		txts, err := resolver.LookupTXT(cctx, r.Name)
		if err != nil || len(txts) == 0 {
			r.Status = "missing"
			return
		}
		for _, t := range txts {
			if strings.TrimSpace(t) == r.Value {
				r.Status = "ok"
				return
			}
		}
		r.Status = "mismatch"
		r.Found = txts[0]
	default:
		r.Status = "unknown"
	}
}

func checkPTR(ctx context.Context, serverIP, mailHostname string) PTRCheck {
	out := PTRCheck{ServerIP: serverIP, Hostname: mailHostname, Status: "unknown"}
	if serverIP == "" {
		return out
	}
	cctx, cancel := context.WithTimeout(ctx, dnsTimeout)
	defer cancel()
	names, err := resolver.LookupAddr(cctx, serverIP)
	if err != nil || len(names) == 0 {
		out.Status = "missing"
		return out
	}
	want := strings.TrimSuffix(strings.ToLower(mailHostname), ".") + "."
	for _, n := range names {
		if strings.ToLower(n) == want {
			out.Status = "ok"
			return out
		}
	}
	out.Status = "mismatch"
	out.Found = names[0]
	return out
}

// CheckDomainDNS resolves a mail domain's server (for its hostname and IP) and checks its records.
func (s *Service) CheckDomainDNS(ctx context.Context, q *db.Queries, teamID string, srv db.MailServer, dom db.MailDomain) (DomainDNS, error) {
	ip := ""
	if full, err := q.ServerInTeam(ctx, db.ServerInTeamParams{ID: srv.ServerID, TeamID: teamID}); err == nil && full.IpAddress != nil {
		ip = *full.IpAddress
	}
	return CheckDNS(ctx, dom, srv.Hostname, ip), nil
}

// ---- publish ------------------------------------------------------------------------------------

// PublishResult reports what Publish wrote or why it could not.
type PublishResult struct {
	Published []string `json:"published"`
	Skipped   []string `json:"skipped,omitempty"`
	Zone      string   `json:"zone,omitempty"`
}

// PublishDNS writes every record Records() lists to the team's Cloudflare zone for the domain, the
// same way internal/deploy/teamdns.go wires an app's custom domain. It requires a saved Cloudflare
// credential whose account holds the zone; otherwise it reports Skipped with a reason, never an
// error, so a caller without Cloudflare still sees the records to add by hand.
func (s *Service) PublishDNS(ctx context.Context, q *db.Queries, teamID string, dom db.MailDomain, mailHostname string) (PublishResult, error) {
	var out PublishResult
	cf, zone, err := s.teamZone(ctx, q, teamID, dom.Domain)
	if err != nil {
		if errors.Is(err, cloudflare.ErrNotFound) {
			out.Skipped = append(out.Skipped, "no Cloudflare credential holds this domain's zone")
			return out, nil
		}
		return out, err
	}
	out.Zone = zone.Name
	for _, r := range Records(dom, mailHostname) {
		cr := cloudflare.Record{Type: r.Type, Name: r.Name, Content: r.Value, Priority: r.Priority, TTL: 1}
		cctx, cancel := context.WithTimeout(ctx, dnsTimeout)
		_, err := cf.Upsert(cctx, zone.ID, cr)
		cancel()
		if err != nil {
			out.Skipped = append(out.Skipped, fmt.Sprintf("%s %s: %s", r.Type, r.Name, err.Error()))
			continue
		}
		out.Published = append(out.Published, r.Type+" "+r.Name)
	}
	return out, nil
}

// teamZone finds the team's Cloudflare credential whose account holds hostname (mirrors
// internal/deploy's unexported teamZone, kept separate since that package does not export it).
func (s *Service) teamZone(ctx context.Context, q *db.Queries, teamID, hostname string) (*cloudflare.Client, cloudflare.Zone, error) {
	creds, err := q.ListCredentialsOfTeam(ctx, teamID)
	if err != nil {
		return nil, cloudflare.Zone{}, err
	}
	var dek []byte
	for _, c := range creds {
		if c.Provider != "cloudflare" {
			continue
		}
		if dek == nil {
			if dek, err = s.teamDEK(ctx, q, teamID); err != nil {
				return nil, cloudflare.Zone{}, err
			}
		}
		tok, err := crypto.Open(dek, teamID, provisioning.CredentialContext(c.ID), c.SecretSealed)
		if err != nil {
			continue
		}
		cf := cloudflare.New(string(tok))
		cctx, cancel := context.WithTimeout(ctx, dnsTimeout)
		z, err := cf.ZoneFor(cctx, hostname)
		cancel()
		if err == nil {
			return cf, z, nil
		}
	}
	return nil, cloudflare.Zone{}, cloudflare.ErrNotFound
}
