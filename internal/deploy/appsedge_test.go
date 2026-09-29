package deploy

import (
	"testing"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
)

func TestAppsDomainEdgeFromEnv(t *testing.T) {
	clearAppsEnv(t)
	t.Setenv("HOSBAH_APPS_DOMAIN", "hosbah.com")
	t.Setenv("HOSBAH_APPS_TLS", "auto")
	d := AppsDomainFromEnv()
	if d.EdgeConfigured() {
		t.Fatal("no HOSBAH_APPS_EDGE: EdgeConfigured must be false")
	}
	if got := d.PlatformTLS(); got != "auto" {
		t.Fatalf("PlatformTLS() = %q, want the configured HOSBAH_APPS_TLS", got)
	}

	t.Setenv("HOSBAH_APPS_EDGE", "srv_edge")
	d = AppsDomainFromEnv()
	if !d.EdgeConfigured() || d.EdgeServerID != "srv_edge" {
		t.Fatalf("d = %+v", d)
	}
	if got := d.PlatformTLS(); got != "off" {
		t.Fatalf("PlatformTLS() = %q, want \"off\" once the edge is configured, regardless of HOSBAH_APPS_TLS", got)
	}
}

// The legacy CLOUDY_APPS_EDGE name still works, like every other pre-rename variable.
func TestAppsDomainEdgeReadsThePreRenameVariable(t *testing.T) {
	clearAppsEnv(t)
	t.Setenv("HOSBAH_APPS_DOMAIN", "hosbah.com")
	t.Setenv("CLOUDY_APPS_EDGE", "srv_edge")
	d := AppsDomainFromEnv()
	if !d.EdgeConfigured() || d.EdgeServerID != "srv_edge" {
		t.Fatalf("d = %+v", d)
	}
}

func TestEdgeProxyID(t *testing.T) {
	// The agent's proxy.set accepts only a UUID (it refused "edge-<id>" live with "bad proxy id").
	if id := EdgeProxyID("dom_123"); !agentproto.ValidID(id) || (agentproto.ProxyParams{ProxyID: id, Remove: true}).Validate() != nil {
		t.Fatalf("EdgeProxyID() = %q is not a proxy id the agent accepts", id)
	}
	// Distinct domains never collide, and it is stable (the reconcile re-applies it every run).
	if EdgeProxyID("dom_1") == EdgeProxyID("dom_2") {
		t.Fatal("EdgeProxyID must be distinct per domain")
	}
	if EdgeProxyID("dom_1") != EdgeProxyID("dom_1") {
		t.Fatal("EdgeProxyID must be deterministic")
	}
}

func TestEdgeUpstream(t *testing.T) {
	ip := "10.10.10.5"
	if got, want := edgeUpstream(db.Server{IpAddress: &ip}), "10.10.10.5:80"; got != want {
		t.Fatalf("edgeUpstream() = %q, want %q", got, want)
	}
	if got := edgeUpstream(db.Server{}); got != "" {
		t.Fatalf("edgeUpstream() with no address = %q, want \"\"", got)
	}
	empty := "  "
	if got := edgeUpstream(db.Server{IpAddress: &empty}); got != "" {
		t.Fatalf("edgeUpstream() with a blank address = %q, want \"\"", got)
	}
}

// edgeProxyOpID is deterministic per (domain, params): the same route re-applied is the same
// operation id (so the reconcile's repeat enqueue does not duplicate), while a change that would
// otherwise collide (Set -> Remove, or a new upstream) gets a fresh id instead of erroring.
func TestEdgeProxyOpID(t *testing.T) {
	set := agentproto.ProxyParams{ProxyID: EdgeProxyID("dom_1"), Hostname: "shop.hosbah.com", TLS: "off", Upstream: "10.10.10.5:80"}
	sameAgain := agentproto.ProxyParams{ProxyID: EdgeProxyID("dom_1"), Hostname: "shop.hosbah.com", TLS: "off", Upstream: "10.10.10.5:80"}
	moved := agentproto.ProxyParams{ProxyID: EdgeProxyID("dom_1"), Hostname: "shop.hosbah.com", TLS: "off", Upstream: "10.10.10.6:80"}
	remove := agentproto.ProxyParams{ProxyID: EdgeProxyID("dom_1"), Remove: true}
	other := agentproto.ProxyParams{ProxyID: EdgeProxyID("dom_2"), Hostname: "shop.hosbah.com", TLS: "off", Upstream: "10.10.10.5:80"}

	if edgeProxyOpID("dom_1", set) != edgeProxyOpID("dom_1", sameAgain) {
		t.Fatal("identical params must give the same op id (idempotent re-apply)")
	}
	ids := []string{edgeProxyOpID("dom_1", set), edgeProxyOpID("dom_1", moved), edgeProxyOpID("dom_1", remove), edgeProxyOpID("dom_2", other)}
	for i := range ids {
		for j := range ids {
			if i != j && ids[i] == ids[j] {
				t.Fatalf("op ids must differ when the params differ: %v", ids)
			}
		}
	}
	// Well-formed UUID shape (ops.Enqueue stores it as one).
	if len(ids[0]) != 36 {
		t.Fatalf("edgeProxyOpID() = %q, not a UUID", ids[0])
	}
}
