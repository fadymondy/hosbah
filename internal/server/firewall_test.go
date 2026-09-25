package server

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

/*
A server's firewall (MH-580 J1), end to end against a real fake agent: staged rules (add/list/
remove), a preset, apply (which renders the rules through the agent and returns what nft loaded),
live status, role enforcement (a viewer reads but does not write, and a drop default policy needs
confirm), input validation, and cross-team isolation.
*/
func TestFirewall(t *testing.T) {
	ts, stop := bootTestServer(t)
	defer stop()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	short := suffix[len(suffix)-6:]

	owner := newClient(t, ts.URL)
	owner.register("firewall-owner-" + suffix + "@example.test")
	var team struct{ ID string }
	owner.mustJSON("POST", "/api/teams", map[string]any{"name": "Firewall " + suffix}, 201, &team)
	base := "/api/teams/" + team.ID

	viewerEmail := "firewall-viewer-" + suffix + "@example.test"
	viewer := newClient(t, ts.URL)
	viewer.register(viewerEmail)
	var inv struct {
		AcceptURL string `json:"accept_url"`
	}
	owner.mustJSON("POST", base+"/invitations", map[string]any{"email": viewerEmail, "role": "viewer"}, 201, &inv)
	viewer.mustJSON("POST", "/api/invitations/"+inv.AcceptURL[strings.LastIndex(inv.AcceptURL, "/")+1:]+"/accept", nil, 200, nil)

	stranger := newClient(t, ts.URL)
	stranger.register("firewall-stranger-" + suffix + "@example.test")
	var otherTeam struct{ ID string }
	stranger.mustJSON("POST", "/api/teams", map[string]any{"name": "FirewallElsewhere " + suffix}, 201, &otherTeam)

	srv, priv := enrolWithServicesKey(t, owner, ts.URL, team.ID, "firewall-a-"+short, "203.0.113.60", nil)

	respond := func(op agentproto.SignedOp) (any, string) {
		switch op.Kind {
		case agentproto.KindFirewallApply:
			return agentproto.FirewallApplyResult{SSHPort: 22, Ruleset: "table inet hosbah {\n}\n"}, ""
		case agentproto.KindFirewallStatus:
			return agentproto.FirewallStatusResult{Active: true, Ruleset: "table inet hosbah {\n}\n"}, ""
		default:
			return nil, "this fake agent does not run " + op.Kind
		}
	}
	startFakeAgent(t, ts.URL, srv, priv, respond)

	svcBase := base + "/servers/" + srv + "/firewall"

	var ruleA struct {
		ID string `json:"id"`
	}
	t.Run("add, list, update, remove a rule", func(t *testing.T) {
		viewer.mustJSON("POST", svcBase, map[string]any{"action": "allow", "proto": "tcp", "port_start": 5432, "port_end": 5432,
			"source_cidr": "10.0.0.0/24", "comment": "postgres"}, 403, nil)
		owner.mustJSON("POST", svcBase, map[string]any{"action": "allow", "proto": "tcp", "port_start": 5432, "port_end": 5432,
			"source_cidr": "10.0.0.0/24", "comment": "postgres"}, 201, &ruleA)
		if ruleA.ID == "" {
			t.Fatalf("created rule: %+v", ruleA)
		}

		var list struct {
			Rules []struct {
				ID      string `json:"id"`
				Action  string `json:"action"`
				Comment string `json:"comment"`
			} `json:"rules"`
		}
		viewer.mustJSON("GET", svcBase, nil, 200, &list) // any member reads
		if len(list.Rules) != 1 || list.Rules[0].ID != ruleA.ID || list.Rules[0].Comment != "postgres" {
			t.Fatalf("list: %+v", list.Rules)
		}

		var updated struct {
			Comment string `json:"comment"`
		}
		owner.mustJSON("PATCH", svcBase+"/"+ruleA.ID, map[string]any{"action": "allow", "proto": "tcp", "port_start": 5432,
			"port_end": 5432, "source_cidr": "10.0.0.0/24", "comment": "postgres (updated)"}, 200, &updated)
		if updated.Comment != "postgres (updated)" {
			t.Fatalf("updated: %+v", updated)
		}

		viewer.mustJSON("DELETE", svcBase+"/"+ruleA.ID, nil, 403, nil)
		owner.mustJSON("DELETE", svcBase+"/"+ruleA.ID, nil, 204, nil)
		var afterDelete struct {
			Rules []any `json:"rules"`
		}
		owner.mustJSON("GET", svcBase, nil, 200, &afterDelete)
		if len(afterDelete.Rules) != 0 {
			t.Fatalf("rule still listed after delete: %+v", afterDelete.Rules)
		}
	})

	t.Run("presets", func(t *testing.T) {
		var presets struct {
			Presets []struct{ Name string } `json:"presets"`
		}
		owner.mustJSON("GET", svcBase+"/presets", nil, 200, &presets)
		if len(presets.Presets) == 0 {
			t.Fatalf("no presets returned")
		}
		var added struct {
			ID string `json:"id"`
		}
		owner.mustJSON("POST", svcBase+"/presets/web", map[string]any{}, 201, &added)
		if added.ID == "" {
			t.Fatalf("preset add: %+v", added)
		}
		if status, text := owner.do("POST", svcBase+"/presets/no-such-preset", map[string]any{}); status != 422 {
			t.Fatalf("unknown preset: %d (want 422): %s", status, text)
		}
	})

	t.Run("settings: default policy and ssh port", func(t *testing.T) {
		var got struct {
			DefaultPolicy string `json:"default_policy"`
			SSHPort       int    `json:"ssh_port"`
		}
		owner.mustJSON("GET", svcBase+"/settings", nil, 200, &got)
		if got.DefaultPolicy != "accept" || got.SSHPort != 22 {
			t.Fatalf("default settings: %+v", got)
		}
		if status, text := owner.do("PUT", svcBase+"/settings", map[string]any{"default_policy": "drop"}); status != 422 {
			t.Fatalf("drop without confirm: %d (want 422): %s", status, text)
		}
		owner.mustJSON("PUT", svcBase+"/settings", map[string]any{"default_policy": "drop", "confirm": "drop", "ssh_port": 2222}, 200, &got)
		if got.DefaultPolicy != "drop" || got.SSHPort != 2222 {
			t.Fatalf("after drop: %+v", got)
		}
		// Put it back to accept for the apply sub-test below.
		owner.mustJSON("PUT", svcBase+"/settings", map[string]any{"default_policy": "accept", "ssh_port": 22}, 200, &got)
	})

	t.Run("apply and status, through the fake agent", func(t *testing.T) {
		var applied agentproto.FirewallApplyResult
		viewer.mustJSON("POST", svcBase+"/apply", map[string]any{}, 403, nil)
		owner.mustJSON("POST", svcBase+"/apply", map[string]any{}, 200, &applied)
		if applied.SSHPort != 22 || !strings.Contains(applied.Ruleset, "table inet hosbah") {
			t.Fatalf("apply result: %+v", applied)
		}
		var status agentproto.FirewallStatusResult
		owner.mustJSON("GET", svcBase+"/status", nil, 200, &status)
		if !status.Active {
			t.Fatalf("status: %+v", status)
		}
	})

	t.Run("bad input", func(t *testing.T) {
		if status, text := owner.do("POST", svcBase, map[string]any{"action": "smash", "proto": "tcp", "port_start": 1, "port_end": 1}); status != 422 {
			t.Fatalf("bad action: %d (want 422): %s", status, text)
		}
		if status, text := owner.do("POST", svcBase, map[string]any{"action": "allow", "proto": "tcp", "port_start": 100, "port_end": 1}); status != 422 {
			t.Fatalf("start > end: %d (want 422): %s", status, text)
		}
		if status, text := owner.do("POST", svcBase, map[string]any{"action": "allow", "proto": "tcp", "port_start": 1, "port_end": 1,
			"source_cidr": "not a cidr; rm -rf /"}); status != 422 {
			t.Fatalf("bad cidr: %d (want 422): %s", status, text)
		}
	})

	t.Run("cross-team isolation", func(t *testing.T) {
		stranger.mustJSON("GET", svcBase, nil, 404, nil)
		stranger.mustJSON("POST", svcBase, map[string]any{"action": "allow", "proto": "tcp", "port_start": 1, "port_end": 1}, 404, nil)
		stranger.mustJSON("GET", svcBase+"/settings", nil, 404, nil)
		stranger.mustJSON("POST", svcBase+"/apply", map[string]any{}, 404, nil)
		stranger.mustJSON("GET", svcBase+"/status", nil, 404, nil)
	})
}
