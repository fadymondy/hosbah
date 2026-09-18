package agentproto

import "testing"

func TestParseUpstream(t *testing.T) {
	ok := map[string]Upstream{
		"10.0.0.5:8080":        {Dial: "10.0.0.5:8080"},
		"10.0.0.5":             {Dial: "10.0.0.5:80"},
		"http://nas.lan:5000/": {Dial: "nas.lan:5000"},
		"https://192.168.1.2":  {Dial: "192.168.1.2:443", TLS: true},
		"HTTPS://pve.lan:8006": {Dial: "pve.lan:8006", TLS: true},
		"[2001:db8::1]:9000":   {Dial: "[2001:db8::1]:9000"},
		"localhost:3000":       {Dial: "localhost:3000"},
	}
	for in, want := range ok {
		got, err := ParseUpstream(in)
		if err != nil || got != want {
			t.Errorf("%q: got %+v err %v, want %+v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "ftp://x:21", "http://x/path", "user@host:22", "host:0", "host:70000", "2001:db8::1", "bad_host!:80"} {
		if _, err := ParseUpstream(in); err == nil {
			t.Errorf("%q: expected an error", in)
		}
	}
}

func TestProxyParamsValidate(t *testing.T) {
	id := "11111111-2222-4333-8444-555555555555"
	if err := (ProxyParams{ProxyID: id, Hostname: "nas.example.com", TLS: "auto", Upstream: "10.0.0.5:5000"}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (ProxyParams{ProxyID: id, Remove: true}).Validate(); err != nil {
		t.Fatal(err)
	}
	if (ProxyParams{ProxyID: "x", Hostname: "a.b", TLS: "auto", Upstream: "1.2.3.4"}).Validate() == nil {
		t.Fatal("bad id accepted")
	}
	if (ProxyParams{ProxyID: id, Hostname: "a.b", TLS: "bogus", Upstream: "1.2.3.4"}).Validate() == nil {
		t.Fatal("bad tls accepted")
	}
}
