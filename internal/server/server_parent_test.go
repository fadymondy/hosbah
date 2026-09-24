package server

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
)

// enrolServerWithFacts is enrolServer, but the caller supplies the full Facts (for guest
// discovery: Services of kind "guest").
func enrolServerWithFacts(t *testing.T, c *client, base, teamID, name string, facts agentproto.Facts) string {
	t.Helper()
	var created struct {
		Server  struct{ ID string }
		Install struct{ URL string }
	}
	c.mustJSON("POST", "/api/teams/"+teamID+"/servers", map[string]any{"name": name, "provider": "byo"}, 201, &created)
	token := created.Install.URL[strings.LastIndex(created.Install.URL, "/")+1:]
	pub, _, _ := ed25519.GenerateKey(nil)
	facts.Hostname = name
	if facts.OSID == "" {
		facts.OSID, facts.OSVersion, facts.Arch = "ubuntu", "24.04", "amd64"
	}
	if facts.MemTotalBytes == 0 {
		facts.MemTotalBytes = 2 << 30
	}
	if facts.CPUs == 0 {
		facts.CPUs = 2
	}
	facts.Systemd = true
	body, _ := json.Marshal(agentproto.EnrollRequest{Token: token, PublicKey: base64.StdEncoding.EncodeToString(pub), AgentVersion: "test", Facts: facts})
	resp, err := http.Post(base+"/agent/enroll", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("enrol %s: %d", name, resp.StatusCode)
	}
	return created.Server.ID
}

// A guest server's link to its Proxmox node: manual PUT .../parent, and auto-link from the node's
// discovered guests (MH-574 A1).
func TestServerParent(t *testing.T) {
	ts, cleanup := bootTestServer(t)
	defer cleanup()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	short := suffix[len(suffix)-6:]
	ua := newClient(t, ts.URL)
	ua.register("parent-a-" + suffix + "@example.test")
	var team struct{ ID string }
	ua.mustJSON("POST", "/api/teams", map[string]any{"name": "ParentTeam " + suffix}, 201, &team)

	node, _ := enrolServer(t, ua, ts.URL, team.ID, "node-"+short)
	guestBSrv, _ := enrolServer(t, ua, ts.URL, team.ID, "guest-b-"+short)

	// ---- manual link -----------------------------------------------------------------------
	base := "/api/teams/" + team.ID + "/servers/" + guestBSrv + "/parent"
	var out struct {
		ID             string  `json:"id"`
		ParentServerID *string `json:"parent_server_id"`
		GuestVMID      *int64  `json:"guest_vmid"`
	}
	ua.mustJSON("PUT", base, map[string]any{"parent_server_id": node, "guest_vmid": 121}, 200, &out)
	if out.ParentServerID == nil || *out.ParentServerID != node {
		t.Fatalf("expected parent_server_id %s, got %+v", node, out.ParentServerID)
	}
	if out.GuestVMID == nil || *out.GuestVMID != 121 {
		t.Fatalf("expected guest_vmid 121, got %+v", out.GuestVMID)
	}

	var detail struct {
		ParentServerID   *string `json:"parent_server_id"`
		ParentServerName *string `json:"parent_server_name"`
	}
	ua.mustJSON("GET", "/api/teams/"+team.ID+"/servers/"+guestBSrv, nil, 200, &detail)
	if detail.ParentServerID == nil || *detail.ParentServerID != node {
		t.Fatalf("server detail did not keep the parent link: %+v", detail)
	}
	if detail.ParentServerName == nil || !strings.Contains(*detail.ParentServerName, "node-") {
		t.Fatalf("server detail did not resolve the parent's name: %+v", detail)
	}

	// A guest cannot be its own parent, and a server outside the team is not a valid parent.
	if status, _ := ua.do("PUT", base, map[string]any{"parent_server_id": guestBSrv}); status != 422 {
		t.Errorf("guest as its own parent -> %d (want 422)", status)
	}

	// ---- unlink -----------------------------------------------------------------------------
	ua.mustJSON("PUT", base, map[string]any{"parent_server_id": nil}, 200, &out)
	if out.ParentServerID != nil {
		t.Fatalf("expected parent_server_id cleared, got %+v", out.ParentServerID)
	}

	// ---- cross-team: a server of another team is not a valid parent, and the route 404s on
	// another team's server id -------------------------------------------------------------
	ub := newClient(t, ts.URL)
	ub.register("parent-b-" + suffix + "@example.test")
	var teamB struct{ ID string }
	ub.mustJSON("POST", "/api/teams", map[string]any{"name": "ParentTeamB " + suffix}, 201, &teamB)
	if status, _ := ub.do("PUT", "/api/teams/"+teamB.ID+"/servers/"+guestBSrv+"/parent", map[string]any{"parent_server_id": node}); status != 404 {
		t.Errorf("PUT .../parent on another team's server -> %d (want 404)", status)
	}
	if status, _ := ua.do("PUT", base, map[string]any{"parent_server_id": "does-not-exist"}); status != 422 {
		t.Errorf("PUT .../parent with an unknown parent -> %d (want 422)", status)
	}
}

// A node's discovered guests are auto-linked to the team's other servers (by IP, then hostname vs
// guest name), and both the node's guests list and the linked guest's own record reflect it —
// unless the link was set by hand, which auto-link must never touch.
func TestServerAutoLinkGuests(t *testing.T) {
	ts, cleanup := bootTestServer(t)
	defer cleanup()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	short := suffix[len(suffix)-6:]
	ua := newClient(t, ts.URL)
	ua.register("autolink-" + suffix + "@example.test")
	var team struct{ ID string }
	ua.mustJSON("POST", "/api/teams", map[string]any{"name": "AutoLinkTeam " + suffix}, 201, &team)

	// The guest server enrols first, its hostname reported as its own name.
	guestName := "guest-" + short
	guest, _ := enrolServer(t, ua, ts.URL, team.ID, guestName)

	// A server auto-link must never touch: linked by hand to nothing in particular, just marked manual.
	manualGuest, _ := enrolServer(t, ua, ts.URL, team.ID, "manual-"+short)
	ua.mustJSON("PUT", "/api/teams/"+team.ID+"/servers/"+manualGuest+"/parent", map[string]any{"parent_server_id": nil}, 200, nil)

	// The node reports both as Proxmox guests by name; only the non-manual one should link.
	node := enrolServerWithFacts(t, ua, ts.URL, team.ID, "pve-"+short, agentproto.Facts{
		Proxmox: agentproto.ProxmoxFacts{Installed: true, Version: "8.2"},
		Services: []agentproto.Service{
			{Kind: "guest", ID: "121", Type: "lxc", Name: guestName, Status: "running", Address: "10.10.10.121"},
			{Kind: "guest", ID: "122", Type: "lxc", Name: "manual-" + short, Status: "running", Address: "10.10.10.122"},
		},
	})

	var guestDetail struct {
		ParentServerID *string `json:"parent_server_id"`
		GuestVMID      *int64  `json:"guest_vmid"`
		GuestType      *string `json:"guest_type"`
	}
	ua.mustJSON("GET", "/api/teams/"+team.ID+"/servers/"+guest, nil, 200, &guestDetail)
	if guestDetail.ParentServerID == nil || *guestDetail.ParentServerID != node {
		t.Fatalf("auto-link did not link the guest to its node: %+v", guestDetail)
	}
	if guestDetail.GuestVMID == nil || *guestDetail.GuestVMID != 121 {
		t.Fatalf("auto-link did not record the guest's vmid: %+v", guestDetail)
	}
	if guestDetail.GuestType == nil || *guestDetail.GuestType != "lxc" {
		t.Fatalf("auto-link did not record the guest's type: %+v", guestDetail)
	}

	var manualDetail struct {
		ParentServerID *string `json:"parent_server_id"`
	}
	ua.mustJSON("GET", "/api/teams/"+team.ID+"/servers/"+manualGuest, nil, 200, &manualDetail)
	if manualDetail.ParentServerID != nil {
		t.Fatalf("auto-link overwrote a manual link: %+v", manualDetail)
	}

	// The node's guests output (list + detail + the dedicated endpoint) names the enrolled guest.
	var guestsOut struct {
		Guests []struct {
			VMID     string  `json:"vmid"`
			Name     string  `json:"name"`
			ServerID *string `json:"server_id"`
		} `json:"guests"`
	}
	ua.mustJSON("GET", "/api/teams/"+team.ID+"/servers/"+node+"/guests", nil, 200, &guestsOut)
	found := false
	for _, g := range guestsOut.Guests {
		if g.VMID == "121" {
			found = true
			if g.ServerID == nil || *g.ServerID != guest {
				t.Errorf("guest 121's server_id not resolved: %+v", g)
			}
		}
	}
	if !found {
		t.Fatalf("guest 121 missing from /guests: %+v", guestsOut.Guests)
	}

	var nodeDetail struct {
		Guests []struct {
			VMID string `json:"vmid"`
		} `json:"guests"`
	}
	ua.mustJSON("GET", "/api/teams/"+team.ID+"/servers/"+node, nil, 200, &nodeDetail)
	if len(nodeDetail.Guests) != 2 {
		t.Fatalf("expected 2 guests on the node's own detail view, got %+v", nodeDetail.Guests)
	}
}
