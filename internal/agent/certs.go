package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

/*
caddy.certs: the certificate the server holds for each hostname. Read-only.

Caddy's admin API has no certificate listing (only its config and its internal CA), so the agent
reads Caddy's certificate storage instead. The path is not a guess: the agent wrote Caddy's unit
with XDG_DATA_HOME=/var/lib, so certmagic stores every certificate at
  /var/lib/caddy/certificates/<issuer>/<host>/<host>.crt
where <issuer> is "local" for Caddy's internal CA and the ACME directory otherwise
(acme-v02.api.letsencrypt.org-directory, …), and a wildcard's <host> is "wildcard_.<parent>".
Reading a file never triggers an issuance, which a handshake to an on-demand hostname would.

Only when storage holds nothing for a hostname does the agent look at what is actually presented
on 127.0.0.1:443 with that SNI (verification off: the leaf is only read, never trusted). That
covers a server whose 443 belongs to something else (an imported reverse proxy); such a
certificate is reported with managed=false.
*/

var (
	// caddyCertsDir and certsDialAddr are overridable in tests.
	caddyCertsDir = "/var/lib/caddy/certificates"
	certsDialAddr = "127.0.0.1:443"
)

const certsDialTimeout = 3 * time.Second

func caddyCerts(oc *OpContext) (any, int, error) {
	p, err := decode[agentproto.CaddyCertsParams](oc)
	if err != nil {
		return nil, 2, err
	}
	if err := p.Validate(); err != nil {
		return nil, 2, err
	}
	res := agentproto.CaddyCertsResult{Certificates: make([]agentproto.CaddyCert, len(p.Hostnames))}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i, h := range p.Hostnames {
		h = strings.ToLower(h)
		if c, ok := storedCert(h); ok {
			res.Certificates[i] = c
			continue
		}
		wg.Add(1)
		go func(i int, h string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			res.Certificates[i] = presentedCert(oc.Ctx, h)
		}(i, h)
	}
	wg.Wait()
	return res, 0, nil
}

// storedCert finds the hostname's certificate (or its parent's wildcard) in Caddy's storage: the
// one that expires last, a public CA's before the internal CA's.
func storedCert(host string) (agentproto.CaddyCert, bool) {
	names := []string{host}
	if _, parent, ok := strings.Cut(host, "."); ok && strings.Contains(parent, ".") {
		names = append(names, "wildcard_."+parent)
	}
	issuers, err := os.ReadDir(caddyCertsDir)
	if err != nil {
		return agentproto.CaddyCert{}, false
	}
	var (
		best      *x509.Certificate
		bestLocal bool
	)
	for _, is := range issuers {
		if !is.IsDir() {
			continue
		}
		for _, n := range names {
			b, err := os.ReadFile(filepath.Join(caddyCertsDir, is.Name(), n, n+".crt")) //#nosec G304 -- n passed ValidHostname: no separators, no ".."
			if err != nil {
				continue
			}
			leaf := firstCert(b)
			if leaf == nil {
				continue
			}
			local := is.Name() == "local"
			if best == nil || (bestLocal && !local) || (bestLocal == local && leaf.NotAfter.After(best.NotAfter)) {
				best, bestLocal = leaf, local
			}
		}
	}
	if best == nil {
		return agentproto.CaddyCert{}, false
	}
	return certOut(host, best, !bestLocal), true
}

func firstCert(pemBytes []byte) *x509.Certificate {
	for {
		var block *pem.Block
		block, pemBytes = pem.Decode(pemBytes)
		if block == nil {
			return nil
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil
		}
		return c
	}
}

// presentedCert reads the leaf presented locally for the hostname. Nothing listening, or a
// handshake that fails, is "not found", not an error of the operation.
func presentedCert(ctx context.Context, host string) agentproto.CaddyCert {
	out := agentproto.CaddyCert{Hostname: host}
	dctx, cancel := context.WithTimeout(ctx, certsDialTimeout)
	defer cancel()
	d := tls.Dialer{NetDialer: &net.Dialer{}, Config: &tls.Config{
		ServerName:         host,
		InsecureSkipVerify: true, //#nosec G402 -- the leaf is read and reported, never trusted; the peer is this host
		MinVersion:         tls.VersionTLS12,
	}}
	conn, err := d.DialContext(dctx, "tcp", certsDialAddr)
	if err != nil {
		// Nothing listening on 443 is simply "no certificate"; anything else is worth telling.
		if !errors.Is(err, syscall.ECONNREFUSED) {
			out.Error = "the TLS handshake on this server failed: " + clipCertErr(err.Error())
		}
		return out
	}
	defer conn.Close()
	tc, ok := conn.(*tls.Conn)
	if !ok || len(tc.ConnectionState().PeerCertificates) == 0 {
		return out
	}
	leaf := tc.ConnectionState().PeerCertificates[0]
	if leaf.VerifyHostname(host) != nil {
		return out // a default certificate of something else: not this hostname's
	}
	return certOut(host, leaf, false)
}

func clipCertErr(s string) string {
	if len(s) > 200 {
		return s[:200]
	}
	return s
}

func certOut(host string, c *x509.Certificate, managed bool) agentproto.CaddyCert {
	nb, na := c.NotBefore.UTC(), c.NotAfter.UTC()
	issuer := c.Issuer.CommonName
	if len(c.Issuer.Organization) > 0 && c.Issuer.Organization[0] != "" {
		if issuer != "" {
			issuer = c.Issuer.Organization[0] + " " + issuer
		} else {
			issuer = c.Issuer.Organization[0]
		}
	}
	return agentproto.CaddyCert{Hostname: host, Found: true, Issuer: issuer, NotBefore: &nb, NotAfter: &na, Managed: managed}
}
