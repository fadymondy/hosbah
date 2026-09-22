package agentproto

import (
	"errors"
	"net"
	"strconv"
	"strings"
)

// The Caddy proxy manager: a proxy host routes one hostname on a server to any upstream that
// server can reach — another container, a NAS, a printer, a legacy VM — the way Nginx Proxy Manager
// does it, but on the Caddy the agent already owns. App routes (route.set) stay as they are: those
// are local ports Hosbah itself started, so they are limited to 127.0.0.1.

// KindProxySet applies or removes one proxy host.
const KindProxySet = "proxy.set"

// ProxyParams: proxy.set. Remove takes the host out of the table; everything else is ignored then.
type ProxyParams struct {
	ProxyID  string `json:"proxy_id"`
	Hostname string `json:"hostname"`
	TLS      string `json:"tls"` // auto | internal | off
	// Upstream is what Caddy dials: "host:port", or with a scheme ("https://host:port") to speak
	// TLS to the upstream. A bare host gets :80 (:443 with https).
	Upstream string `json:"upstream,omitempty"`
	// SkipVerify accepts an upstream's self-signed certificate (https upstreams only).
	SkipVerify bool `json:"skip_verify,omitempty"`
	Remove     bool `json:"remove,omitempty"`
}

// ProxyRouteID is the @id of a proxy host's route in Caddy's config.
func ProxyRouteID(proxyID string) string { return NamePrefix + "proxy-" + proxyID }

// Upstream is a parsed upstream: what to dial, and whether to speak TLS to it.
type Upstream struct {
	Dial string // host:port
	TLS  bool
}

// ParseUpstream accepts "host:port", "host", "http://host[:port]" or "https://host[:port]" and
// returns what Caddy should dial. Paths, queries and credentials are refused: a reverse proxy
// upstream is a host and a port, and anything else is a sign the user meant something else.
func ParseUpstream(s string) (Upstream, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Upstream{}, errors.New("give an upstream, for example 10.0.0.5:8080")
	}
	var u Upstream
	rest := s
	switch {
	case strings.HasPrefix(strings.ToLower(s), "https://"):
		u.TLS, rest = true, s[len("https://"):]
	case strings.HasPrefix(strings.ToLower(s), "http://"):
		rest = s[len("http://"):]
	case strings.Contains(s, "://"):
		return Upstream{}, errors.New("the upstream must be host:port, http://host:port or https://host:port")
	}
	rest = strings.TrimSuffix(rest, "/")
	if strings.ContainsAny(rest, "/?#@ \t") {
		return Upstream{}, errors.New("the upstream is a host and a port only — no path, query or credentials")
	}
	host, port := rest, ""
	if h, p, err := net.SplitHostPort(rest); err == nil {
		host, port = h, p
	} else if strings.Count(rest, ":") > 1 && !strings.HasPrefix(rest, "[") {
		return Upstream{}, errors.New("an IPv6 upstream needs brackets, for example [2001:db8::1]:8080")
	}
	host = strings.Trim(host, "[]")
	if host == "" {
		return Upstream{}, errors.New("the upstream has no host")
	}
	if net.ParseIP(host) == nil && !ValidHostname(host) && host != "localhost" {
		return Upstream{}, errors.New("the upstream host must be an IP address or a hostname")
	}
	if port == "" {
		port = "80"
		if u.TLS {
			port = "443"
		}
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return Upstream{}, errors.New("the upstream port must be 1–65535")
	}
	u.Dial = net.JoinHostPort(host, strconv.Itoa(n))
	return u, nil
}

// Validate checks a proxy.set on both sides of the wire.
func (p ProxyParams) Validate() error {
	if !ValidID(p.ProxyID) {
		return errors.New("bad proxy id")
	}
	if p.Remove {
		return nil
	}
	if !ValidHostname(p.Hostname) {
		return errors.New("bad hostname")
	}
	if !ValidTLS(p.TLS) {
		return errors.New("tls must be auto, internal or off")
	}
	_, err := ParseUpstream(p.Upstream)
	return err
}
