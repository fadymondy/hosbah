package mail

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"

	db "github.com/fadymondy/hosbah/internal/db/gen"
)

// MH-577 I5: a deliverability report per mail domain — the DNS records dns.go already checks (MX,
// SPF, DKIM, DMARC, PTR) plus a DNSBL blacklist lookup of the mail server's public IPv4 against a
// small fixed list of well-known blocklists, done from the control plane with the same injectable
// Resolver dns.go uses.
//
// Spam filter toggle: not exposed here. Stalwart v0.13's generic /api/settings endpoint takes an
// arbitrary key path, but no key controlling its built-in spam filter's on/off state is documented
// in its openapi.yml or confirmed against its source; guessing one this package would then reload
// live risks silently mis-configuring the mail server, so this feature is left out on purpose. See
// SpamFilterNote below, which the API and UI surface verbatim.

// DNSBLZones is the fixed list of blocklists checked, in the exact order reported.
var DNSBLZones = []string{"zen.spamhaus.org", "bl.spamcop.net", "b.barracudacentral.org", "dnsbl.sorbs.net"}

// BlacklistCheck is one DNSBL zone's answer for the server's IP.
type BlacklistCheck struct {
	Zone   string `json:"zone"`
	Status string `json:"status" enum:"ok,listed,error"`
	Detail string `json:"detail,omitempty" doc:"The listing's A record, or the lookup error."`
}

// DeliverabilityReport is a mail domain's full deliverability picture: its DNS records (as CheckDNS
// reports them) and its server's DNSBL status.
type DeliverabilityReport struct {
	Domain         string           `json:"domain"`
	DNS            DomainDNS        `json:"dns"`
	Blacklists     []BlacklistCheck `json:"blacklists"`
	SpamFilterNote string           `json:"spam_filter_note"`
}

const spamFilterNote = "Stalwart's built-in spam filter on/off could not be confirmed against a documented /api/settings key for v0.13, so Hosbah does not offer that toggle."

// reverseIPv4 turns "203.0.113.7" into "7.113.0.203" for a DNSBL query name, or reports ok=false for
// anything that is not a plain dotted-quad (DNSBL lookups here are IPv4 only, per MH-577 I5).
func reverseIPv4(ip string) (string, bool) {
	parts := strings.Split(ip, ".")
	if len(parts) != 4 {
		return "", false
	}
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || n > 255 {
			return "", false
		}
	}
	return parts[3] + "." + parts[2] + "." + parts[1] + "." + parts[0], true
}

// isNotFoundDNS reports whether err is a DNS "no such record" answer (NXDOMAIN or no data), the
// expected shape of "not listed" from a DNSBL zone.
func isNotFoundDNS(err error) bool {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return dnsErr.IsNotFound
	}
	return false
}

// checkDNSBL looks up ip against every zone in DNSBLZones. A resolved A record means the address is
// listed; a not-found answer means it is clean; any other error is reported as its own status so a
// transient resolver failure never reads as "clean".
func checkDNSBL(ctx context.Context, ip string) []BlacklistCheck {
	out := make([]BlacklistCheck, 0, len(DNSBLZones))
	rev, ok := reverseIPv4(ip)
	if !ok {
		for _, zone := range DNSBLZones {
			out = append(out, BlacklistCheck{Zone: zone, Status: "error", Detail: "the mail server has no known public IPv4 address"})
		}
		return out
	}
	for _, zone := range DNSBLZones {
		cctx, cancel := context.WithTimeout(ctx, dnsTimeout)
		addrs, err := resolver.LookupHost(cctx, rev+"."+zone+".")
		cancel()
		switch {
		case err == nil && len(addrs) > 0:
			out = append(out, BlacklistCheck{Zone: zone, Status: "listed", Detail: addrs[0]})
		case err == nil:
			out = append(out, BlacklistCheck{Zone: zone, Status: "ok"})
		case isNotFoundDNS(err):
			out = append(out, BlacklistCheck{Zone: zone, Status: "ok"})
		default:
			out = append(out, BlacklistCheck{Zone: zone, Status: "error", Detail: err.Error()})
		}
	}
	return out
}

// DeliverabilityReport builds dom's full report: its DNS records/PTR (CheckDomainDNS) plus its mail
// server's DNSBL status.
func (s *Service) DeliverabilityReport(ctx context.Context, q *db.Queries, teamID string, srv db.MailServer, dom db.MailDomain) (DeliverabilityReport, error) {
	dnsRes, err := s.CheckDomainDNS(ctx, q, teamID, srv, dom)
	if err != nil {
		return DeliverabilityReport{}, err
	}
	return DeliverabilityReport{
		Domain:         dom.Domain,
		DNS:            dnsRes,
		Blacklists:     checkDNSBL(ctx, dnsRes.PTR.ServerIP),
		SpamFilterNote: spamFilterNote,
	}, nil
}
