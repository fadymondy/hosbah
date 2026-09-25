package certcheck

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"testing"
	"time"
)

// testCert starts a bare TLS listener on loopback presenting a self-signed certificate with the
// given validity window and DNS SAN, and returns its address and a closer. Deliberately not
// httptest.NewTLSServer: that helper's certificate has a fixed, always-valid window, so it cannot
// exercise the expired / near-expiry cases this checker exists for.
func testCertListener(t *testing.T, dnsName string, notBefore, notAfter time.Time) (addr string, pool *x509.CertPool, close func()) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Hosbah test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{Organization: []string{"Hosbah test"}, CommonName: dnsName},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		DNSNames:     []string{dnsName},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	cert := tls.Certificate{Certificate: [][]byte{der, caDER}, PrivateKey: key}

	pool = x509.NewCertPool()
	pool.AddCert(caCert)

	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { _ = c.(*tls.Conn).Handshake(); _ = c.Close() }()
		}
	}()
	return ln.Addr().String(), pool, func() { _ = ln.Close() }
}

func newTestService(dialAddr string, pool *x509.CertPool) *Service {
	s := &Service{now: time.Now}
	s.SetDialer(func(ctx context.Context, network, addr string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, dialAddr)
	})
	s.SetRootCAs(pool)
	return s
}

func TestDialOneValidCertificate(t *testing.T) {
	addr, pool, closeFn := testCertListener(t, "valid.example.test", time.Now().Add(-time.Hour), time.Now().Add(90*24*time.Hour))
	defer closeFn()
	s := newTestService(addr, pool)
	r := s.dialOne(context.Background(), "valid.example.test")
	if !r.OK || !r.SANMatch || r.Err != "" {
		t.Fatalf("want a clean reading, got %+v", r)
	}
	if time.Until(r.NotAfter) < 89*24*time.Hour {
		t.Fatalf("unexpected NotAfter: %v", r.NotAfter)
	}
}

func TestDialOneExpiredCertificate(t *testing.T) {
	addr, pool, closeFn := testCertListener(t, "expired.example.test", time.Now().Add(-48*time.Hour), time.Now().Add(-time.Hour))
	defer closeFn()
	s := newTestService(addr, pool)
	r := s.dialOne(context.Background(), "expired.example.test")
	if r.OK {
		t.Fatalf("an expired leaf must fail the TLS handshake, got %+v", r)
	}
	if r.Err == "" {
		t.Fatal("want an error describing why the handshake failed")
	}
}

func TestDialOneMismatchedSAN(t *testing.T) {
	addr, pool, closeFn := testCertListener(t, "other.example.test", time.Now().Add(-time.Hour), time.Now().Add(90*24*time.Hour))
	defer closeFn()
	s := newTestService(addr, pool)
	r := s.dialOne(context.Background(), "mismatch.example.test")
	// The Go TLS client already refuses a handshake whose certificate has no SAN for the requested
	// name (InsecureSkipVerify is never set here), so this surfaces the same way as any other
	// handshake failure — which is exactly the "record the error" outcome the checker wants.
	if r.OK {
		t.Fatalf("a certificate with no matching SAN must not verify, got %+v", r)
	}
	if r.Err == "" {
		t.Fatal("want an error")
	}
}

func TestDialOneUnreachable(t *testing.T) {
	s := newTestService("127.0.0.1:1", nil) // nothing listens on port 1
	r := s.dialOne(context.Background(), "unreachable.example.test")
	if r.OK || r.Err == "" {
		t.Fatalf("an unreachable host must record an error, got %+v", r)
	}
}

func TestFlagSetAndThresholdOrder(t *testing.T) {
	if thresholds[0].Flag != flag1 || thresholds[len(thresholds)-1].Flag != flag14 {
		t.Fatal("thresholds must be checked tightest-first")
	}
}
