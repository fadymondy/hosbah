package agentproto

import "testing"

func TestValidRouteHostname(t *testing.T) {
	for _, ok := range []string{"app.example.com", "*.example.com", "*.a.example.com"} {
		if !ValidRouteHostname(ok) {
			t.Errorf("%q should be a valid route hostname", ok)
		}
	}
	for _, bad := range []string{"*.com", "*", "*.", "a.*.example.com", "**.example.com", "*example.com", "*.-x.example.com"} {
		if ValidRouteHostname(bad) {
			t.Errorf("%q should not be a valid route hostname", bad)
		}
	}
	if ValidHostname("*.example.com") {
		t.Error("ValidHostname must stay exact-only: certs, proxies and DNS never take a wildcard")
	}
}

func TestValidRouteHostRefusesPublicWildcardTLS(t *testing.T) {
	if ValidRouteHost(Route{Hostname: "*.example.com", TLS: "auto"}) {
		t.Error("a wildcard with tls auto (on-demand, no DNS challenge) was accepted")
	}
	for _, tls := range []string{"off", "internal"} {
		if !ValidRouteHost(Route{Hostname: "*.example.com", TLS: tls}) {
			t.Errorf("wildcard with tls %s refused", tls)
		}
	}
	if !ValidRouteHost(Route{Hostname: "app.example.com", TLS: "auto"}) {
		t.Error("an exact hostname with tls auto refused")
	}
}
