package deploy

import (
	"context"
	"sort"
	"strings"

	"github.com/fadymondy/hosbah/internal/cloudflare"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
The DNS zone manager (MH-578 E8): unlike dns.go/teamdns.go, which only ever touch the one record
that points an app's own hostname (or a proxy host's) at a server, this lets a team browse every
zone its own Cloudflare credentials can see and manage any record in it — MX, TXT, SRV, CAA, the
lot, not just A/AAAA/CNAME of the team's own hostnames.

Because it can reach any record of the zone, a hostname Hosbah itself put there (an app domain, a
proxy host, or mail DNS — internal/mail/dns.go) is flagged with ManagedKind so the UI can warn
before an edit or delete breaks it. Nothing here is refused outright: it is still the team's own
zone, and Hosbah does not own every record in it the way it owns the one record dns.go writes.
*/

// DNSManagedKind labels a hostname something else in Hosbah already manages in DNS.
const (
	DNSManagedApp   = "app_domain" // one of an app's own hostnames (dns.go)
	DNSManagedProxy = "proxy_host" // a Caddy proxy manager hostname (proxyhosts.go)
	DNSManagedMail  = "mail"       // MX/SPF/DMARC/DKIM of a team mail domain (internal/mail/dns.go)
)

// DNSZone is one zone a team's Cloudflare credential can see.
type DNSZone struct {
	ID             string
	Name           string
	Status         string
	CredentialID   string
	CredentialName string
}

// DNSZoneRecord is one record of a zone, annotated with what Hosbah knows about the hostname.
type DNSZoneRecord struct {
	cloudflare.Record
	ManagedByHosbah bool   // carries Hosbah's "managed by Hosbah" comment (created through dns.go/proxyhosts.go)
	ManagedKind     string // "", DNSManagedApp, DNSManagedProxy or DNSManagedMail — Hosbah put this hostname in DNS
}

// DNSZoneRecordInput is what a caller may set on a record.
type DNSZoneRecordInput struct {
	Type     string
	Name     string
	Content  string
	TTL      int // 0/1 = automatic
	Priority int // MX, SRV
	Proxied  *bool
}

// dnsRecordTypes are the record types the zone manager edits.
var dnsRecordTypes = map[string]bool{"A": true, "AAAA": true, "CNAME": true, "TXT": true, "MX": true, "SRV": true, "CAA": true}

// ValidDNSRecordType reports whether typ is one of the record types the zone manager supports.
func ValidDNSRecordType(typ string) bool { return dnsRecordTypes[strings.ToUpper(typ)] }

func (in DNSZoneRecordInput) validate() error {
	typ := strings.ToUpper(strings.TrimSpace(in.Type))
	if !ValidDNSRecordType(typ) {
		return invalid("type must be one of A, AAAA, CNAME, TXT, MX, SRV, CAA")
	}
	if strings.TrimSpace(in.Name) == "" {
		return invalid("name is required")
	}
	if strings.TrimSpace(in.Content) == "" {
		return invalid("content is required")
	}
	if typ == "MX" && in.Priority <= 0 {
		return invalid("an MX record needs a priority")
	}
	return nil
}

// ListDNSZones lists every zone the team's Cloudflare credentials can see. One credential failing
// to answer does not hide the others'.
func (s *Service) ListDNSZones(ctx context.Context, q *db.Queries, teamID string) ([]DNSZone, error) {
	cfs, err := s.teamCloudflares(ctx, q, teamID)
	if err != nil {
		return nil, err
	}
	out := []DNSZone{}
	for _, t := range cfs {
		cctx, cancel := context.WithTimeout(ctx, dnsTimeout)
		zs, zerr := t.cf.Zones(cctx)
		cancel()
		if zerr != nil {
			continue
		}
		for _, z := range zs {
			out = append(out, DNSZone{ID: z.ID, Name: z.Name, Status: z.Status, CredentialID: t.id, CredentialName: t.name})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CredentialName != out[j].CredentialName {
			return out[i].CredentialName < out[j].CredentialName
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// dnsCredential finds one Cloudflare credential of the team by id or name.
func (s *Service) dnsCredential(ctx context.Context, q *db.Queries, teamID, ref string) (*teamCF, error) {
	cfs, err := s.teamCloudflares(ctx, q, teamID)
	if err != nil {
		return nil, err
	}
	for _, t := range cfs {
		if t.id == ref || t.name == ref {
			return t, nil
		}
	}
	return nil, notFound("Cloudflare credential")
}

// dnsZone resolves ref (id or name) to a zone of t's account.
func (t *teamCF) dnsZone(ctx context.Context, ref string) (cloudflare.Zone, error) {
	cctx, cancel := context.WithTimeout(ctx, dnsTimeout)
	zs, err := t.cf.Zones(cctx)
	cancel()
	if err != nil {
		return cloudflare.Zone{}, &ActionError{Status: 502, Msg: "Cloudflare did not answer: " + dnsErrText(err)}
	}
	for _, z := range zs {
		if z.ID == ref || strings.EqualFold(z.Name, ref) {
			return z, nil
		}
	}
	return cloudflare.Zone{}, notFound("zone")
}

// managedHostnames is every hostname Hosbah itself already put in the team's DNS: app domains,
// proxy hosts, and mail DNS (apex, _dmarc and the DKIM selector of each mail domain).
func (s *Service) managedHostnames(ctx context.Context, q *db.Queries, teamID string) (map[string]string, error) {
	out := map[string]string{}
	domains, err := q.ListDomainsOfTeam(ctx, teamID)
	if err != nil {
		return nil, err
	}
	for _, d := range domains {
		out[strings.ToLower(d.Hostname)] = DNSManagedApp
	}
	proxies, err := q.ListProxyHostsOfTeam(ctx, teamID)
	if err != nil {
		return nil, err
	}
	for _, p := range proxies {
		h := strings.ToLower(p.Hostname)
		if _, exists := out[h]; !exists {
			out[h] = DNSManagedProxy
		}
	}
	mds, err := q.ListMailDomainsOfTeam(ctx, teamID)
	if err != nil {
		return nil, err
	}
	for _, m := range mds {
		dom := strings.ToLower(m.Domain)
		for _, h := range []string{dom, "_dmarc." + dom} {
			if _, exists := out[h]; !exists {
				out[h] = DNSManagedMail
			}
		}
		if m.DkimSelector != nil {
			h := strings.ToLower(*m.DkimSelector) + "._domainkey." + dom
			if _, exists := out[h]; !exists {
				out[h] = DNSManagedMail
			}
		}
	}
	return out, nil
}

func annotate(recs []cloudflare.Record, managed map[string]string) []DNSZoneRecord {
	out := make([]DNSZoneRecord, 0, len(recs))
	for _, r := range recs {
		name := strings.ToLower(strings.TrimSuffix(r.Name, "."))
		out = append(out, DNSZoneRecord{Record: r, ManagedByHosbah: cloudflare.Managed(r), ManagedKind: managed[name]})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Type < out[j].Type
	})
	return out
}

// ListDNSZoneRecords lists every record of a zone of the team's, annotated with what Hosbah itself
// manages there.
func (s *Service) ListDNSZoneRecords(ctx context.Context, q *db.Queries, teamID, credRef, zoneRef string) (DNSZone, []DNSZoneRecord, error) {
	t, err := s.dnsCredential(ctx, q, teamID, credRef)
	if err != nil {
		return DNSZone{}, nil, err
	}
	z, err := t.dnsZone(ctx, zoneRef)
	if err != nil {
		return DNSZone{}, nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, dnsTimeout)
	recs, err := t.cf.AllRecords(cctx, z.ID)
	cancel()
	if err != nil {
		return DNSZone{}, nil, &ActionError{Status: 502, Msg: "Cloudflare did not answer: " + dnsErrText(err)}
	}
	managed, err := s.managedHostnames(ctx, q, teamID)
	if err != nil {
		return DNSZone{}, nil, err
	}
	zone := DNSZone{ID: z.ID, Name: z.Name, Status: z.Status, CredentialID: t.id, CredentialName: t.name}
	return zone, annotate(recs, managed), nil
}

// CreateDNSZoneRecord adds a record to a zone of the team's. Audited.
func (s *Service) CreateDNSZoneRecord(ctx context.Context, q *db.Queries, teamID string, actor Actor, credRef, zoneRef string, in DNSZoneRecordInput) (DNSZone, DNSZoneRecord, error) {
	if err := in.validate(); err != nil {
		return DNSZone{}, DNSZoneRecord{}, err
	}
	t, err := s.dnsCredential(ctx, q, teamID, credRef)
	if err != nil {
		return DNSZone{}, DNSZoneRecord{}, err
	}
	z, err := t.dnsZone(ctx, zoneRef)
	if err != nil {
		return DNSZone{}, DNSZoneRecord{}, err
	}
	cctx, cancel := context.WithTimeout(ctx, dnsTimeout)
	rec, err := t.cf.CreateRecord(cctx, z.ID, cloudflare.Record{Type: strings.ToUpper(in.Type), Name: in.Name, Content: in.Content,
		TTL: in.TTL, Priority: in.Priority, Proxied: in.Proxied})
	cancel()
	if err != nil {
		return DNSZone{}, DNSZoneRecord{}, &ActionError{Status: 502, Msg: "Cloudflare refused the record: " + dnsErrText(err)}
	}
	zone := DNSZone{ID: z.ID, Name: z.Name, Status: z.Status, CredentialID: t.id, CredentialName: t.name}
	managed, err := s.managedHostnames(ctx, q, teamID)
	if err != nil {
		return DNSZone{}, DNSZoneRecord{}, err
	}
	out := annotate([]cloudflare.Record{rec}, managed)[0]
	extra := map[string]any{"zone": z.Name, "credential": t.name, "type": rec.Type, "name": rec.Name, "content": rec.Content}
	return zone, out, tenancy.Record(ctx, q, teamID, actor.UserID, "dns.zone_record_created", "credential", t.id, actor.audit(extra))
}

// UpdateDNSZoneRecord changes a record's content, TTL, priority or proxied flag. Audited.
func (s *Service) UpdateDNSZoneRecord(ctx context.Context, q *db.Queries, teamID string, actor Actor, credRef, zoneRef, recordID string, in DNSZoneRecordInput) (DNSZone, DNSZoneRecord, error) {
	t, err := s.dnsCredential(ctx, q, teamID, credRef)
	if err != nil {
		return DNSZone{}, DNSZoneRecord{}, err
	}
	z, err := t.dnsZone(ctx, zoneRef)
	if err != nil {
		return DNSZone{}, DNSZoneRecord{}, err
	}
	if strings.TrimSpace(recordID) == "" {
		return DNSZone{}, DNSZoneRecord{}, notFound("record")
	}
	patch := cloudflare.RecordPatch{}
	if in.Content != "" {
		patch.Content = &in.Content
	}
	if in.TTL != 0 {
		patch.TTL = &in.TTL
	}
	if in.Priority != 0 {
		patch.Priority = &in.Priority
	}
	if in.Proxied != nil {
		patch.Proxied = in.Proxied
	}
	cctx, cancel := context.WithTimeout(ctx, dnsTimeout)
	rec, err := t.cf.PatchRecord(cctx, z.ID, recordID, patch)
	cancel()
	if err != nil {
		return DNSZone{}, DNSZoneRecord{}, &ActionError{Status: 502, Msg: "Cloudflare refused the record: " + dnsErrText(err)}
	}
	zone := DNSZone{ID: z.ID, Name: z.Name, Status: z.Status, CredentialID: t.id, CredentialName: t.name}
	managed, err := s.managedHostnames(ctx, q, teamID)
	if err != nil {
		return DNSZone{}, DNSZoneRecord{}, err
	}
	out := annotate([]cloudflare.Record{rec}, managed)[0]
	extra := map[string]any{"zone": z.Name, "credential": t.name, "type": rec.Type, "name": rec.Name, "content": rec.Content, "managed_by_hosbah": out.ManagedByHosbah}
	return zone, out, tenancy.Record(ctx, q, teamID, actor.UserID, "dns.zone_record_updated", "credential", t.id, actor.audit(extra))
}

// DeleteDNSZoneRecord removes one record of a zone. Audited.
func (s *Service) DeleteDNSZoneRecord(ctx context.Context, q *db.Queries, teamID string, actor Actor, credRef, zoneRef, recordID string) error {
	t, err := s.dnsCredential(ctx, q, teamID, credRef)
	if err != nil {
		return err
	}
	z, err := t.dnsZone(ctx, zoneRef)
	if err != nil {
		return err
	}
	if strings.TrimSpace(recordID) == "" {
		return notFound("record")
	}
	cctx, cancel := context.WithTimeout(ctx, dnsTimeout)
	err = t.cf.DeleteRecord(cctx, z.ID, recordID)
	cancel()
	if err != nil {
		return &ActionError{Status: 502, Msg: "Cloudflare refused removing the record: " + dnsErrText(err)}
	}
	return tenancy.Record(ctx, q, teamID, actor.UserID, "dns.zone_record_deleted", "credential", t.id,
		actor.audit(map[string]any{"zone": z.Name, "credential": t.name, "record": recordID}))
}
