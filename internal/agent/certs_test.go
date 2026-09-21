package agent

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// testCert is a self-signed leaf for names, as PEM and as a tls.Certificate.
func testCert(t *testing.T, issuerOrg string, notAfter time.Time, names ...string) ([]byte, tls.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{Organization: []string{issuerOrg}, CommonName: "R11"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: notAfter, DNSNames: names,
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func storeCert(t *testing.T, dir, issuer, name string, pemBytes []byte) {
	t.Helper()
	d := filepath.Join(dir, issuer, name)
	if err := os.MkdirAll(d, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, name+".crt"), pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCaddyCerts(t *testing.T) {
	dir := t.TempDir()
	oldDir, oldAddr := caddyCertsDir, certsDialAddr
	t.Cleanup(func() { caddyCertsDir, certsDialAddr = oldDir, oldAddr })
	caddyCertsDir = dir

	in90 := time.Now().Add(90 * 24 * time.Hour)
	pub, _ := testCert(t, "Let's Encrypt", in90, "shop.example.com")
	storeCert(t, dir, "acme-v02.api.letsencrypt.org-directory", "shop.example.com", pub)
	internalCA, _ := testCert(t, "Caddy Local Authority", time.Now().Add(12*time.Hour), "lab.internal.test")
	storeCert(t, dir, "local", "lab.internal.test", internalCA)
	// Both an internal and a public certificate: the public one is reported.
	both, _ := testCert(t, "Caddy Local Authority", time.Now().Add(400*24*time.Hour), "both.example.com")
	storeCert(t, dir, "local", "both.example.com", both)
	bothPub, _ := testCert(t, "ZeroSSL", in90, "both.example.com")
	storeCert(t, dir, "acme.zerossl.com-v2-dv90", "both.example.com", bothPub)
	wild, _ := testCert(t, "Let's Encrypt", in90, "*.wild.example.com")
	storeCert(t, dir, "acme-v02.api.letsencrypt.org-directory", "wildcard_.wild.example.com", wild)

	// Something else on the server presents a certificate for one name only.
	_, served := testCert(t, "Some Other CA", in90, "npm.example.com")
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{served}, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				_ = c.(*tls.Conn).Handshake()
				c.Close()
			}(c)
		}
	}()
	certsDialAddr = ln.Addr().String()

	hosts := []string{"shop.example.com", "lab.internal.test", "both.example.com", "a.wild.example.com", "npm.example.com", "none.example.com"}
	oc, _ := opCtx(t, agentproto.CaddyCertsParams{Hostnames: hosts})
	res, code, err := caddyCerts(oc)
	if err != nil || code != 0 {
		t.Fatalf("caddy.certs: %d %v", code, err)
	}
	got := res.(agentproto.CaddyCertsResult).Certificates
	if len(got) != len(hosts) {
		t.Fatalf("certificates: %+v", got)
	}
	for i, c := range got {
		if c.Hostname != hosts[i] {
			t.Errorf("entry %d is %s, want %s (order must follow the request)", i, c.Hostname, hosts[i])
		}
	}
	if c := got[0]; !c.Found || !c.Managed || !strings.Contains(c.Issuer, "Let's Encrypt") || c.NotAfter == nil || c.NotAfter.Unix() != in90.Unix() || c.NotBefore == nil {
		t.Errorf("a public certificate in storage: %+v", c)
	}
	if c := got[1]; !c.Found || c.Managed || !strings.Contains(c.Issuer, "Caddy Local Authority") {
		t.Errorf("an internal-CA certificate: %+v", c)
	}
	if c := got[2]; !c.Found || !c.Managed || !strings.Contains(c.Issuer, "ZeroSSL") {
		t.Errorf("public beats internal: %+v", c)
	}
	if c := got[3]; !c.Found || !c.Managed {
		t.Errorf("a wildcard covers its subdomain: %+v", c)
	}
	if c := got[4]; !c.Found || c.Managed || !strings.Contains(c.Issuer, "Some Other CA") {
		t.Errorf("a certificate presented by something else is found, not managed: %+v", c)
	}
	if c := got[5]; c.Found || c.NotAfter != nil || c.Issuer != "" {
		t.Errorf("a presented certificate for another name is not this hostname's: %+v", c)
	}

	// Nothing listening at all: not found, and no error worth showing.
	ln.Close()
	oc, _ = opCtx(t, agentproto.CaddyCertsParams{Hostnames: []string{"none.example.com"}})
	res, _, err = caddyCerts(oc)
	if c := res.(agentproto.CaddyCertsResult).Certificates[0]; err != nil || c.Found {
		t.Errorf("nothing listening: %+v %v", c, err)
	}

	many := make([]string, agentproto.MaxCertHostnames+1)
	for i := range many {
		many[i] = "h.example.com"
	}
	for _, bad := range [][]string{nil, {"../../etc/passwd"}, {"a/b.example.com"}, {"shop.example.com", "bad host"}, {"*.example.com"}, {"x.example.com:443"}, many} {
		oc, _ := opCtx(t, agentproto.CaddyCertsParams{Hostnames: bad})
		if _, code, err := caddyCerts(oc); err == nil || code != 2 {
			t.Errorf("hostnames %.60v should be refused: %d %v", bad, code, err)
		}
	}
}
