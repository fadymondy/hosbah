package monitors

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAddrClass(t *testing.T) {
	for ip, want := range map[string]string{
		"8.8.8.8": "public", "2606:4700:4700::1111": "public",
		"10.1.2.3": "private", "172.16.0.9": "private", "192.168.1.1": "private", "100.64.0.1": "private", "fd12:3456::1": "private",
		"127.0.0.1": "refused", "::1": "refused", "0.0.0.0": "refused", "169.254.169.254": "refused", "169.254.1.1": "refused",
		"fe80::1": "refused", "fd00:ec2::254": "refused", "100.100.100.200": "refused", "192.0.0.192": "refused", "224.0.0.1": "refused",
		"255.255.255.255": "refused", "::ffff:127.0.0.1": "refused", "::ffff:169.254.169.254": "refused", "::ffff:10.0.0.1": "private",
	} {
		if got := addrClass(net.ParseIP(ip)); got != want {
			t.Errorf("%s: %s (want %s)", ip, got, want)
		}
	}
}

// Rule 3: a private address is dialled only for an exempt hostname; loopback, link-local and
// metadata addresses never are, exempt or not.
func TestDialable(t *testing.T) {
	ips := []net.IP{net.ParseIP("10.0.0.5"), net.ParseIP("127.0.0.1"), net.ParseIP("169.254.169.254"), net.ParseIP("93.184.216.34")}
	if got := dialable("app.example.test", ips, nil); len(got) != 1 || got[0].String() != "93.184.216.34" {
		t.Errorf("without the exception: %v", got)
	}
	exempt := map[string]bool{"app.example.test": true}
	if got := dialable("app.example.test", ips, exempt); len(got) != 2 || got[0].String() != "10.0.0.5" || got[1].String() != "93.184.216.34" {
		t.Errorf("with the exception: %v", got)
	}
	if got := dialable("elsewhere.example.test", ips, exempt); len(got) != 1 {
		t.Errorf("the exception is per hostname: %v", got)
	}
	if !IsPrivateAddress("192.168.4.4") || IsPrivateAddress("127.0.0.1") || IsPrivateAddress("203.0.113.9") || IsPrivateAddress("nope") {
		t.Error("IsPrivateAddress")
	}
}

// The transport refuses a name that resolves to loopback — even under the exception — and a
// literal private address, and never reaches the listener.
func TestSafeTransportRefuses(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits++ }))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	client := &http.Client{Transport: SafeTransport(func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("127.0.0.1")}, nil
	})}
	for _, ctx := range []context.Context{context.Background(), allowPrivate(context.Background(), []string{"app.example.test"})} {
		for _, u := range []string{"http://app.example.test:" + port + "/", srv.URL, "http://10.0.0.1:" + port + "/", "http://[::1]:" + port + "/", "http://169.254.169.254/latest/meta-data/"} {
			if ctx.Value(allowPrivateKey{}) != nil && strings.Contains(u, "10.0.0.1") {
				continue // would really be dialled
			}
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
			resp, err := client.Do(req)
			if err == nil {
				resp.Body.Close()
			}
			if !errors.Is(err, ErrRefusedAddress) {
				t.Errorf("%s: %v (want a refusal)", u, err)
			}
		}
	}
	if hits != 0 {
		t.Errorf("the listener was reached %d times", hits)
	}
}

func TestValidate(t *testing.T) {
	hosts := []string{"app.example.test"}
	ok := spec{Name: "up", Kind: KindHTTP, Target: "https://app.example.test/health", ExpectedStatus: 200, Enabled: true}
	if s, err := validate(ok, hosts); err != nil || s.Interval != 60 {
		t.Fatalf("valid: %+v %v", s, err)
	}
	bad := map[string]func(*spec){
		"another host":        func(s *spec) { s.Target = "https://evil.example.test/" },
		"a suffix trick":      func(s *spec) { s.Target = "https://app.example.test.evil.test/" },
		"userinfo trick":      func(s *spec) { s.Target = "https://app.example.test@evil.test/" },
		"credentials":         func(s *spec) { s.Target = "https://u:p@app.example.test/" },
		"metadata":            func(s *spec) { s.Target = "http://169.254.169.254/latest" },
		"scheme":              func(s *spec) { s.Target = "ftp://app.example.test/" },
		"no scheme":           func(s *spec) { s.Target = "app.example.test/health" },
		"status":              func(s *spec) { s.ExpectedStatus = 42 },
		"slow":                func(s *spec) { s.Threshold = 60001 },
		"interval":            func(s *spec) { s.Interval = 10 },
		"kind":                func(s *spec) { s.Kind = "tcp" },
		"name":                func(s *spec) { s.Name = " " },
		"email":               func(s *spec) { s.Emails = []string{"not an email"} },
		"email with a name":   func(s *spec) { s.Emails = []string{"Bob <bob@example.test>"} },
		"eleven emails":       func(s *spec) { s.Emails = strings.Split(strings.Repeat("a@example.test,", 11), ",")[:11] },
		"http webhook":        func(s *spec) { s.Webhook = "http://hooks.example.test/x" },
		"private webhook":     func(s *spec) { s.Webhook = "https://10.0.0.1/x" },
		"loopback webhook":    func(s *spec) { s.Webhook = "https://127.0.0.1/x" },
		"webhook credentials": func(s *spec) { s.Webhook = "https://u:p@hooks.example.test/x" },
		"cpu with a target":   func(s *spec) { s.Kind, s.Threshold = KindCPU, 80 },
		"cpu without percent": func(s *spec) { s.Kind, s.Target = KindCPU, "" },
		"cpu window":          func(s *spec) { s.Kind, s.Target, s.Threshold, s.Window = KindCPU, "", 80, 61 },
	}
	for name, mutate := range bad {
		s := ok
		mutate(&s)
		if _, err := validate(s, hosts); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	s, err := validate(spec{Name: " disk ", Kind: KindDisk, Threshold: 90, Emails: []string{" A@Example.test ", "a@example.test"},
		Webhook: "https://hooks.example.test/x"}, nil)
	if err != nil || s.Name != "disk" || s.Window != 5 || len(s.Emails) != 1 || s.Emails[0] != "a@example.test" {
		t.Errorf("disk: %+v %v", s, err)
	}
	if _, err := validate(spec{Name: "up", Kind: KindHTTP, Target: "HTTP://APP.example.test:8443/x?y=1"}, hosts); err != nil {
		t.Errorf("a port, a query and upper case are fine: %v", err)
	}
}
