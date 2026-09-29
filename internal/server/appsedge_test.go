package server

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	"github.com/fadymondy/hosbah/internal/deploy"
)

/*
The apps edge (HOSBAH_APPS_EDGE): with it set to server E, a platform hostname created on another
server S gets tls "off" and a route on E's own Caddy (proxy.set) to S's private address; deleting the
app removes that route. Servers must exist before the env var naming one of them can be set, so this
boots the control plane twice against the same database — once to enrol the two servers, once with
HOSBAH_APPS_EDGE pointed at the first (the ops signing key is derived from the KEK, not random per
boot, so the fake agent that connects the second time verifies the same as the first).
*/
func TestAppsEdgeRoutesAPlatformHostnameThroughTheEdgeServer(t *testing.T) {
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	short := suffix[len(suffix)-6:]
	email := "apps-edge-" + suffix + "@example.test"
	domain := "apps-edge-" + short + ".test"

	var teamID, edgeSrv, appSrv string
	var edgePriv ed25519.PrivateKey
	func() {
		ts, stop := bootTestServer(t)
		defer stop()
		owner := newClient(t, ts.URL)
		owner.register(email)
		var team struct{ ID string }
		owner.mustJSON("POST", "/api/teams", map[string]any{"name": "Apps Edge " + suffix}, 201, &team)
		teamID = team.ID
		edgeSrv, edgePriv = enrolWithServicesKey(t, owner, ts.URL, teamID, "edge-"+short, "10.10.10.1", nil)
		appSrv, _ = enrolWithServicesKey(t, owner, ts.URL, teamID, "app-"+short, "10.10.10.5", nil)
	}()

	t.Setenv("HOSBAH_APPS_DOMAIN", domain)
	t.Setenv("HOSBAH_APPS_TLS", "auto")
	t.Setenv("HOSBAH_APPS_EDGE", edgeSrv)

	ts, stop := bootTestServer(t)
	defer stop()
	owner := newClient(t, ts.URL)
	owner.login(email)

	edge := startFakeAgent(t, ts.URL, edgeSrv, edgePriv, func(op agentproto.SignedOp) (any, string) {
		return map[string]any{"ok": true}, ""
	})

	var app struct {
		App struct{ ID, Name string } `json:"app"`
	}
	owner.mustJSON("POST", "/api/teams/"+teamID+"/apps", map[string]any{"name": "site-" + short, "server": appSrv,
		"repo_url": "https://github.com/octocat/api.git"}, 201, &app)
	base := "/api/teams/" + teamID + "/apps/" + app.App.ID

	var domains struct {
		Domains []struct {
			ID       string `json:"id"`
			Hostname string `json:"hostname"`
			TLS      string `json:"tls"`
		} `json:"domains"`
	}
	owner.mustJSON("GET", base+"/domains", nil, 200, &domains)
	if len(domains.Domains) != 1 {
		t.Fatalf("expected one platform domain, got %+v", domains.Domains)
	}
	d := domains.Domains[0]
	wantHost := "hosbah-app-site-" + short + "." + domain
	if d.Hostname != wantHost {
		t.Fatalf("hostname = %q, want %q", d.Hostname, wantHost)
	}
	if d.TLS != "off" {
		t.Fatalf("tls = %q, want \"off\" (the edge terminates TLS)", d.TLS)
	}

	// The edge got a proxy.set for it: deterministic proxy_id, tls off, upstream the app server's
	// private address on :80.
	var setOp agentproto.SignedOp
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		for _, op := range edge.opsSnapshot() {
			if op.Kind == agentproto.KindProxySet {
				var p agentproto.ProxyParams
				if json.Unmarshal(op.Params, &p) == nil && !p.Remove && p.ProxyID == deploy.EdgeProxyID(d.ID) {
					setOp = op
				}
			}
		}
		if setOp.OpID != "" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if setOp.OpID == "" {
		t.Fatalf("the edge never received a proxy.set for the platform hostname (ops: %v)", edge.kinds())
	}
	var p agentproto.ProxyParams
	if err := json.Unmarshal(setOp.Params, &p); err != nil {
		t.Fatal(err)
	}
	if p.Hostname != wantHost || p.TLS != "off" || p.Upstream != "10.10.10.5:80" {
		t.Fatalf("proxy.set params = %+v", p)
	}

	// Deleting the app queues the Remove for the same proxy id.
	owner.mustJSON("DELETE", base, map[string]any{"confirm": app.App.Name}, 204, nil)
	var removeOp agentproto.SignedOp
	deadline = time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		for _, op := range edge.opsSnapshot() {
			if op.Kind != agentproto.KindProxySet {
				continue
			}
			var rp agentproto.ProxyParams
			if json.Unmarshal(op.Params, &rp) == nil && rp.Remove && rp.ProxyID == deploy.EdgeProxyID(d.ID) {
				removeOp = op
			}
		}
		if removeOp.OpID != "" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if removeOp.OpID == "" {
		t.Fatalf("the edge never received the proxy.set Remove for the deleted app (ops: %v)", edge.kinds())
	}

	if rej := edge.rejectedList(); len(rej) > 0 {
		t.Errorf("the edge agent rejected operations: %v", rej)
	}
}
