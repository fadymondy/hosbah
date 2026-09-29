package server

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
Connect guest: putting a Hosbah agent inside an imported Proxmox guest, instead of a git app landing
on the hypervisor node itself (internal/deploy/connectguest.go). The node's agent is asked, over the
already-typed and signed agent protocol, to run the new server's one-time install command inside the
guest with `pct exec` — the token never travels in operations.params in the clear (it is sealed to
the node's own agent key, agentproto.SealBox).

Also covered here: the duplicate guard on create_app (an imported app already answers to this name
or repo — offer connect/adopt instead, unless allow_duplicate is passed) and the refusal to target a
Proxmox node itself with a git app.
*/
func TestConnectGuest(t *testing.T) {
	ts, stop := bootTestServer(t)
	defer stop()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	owner := newClient(t, ts.URL)
	owner.register("cg-owner-" + suffix + "@example.test")
	var team struct{ ID, Slug string }
	owner.mustJSON("POST", "/api/teams", map[string]any{"name": "ConnectGuest " + suffix}, 201, &team)
	var inv struct {
		AcceptURL string `json:"accept_url"`
	}
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/invitations", map[string]any{"email": "cg-viewer-" + suffix + "@example.test", "role": "viewer"}, 201, &inv)
	viewer := newClient(t, ts.URL)
	viewer.register("cg-viewer-" + suffix + "@example.test")
	viewer.mustJSON("POST", "/api/invitations/"+inv.AcceptURL[strings.LastIndex(inv.AcceptURL, "/")+1:]+"/accept", nil, 200, nil)

	node, priv := enrolWithServicesKey(t, owner, ts.URL, team.ID, "cg-node-"+suffix, "10.8.8.8", []agentproto.Service{
		{Kind: "guest", Name: "cg-guest", ID: "301", Type: "lxc", Status: "running", Address: "10.0.0.31"},
		{Kind: "container", Name: "cg-web", Image: "ghcr.io/acme/cg-web:1", Status: "running", Ports: []string{"0.0.0.0:8082->3000/tcp"}},
	})

	agent := startFakeAgent(t, ts.URL, node, priv, func(op agentproto.SignedOp) (any, string) {
		if op.Kind == agentproto.KindGuestEnrol {
			return agentproto.GuestEnrolResult{ExitCode: 0}, ""
		}
		return map[string]any{"ok": true}, ""
	})

	apps, _ := listImported(t, owner, team.ID, node)
	guestApp, containerApp := apps["cg-guest"], apps["cg-web"]
	if guestApp.ID == "" || containerApp.ID == "" {
		t.Fatalf("imports: %v", keys(apps))
	}
	guestPath := "/api/teams/" + team.ID + "/apps/" + guestApp.ID + "/connect-guest"
	containerPath := "/api/teams/" + team.ID + "/apps/" + containerApp.ID + "/connect-guest"

	t.Run("viewers cannot connect a guest", func(t *testing.T) {
		viewer.expect("POST", guestPath, nil, 403)
	})

	t.Run("refused for a non-guest imported app", func(t *testing.T) {
		owner.expectCode("POST", containerPath, nil, 422, "unsupported_origin")
	})

	var connectedServerID string
	var installURL string
	t.Run("connecting creates a nested server and queues guest.enrol on the node, with no plaintext token in operations.params", func(t *testing.T) {
		var out struct {
			Server struct {
				ID             string  `json:"id"`
				Name           string  `json:"name"`
				ParentServerID *string `json:"parent_server_id"`
			} `json:"server"`
			Install *struct {
				URL         string `json:"url"`
				CommandRoot string `json:"command_root"`
			} `json:"install"`
		}
		owner.mustJSON("POST", guestPath, nil, 200, &out)
		if out.Server.ID == "" || out.Server.ID == node {
			t.Fatalf("expected a new server distinct from the node: %+v", out.Server)
		}
		if out.Server.ParentServerID == nil || *out.Server.ParentServerID != node {
			t.Fatalf("the new server must be nested under the node: %+v", out.Server)
		}
		if out.Install == nil || out.Install.CommandRoot == "" {
			t.Fatal("expected a one-time install command on first connect")
		}
		connectedServerID = out.Server.ID
		installURL = out.Install.URL
		token := installURL[strings.LastIndex(installURL, "/")+1:]
		if token == "" {
			t.Fatalf("could not extract the install token from %q", installURL)
		}

		// Find the guest.enrol op the node received, and confirm its params never carry the token.
		var found *agentproto.SignedOp
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			for _, k := range agentOps(agent, agentproto.KindGuestEnrol) {
				kk := k
				found = &kk
			}
			if found != nil {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if found == nil {
			t.Fatalf("the node never received guest.enrol (rejected: %v)", agent.rejectedList())
		}
		if strings.Contains(string(found.Params), token) {
			t.Fatalf("operations.params (in-flight op) leaked the install token: %s", found.Params)
		}

		// The persisted row must not carry the token either.
		sqlDB := testDB(t)
		var paramsText string
		err := tenancy.Tx(context.Background(), sqlDB, tenancy.Scope{TeamID: team.ID}, func(_ *db.Queries, tx *sql.Tx) error {
			return tx.QueryRow(`SELECT params::text FROM operations WHERE team_id = $1 AND kind = $2 ORDER BY created_at DESC LIMIT 1`,
				team.ID, agentproto.KindGuestEnrol).Scan(&paramsText)
		})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(paramsText, token) {
			t.Fatalf("operations.params leaked the install token: %s", paramsText)
		}
	})

	t.Run("connecting again is idempotent: returns the same server, no new install", func(t *testing.T) {
		var out struct {
			Server  struct{ ID string } `json:"server"`
			Install *struct{ URL string } `json:"install"`
		}
		owner.mustJSON("POST", guestPath, nil, 200, &out)
		if out.Server.ID != connectedServerID {
			t.Fatalf("expected the same server %s, got %s", connectedServerID, out.Server.ID)
		}
		if out.Install != nil {
			t.Fatalf("a second connect must not mint a new install command: %+v", out.Install)
		}
	})

	// ---- duplicate guard -------------------------------------------------------------------
	// A plain, non-Proxmox server: the node itself would separately refuse any git app (it is
	// the hypervisor), which is exercised on its own below.
	other := enrolWithServices(t, owner, ts.URL, team.ID, "cg-other-"+suffix, nil)
	t.Run("creating a git app whose repo name matches an imported app is refused, with an override", func(t *testing.T) {
		// A different app name (so it never collides with apps_team_name_key on its own), but its
		// repository is named after the imported guest — the case the duplicate guard exists for.
		body := map[string]any{"name": "cg-guest-app-" + suffix, "server": other, "repo_url": "https://github.com/acme/cg-guest"}
		status, text := owner.do("POST", "/api/teams/"+team.ID+"/apps", body)
		if status != 409 {
			t.Fatalf("POST create-app -> %d (want 409): %s", status, text)
		}
		if !strings.Contains(text, "duplicate_imported") {
			t.Fatalf("expected code duplicate_imported: %s", text)
		}
		if !strings.Contains(text, "connect") && !strings.Contains(text, "adopt") {
			t.Fatalf("expected the refusal to offer connect/adopt: %s", text)
		}
		body["allow_duplicate"] = true
		var created struct {
			App struct{ ID, Name string } `json:"app"`
		}
		owner.mustJSON("POST", "/api/teams/"+team.ID+"/apps", body, 201, &created)
		if created.App.Name != "cg-guest-app-"+suffix {
			t.Fatalf("expected the app to be created as cg-guest-app-%s: %+v", suffix, created.App)
		}
	})

	// ---- Proxmox node as a git app target -------------------------------------------------
	t.Run("targeting the Proxmox node itself with a git app is refused", func(t *testing.T) {
		body := map[string]any{"name": "cg-on-node-" + suffix, "server": node, "repo_url": "https://github.com/acme/somewhere-else"}
		owner.expect("POST", "/api/teams/"+team.ID+"/apps", body, 422)
	})
}
