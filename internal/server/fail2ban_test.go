package server

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

/*
A server's fail2ban sshd jail (MH-580 J2), end to end against a real fake agent: default settings
before any install, install/configure through the agent (with settings saved for next time), the
banned-IP list, unban, role enforcement, input validation, and cross-team isolation.
*/
func TestFail2ban(t *testing.T) {
	ts, stop := bootTestServer(t)
	defer stop()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	short := suffix[len(suffix)-6:]

	owner := newClient(t, ts.URL)
	owner.register("fail2ban-owner-" + suffix + "@example.test")
	var team struct{ ID string }
	owner.mustJSON("POST", "/api/teams", map[string]any{"name": "Fail2ban " + suffix}, 201, &team)
	base := "/api/teams/" + team.ID

	viewerEmail := "fail2ban-viewer-" + suffix + "@example.test"
	viewer := newClient(t, ts.URL)
	viewer.register(viewerEmail)
	var inv struct {
		AcceptURL string `json:"accept_url"`
	}
	owner.mustJSON("POST", base+"/invitations", map[string]any{"email": viewerEmail, "role": "viewer"}, 201, &inv)
	viewer.mustJSON("POST", "/api/invitations/"+inv.AcceptURL[strings.LastIndex(inv.AcceptURL, "/")+1:]+"/accept", nil, 200, nil)

	stranger := newClient(t, ts.URL)
	stranger.register("fail2ban-stranger-" + suffix + "@example.test")
	var otherTeam struct{ ID string }
	stranger.mustJSON("POST", "/api/teams", map[string]any{"name": "Fail2banElsewhere " + suffix}, 201, &otherTeam)

	srv, priv := enrolWithServicesKey(t, owner, ts.URL, team.ID, "fail2ban-a-"+short, "203.0.113.70", nil)

	banned := []string{"10.0.0.5", "10.0.0.6"}
	respond := func(op agentproto.SignedOp) (any, string) {
		switch op.Kind {
		case agentproto.KindFail2banInstall:
			return agentproto.Fail2banInstallResult{Installed: true, Active: true}, ""
		case agentproto.KindFail2banBanned:
			return agentproto.Fail2banBannedResult{Jail: agentproto.SSHJail, Active: true, Banned: banned}, ""
		case agentproto.KindFail2banUnban:
			return agentproto.Fail2banUnbanResult{IP: "10.0.0.5", Unbanned: true}, ""
		default:
			return nil, "this fake agent does not run " + op.Kind
		}
	}
	startFakeAgent(t, ts.URL, srv, priv, respond)

	svcBase := base + "/servers/" + srv + "/fail2ban"

	t.Run("default settings before install", func(t *testing.T) {
		var got struct {
			Enabled  bool `json:"enabled"`
			Bantime  int  `json:"bantime"`
			Findtime int  `json:"findtime"`
			Maxretry int  `json:"maxretry"`
		}
		owner.mustJSON("GET", svcBase+"/settings", nil, 200, &got)
		if got.Enabled || got.Bantime != 600 || got.Findtime != 600 || got.Maxretry != 5 {
			t.Fatalf("default settings: %+v", got)
		}
		viewer.mustJSON("GET", svcBase+"/settings", nil, 200, nil) // any member reads
	})

	t.Run("install through the agent, then read back", func(t *testing.T) {
		viewer.mustJSON("POST", svcBase+"/settings", map[string]any{"enabled": true, "bantime": 3600, "findtime": 600, "maxretry": 3}, 403, nil)

		var res agentproto.Fail2banInstallResult
		owner.mustJSON("POST", svcBase+"/settings", map[string]any{"enabled": true, "bantime": 3600, "findtime": 600, "maxretry": 3}, 200, &res)
		if !res.Installed || !res.Active {
			t.Fatalf("install result: %+v", res)
		}

		var got struct {
			Enabled bool `json:"enabled"`
			Bantime int  `json:"bantime"`
		}
		owner.mustJSON("GET", svcBase+"/settings", nil, 200, &got)
		if !got.Enabled || got.Bantime != 3600 {
			t.Fatalf("read back: %+v", got)
		}
	})

	t.Run("banned list and unban", func(t *testing.T) {
		var list agentproto.Fail2banBannedResult
		owner.mustJSON("GET", svcBase+"/banned", nil, 200, &list)
		if !list.Active || len(list.Banned) != 2 || list.Banned[0] != "10.0.0.5" {
			t.Fatalf("banned list: %+v", list)
		}
		viewer.mustJSON("GET", svcBase+"/banned", nil, 200, nil) // any member reads

		var unbanned agentproto.Fail2banUnbanResult
		viewer.mustJSON("POST", svcBase+"/unban", map[string]any{"ip": "10.0.0.5"}, 403, nil)
		owner.mustJSON("POST", svcBase+"/unban", map[string]any{"ip": "10.0.0.5"}, 200, &unbanned)
		if !unbanned.Unbanned || unbanned.IP != "10.0.0.5" {
			t.Fatalf("unban result: %+v", unbanned)
		}
	})

	t.Run("bad input", func(t *testing.T) {
		if status, text := owner.do("POST", svcBase+"/settings", map[string]any{"enabled": true, "bantime": 1, "findtime": 600, "maxretry": 3}); status != 422 {
			t.Fatalf("bantime out of range: %d (want 422): %s", status, text)
		}
		if status, text := owner.do("POST", svcBase+"/unban", map[string]any{"ip": "not-an-ip"}); status != 422 {
			t.Fatalf("bad ip: %d (want 422): %s", status, text)
		}
	})

	t.Run("cross-team isolation", func(t *testing.T) {
		stranger.mustJSON("GET", svcBase+"/settings", nil, 404, nil)
		stranger.mustJSON("POST", svcBase+"/settings", map[string]any{"enabled": true, "bantime": 600, "findtime": 600, "maxretry": 5}, 404, nil)
		stranger.mustJSON("GET", svcBase+"/banned", nil, 404, nil)
		stranger.mustJSON("POST", svcBase+"/unban", map[string]any{"ip": "10.0.0.5"}, 404, nil)
	})
}
