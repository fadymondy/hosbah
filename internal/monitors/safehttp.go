package monitors

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

/*
The control plane fetches URLs that tenants typed (a monitor's target, a webhook), so every
connection it opens for them goes through one dialer. The rule:

 1. The hostname is resolved HERE, once, and the connection is made to the resolved IP — never to
    the name — so the address that was judged is the address that is dialled (no DNS rebinding
    between the check and the connect). Every hop of a redirect dials through the same rule.
 2. An address is REFUSED when it is loopback, private (RFC 1918, fc00::/7), carrier-grade NAT
    (100.64.0.0/10), link-local (169.254.0.0/16 — which holds the cloud metadata service
    169.254.169.254 — and fe80::/10), the other metadata addresses (fd00:ec2::254, 100.100.100.200,
    192.0.0.192), unspecified, multicast, broadcast, or one of the protocol-assignment / documentation
    / benchmarking blocks. IPv4-mapped IPv6 is unwrapped first.
 3. One exception, for sites that live on a LAN: a PRIVATE address (RFC 1918, fc00::/7 or
    100.64.0.0/10 — nothing else of rule 2) is allowed when BOTH hold: the app's server itself has a
    private address (servers.ip_address), AND the hostname being dialled is one of the app's own
    hostnames. The exception travels in the request's context (allowPrivate), is only ever set for
    a monitor's check — never for a webhook — and never covers loopback, link-local or metadata
    addresses, nor any other hostname a redirect leads to.
*/

// ErrRefusedAddress is the dialer's refusal (rule 2).
var ErrRefusedAddress = errors.New("refused: the address is private, loopback, link-local or a metadata address")

var refusedPrefixes = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{
		"0.0.0.0/8", "127.0.0.0/8", "169.254.0.0/16", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24",
		"203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4", "100.100.100.200/32",
		"::/128", "::1/128", "fe80::/10", "ff00::/8", "2001:db8::/32", "64:ff9b::/96", "64:ff9b:1::/48", "fd00:ec2::/32",
	} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

var privatePrefixes = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10", "fc00::/7"} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

func inAny(a netip.Addr, ps []netip.Prefix) bool {
	for _, p := range ps {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// addrClass sorts an address: "refused" (never dialled), "private" (only under the exception) or
// "public".
func addrClass(ip net.IP) string {
	a, ok := netip.AddrFromSlice(ip)
	if !ok {
		return "refused"
	}
	a = a.Unmap()
	switch {
	case inAny(a, refusedPrefixes), a.IsLoopback(), a.IsLinkLocalUnicast(), a.IsLinkLocalMulticast(), a.IsMulticast(), a.IsUnspecified():
		return "refused"
	case inAny(a, privatePrefixes), a.IsPrivate():
		return "private"
	}
	return "public"
}

// IsPrivateAddress reports whether s is an IP in the private ranges of the exception (rule 3).
func IsPrivateAddress(s string) bool {
	ip := net.ParseIP(strings.TrimSpace(s))
	return ip != nil && addrClass(ip) == "private"
}

type allowPrivateKey struct{}

// allowPrivate marks the hostnames for which rule 3 applies to requests made with ctx.
func allowPrivate(ctx context.Context, hostnames []string) context.Context {
	set := map[string]bool{}
	for _, h := range hostnames {
		set[strings.ToLower(strings.TrimSuffix(h, "."))] = true
	}
	return context.WithValue(ctx, allowPrivateKey{}, set)
}

// Resolver looks a hostname up (net.DefaultResolver.LookupIP in production).
type Resolver func(ctx context.Context, host string) ([]net.IP, error)

func defaultResolver(ctx context.Context, host string) ([]net.IP, error) {
	return net.DefaultResolver.LookupIP(ctx, "ip", host)
}

// SafeDialContext is the dialer of the rule above.
func SafeDialContext(resolve Resolver) func(ctx context.Context, network, addr string) (net.Conn, error) {
	if resolve == nil {
		resolve = defaultResolver
	}
	d := &net.Dialer{Timeout: 5 * time.Second}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if network != "tcp" && network != "tcp4" && network != "tcp6" {
			return nil, fmt.Errorf("refused: network %s", network)
		}
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		host = strings.ToLower(strings.TrimSuffix(host, "."))
		var ips []net.IP
		if ip := net.ParseIP(host); ip != nil {
			ips = []net.IP{ip}
		} else if ips, err = resolve(ctx, host); err != nil {
			return nil, err
		}
		exempt, _ := ctx.Value(allowPrivateKey{}).(map[string]bool)
		var lastErr error = ErrRefusedAddress
		for _, ip := range dialable(host, ips, exempt) {
			conn, err := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
}

// SafeTransport is an http.RoundTripper that only connects through SafeDialContext. No proxy (a
// proxy would do the dialling, unjudged), no keep-alive (each check is a fresh connection).
func SafeTransport(resolve Resolver) *http.Transport {
	return &http.Transport{
		Proxy:                  nil,
		DialContext:            SafeDialContext(resolve),
		DisableKeepAlives:      true,
		TLSHandshakeTimeout:    5 * time.Second,
		ResponseHeaderTimeout:  10 * time.Second,
		MaxResponseHeaderBytes: 64 << 10,
		ForceAttemptHTTP2:      false,
	}
}

// dialable is rules 2 and 3: the addresses of host that may be dialled. exempt holds the hostnames
// the private-address exception applies to (nil: none).
func dialable(host string, ips []net.IP, exempt map[string]bool) []net.IP {
	var out []net.IP
	for _, ip := range ips {
		switch addrClass(ip) {
		case "public":
			out = append(out, ip)
		case "private":
			if exempt[host] {
				out = append(out, ip)
			}
		}
	}
	return out
}
