package mail

import (
	"context"
	"errors"
	"net"
	"testing"
)

// fakeResolver is a minimal Resolver for MH-577 I5 tests: only LookupHost matters here, the other
// methods are unused stubs (dns.go's own checks are exercised through CheckDNS elsewhere).
type fakeResolver struct {
	// hosts maps a DNSBL query name to the addresses it should resolve to. A name absent from this
	// map answers a "not found" DNS error, the shape a clean IP gets from a real blocklist.
	hosts map[string][]string
	// errHosts maps a DNSBL query name to an arbitrary (non-not-found) error, simulating a resolver
	// failure distinct from "not listed".
	errHosts map[string]error
}

func (f *fakeResolver) LookupTXT(ctx context.Context, name string) ([]string, error) { return nil, nil }
func (f *fakeResolver) LookupMX(ctx context.Context, name string) ([]*net.MX, error) { return nil, nil }
func (f *fakeResolver) LookupAddr(ctx context.Context, addr string) ([]string, error) {
	return nil, nil
}
func (f *fakeResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	if err, ok := f.errHosts[host]; ok {
		return nil, err
	}
	if addrs, ok := f.hosts[host]; ok {
		return addrs, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

func withFakeResolver(t *testing.T, f *fakeResolver) {
	t.Helper()
	prev := resolver
	resolver = f
	t.Cleanup(func() { resolver = prev })
}

func TestReverseIPv4(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"203.0.113.7", "7.113.0.203", true},
		{"1.2.3.4", "4.3.2.1", true},
		{"::1", "", false},
		{"not-an-ip", "", false},
		{"256.1.1.1", "", false},
		{"1.2.3", "", false},
	}
	for _, c := range cases {
		got, ok := reverseIPv4(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("reverseIPv4(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestIsNotFoundDNS(t *testing.T) {
	if isNotFoundDNS(errors.New("boom")) {
		t.Fatal("a plain error must not read as not-found")
	}
	if !isNotFoundDNS(&net.DNSError{IsNotFound: true}) {
		t.Fatal("IsNotFound DNS error must read as not-found")
	}
	if isNotFoundDNS(&net.DNSError{IsNotFound: false, IsTimeout: true}) {
		t.Fatal("a timeout must not read as not-found")
	}
}

func TestCheckDNSBLNoIP(t *testing.T) {
	withFakeResolver(t, &fakeResolver{})
	out := checkDNSBL(context.Background(), "")
	if len(out) != len(DNSBLZones) {
		t.Fatalf("expected one result per zone, got %d", len(out))
	}
	for _, c := range out {
		if c.Status != "error" {
			t.Errorf("%s: want error status with no server IP, got %s", c.Zone, c.Status)
		}
	}
}

func TestCheckDNSBLCleanListedAndError(t *testing.T) {
	// A clean IP: none of the reversed names resolve, so every zone answers "not found" -> ok.
	withFakeResolver(t, &fakeResolver{})
	out := checkDNSBL(context.Background(), "203.0.113.7")
	if len(out) != len(DNSBLZones) {
		t.Fatalf("expected one result per zone, got %d", len(out))
	}
	for _, c := range out {
		if c.Status != "ok" {
			t.Errorf("%s: want ok for a clean IP, got %s (%s)", c.Zone, c.Status, c.Detail)
		}
	}

	// Listed on the first zone, a resolver error on the second, clean on the rest.
	rev, ok := reverseIPv4("203.0.113.7")
	if !ok {
		t.Fatal("reverseIPv4 should succeed for a dotted quad")
	}
	listedName := rev + "." + DNSBLZones[0] + "."
	errName := rev + "." + DNSBLZones[1] + "."
	withFakeResolver(t, &fakeResolver{
		hosts:    map[string][]string{listedName: {"127.0.0.2"}},
		errHosts: map[string]error{errName: errors.New("resolver unavailable")},
	})
	out = checkDNSBL(context.Background(), "203.0.113.7")
	byZone := map[string]BlacklistCheck{}
	for _, c := range out {
		byZone[c.Zone] = c
	}
	if byZone[DNSBLZones[0]].Status != "listed" || byZone[DNSBLZones[0]].Detail != "127.0.0.2" {
		t.Errorf("zone 0 should be listed with detail 127.0.0.2, got %+v", byZone[DNSBLZones[0]])
	}
	if byZone[DNSBLZones[1]].Status != "error" {
		t.Errorf("zone 1 should report a resolver error, got %+v", byZone[DNSBLZones[1]])
	}
	for _, zone := range DNSBLZones[2:] {
		if byZone[zone].Status != "ok" {
			t.Errorf("%s should be ok (not found), got %+v", zone, byZone[zone])
		}
	}
}

func TestSpamFilterNoteIsAlwaysSet(t *testing.T) {
	if spamFilterNote == "" {
		t.Fatal("the spam filter note must explain why no toggle is offered")
	}
}
