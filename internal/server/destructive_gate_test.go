package server

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

/*
The P4 destructive gate, end to end over the real /api/mcp transport.

Everything here goes through HTTP and a real Postgres, because the parts most worth testing are
the ones a unit test would stub away: that the approval row is a ONE-SHOT capability enforced by
a UPDATE … WHERE status = 'approved' rather than by Go, that a refusal is still audited after its
transaction rolls back, and that the whole thing is invisible until the team turns it on.
*/
func TestDestructiveGate(t *testing.T) {
	ts, stop := bootTestServer(t)
	defer stop()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	owner := newClient(t, ts.URL)
	owner.register("gate-owner-" + suffix + "@example.test")

	var team struct{ ID, Slug string }
	owner.mustJSON("POST", "/api/teams", map[string]any{"name": "Gate " + suffix}, 201, &team)

	// A token with every scope: this test is about the gate, not about scopes.
	var tok struct{ ID, Token string }
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/mcp-tokens", map[string]any{
		"name": "gate-all", "scopes": []string{"read", "deploy", "write", "secrets:read", "destructive"}, "expires_in_days": 30,
	}, 201, &tok)

	// Something destructible: a server with no apps on it, so delete_server's own guard is not
	// what refuses the call.
	srvID, _ := enrolledServerWithOp(t, owner, ts.URL, team.ID, "gate-"+suffix)
	_ = srvID
	const srvName = "gate-"
	serverName := srvName + suffix

	// ---- approval off (the default): the call goes straight through --------------------------

	t.Run("confirm must match even with the scope", func(t *testing.T) {
		res := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "delete_server",
			map[string]any{"server": serverName, "confirm": "not-the-name"})
		if !res.IsError || !strings.Contains(res.Text, "confirm must equal") {
			t.Fatalf("a wrong confirm must refuse in-band, got isError=%v %q", res.IsError, res.Text)
		}
		// And it really did nothing.
		if list := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "list_servers", nil); !strings.Contains(list.Text, serverName) {
			t.Fatalf("the server should still exist after a refused delete, got %q", list.Text)
		}
	})

	t.Run("a refusal is audited with the token id", func(t *testing.T) {
		// The refusal above rolled its transaction back; the audit entry must have survived that.
		res := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "list_audit", map[string]any{"limit": 50})
		if res.IsError {
			t.Fatalf("list_audit: %s", res.Text)
		}
		if !strings.Contains(res.Text, "mcp.destructive.refused") {
			t.Fatalf("a refused destructive call must be audited, got %q", res.Text)
		}
	})

	// ---- approval on -------------------------------------------------------------------------

	owner.mustJSON("PATCH", "/api/teams/"+team.ID, map[string]any{"require_destructive_approval": true}, 200, nil)

	var requestID string

	t.Run("a destructive call is held, not run", func(t *testing.T) {
		res := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "delete_server",
			map[string]any{"server": serverName, "confirm": serverName})
		if !res.IsError || !strings.Contains(res.Text, "needs approval") {
			t.Fatalf("expected an in-band hold, got isError=%v %q", res.IsError, res.Text)
		}
		if list := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "list_servers", nil); !strings.Contains(list.Text, serverName) {
			t.Fatalf("a held call must not have deleted anything, got %q", list.Text)
		}

		var out struct {
			Requests []struct {
				ID           string `json:"id"`
				Tool         string `json:"tool"`
				Status       string `json:"status"`
				ResourceName string `json:"resource_name"`
				TokenID      string `json:"token_id"`
			} `json:"requests"`
		}
		owner.mustJSON("GET", "/api/teams/"+team.ID+"/approvals?status=pending", nil, 200, &out)
		if len(out.Requests) != 1 {
			t.Fatalf("expected exactly one pending request, got %+v", out.Requests)
		}
		r := out.Requests[0]
		if r.Tool != "delete_server" || r.ResourceName != serverName || r.Status != "pending" {
			t.Fatalf("the request does not describe the call: %+v", r)
		}
		if r.TokenID != tok.ID {
			t.Fatalf("the request must name the MCP token that asked: got %q want %q", r.TokenID, tok.ID)
		}
		requestID = r.ID
	})

	t.Run("retrying while pending does not queue a second ask", func(t *testing.T) {
		if res := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "delete_server",
			map[string]any{"server": serverName, "confirm": serverName}); !res.IsError {
			t.Fatalf("a retry while pending must still be refused, got %q", res.Text)
		}
		var out struct {
			Requests []struct{ ID string } `json:"requests"`
		}
		owner.mustJSON("GET", "/api/teams/"+team.ID+"/approvals?status=pending", nil, 200, &out)
		if len(out.Requests) != 1 || out.Requests[0].ID != requestID {
			t.Fatalf("a retry must reuse its own pending request, got %+v", out.Requests)
		}
	})

	t.Run("approved, the same call runs exactly once", func(t *testing.T) {
		owner.mustJSON("POST", "/api/teams/"+team.ID+"/approvals/"+requestID+"/approve", nil, 200, nil)

		res := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "delete_server",
			map[string]any{"server": serverName, "confirm": serverName})
		if res.IsError {
			t.Fatalf("an approved call must go through, got %q", res.Text)
		}
		if list := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "list_servers", nil); strings.Contains(list.Text, serverName) {
			t.Fatalf("the server should be gone, got %q", list.Text)
		}

		// The approval is spent: it cannot authorise anything a second time. (The tool now fails
		// for its own reason too — the server is gone — so the point is that the request's status
		// is terminal, not that the second call errored.)
		var out struct {
			Requests []struct{ ID, Status string } `json:"requests"`
		}
		owner.mustJSON("GET", "/api/teams/"+team.ID+"/approvals", nil, 200, &out)
		for _, r := range out.Requests {
			if r.ID == requestID && r.Status != "used" {
				t.Fatalf("an approval that authorised a call must be 'used', got %q", r.Status)
			}
		}
	})

	t.Run("an allowed destructive call is audited with the token id", func(t *testing.T) {
		res := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "list_audit", map[string]any{"limit": 50})
		if res.IsError {
			t.Fatalf("list_audit: %s", res.Text)
		}
		if !strings.Contains(res.Text, "mcp.destructive.allowed") {
			t.Fatalf("an allowed destructive call must be audited, got %q", res.Text)
		}
		if !strings.Contains(res.Text, "mcp.destructive.held") {
			t.Fatalf("a held destructive call must be audited, got %q", res.Text)
		}
	})

	t.Run("rejecting is terminal", func(t *testing.T) {
		// A fresh resource and a fresh ask, this time rejected.
		name2 := "gate2-" + suffix
		enrolledServerWithOp(t, owner, ts.URL, team.ID, name2)

		if res := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "delete_server",
			map[string]any{"server": name2, "confirm": name2}); !res.IsError {
			t.Fatalf("expected a hold, got %q", res.Text)
		}
		var out struct {
			Requests []struct {
				ID           string `json:"id"`
				ResourceName string `json:"resource_name"`
			} `json:"requests"`
		}
		owner.mustJSON("GET", "/api/teams/"+team.ID+"/approvals?status=pending", nil, 200, &out)
		id := ""
		for _, r := range out.Requests {
			if r.ResourceName == name2 {
				id = r.ID
			}
		}
		if id == "" {
			t.Fatalf("no pending request for %s: %+v", name2, out.Requests)
		}
		owner.mustJSON("POST", "/api/teams/"+team.ID+"/approvals/"+id+"/reject", nil, 200, nil)

		res := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "delete_server",
			map[string]any{"server": name2, "confirm": name2})
		if !res.IsError {
			t.Fatalf("a rejected request must not let the call through, got %q", res.Text)
		}
		if list := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "list_servers", nil); !strings.Contains(list.Text, name2) {
			t.Fatalf("%s should still exist after a rejection, got %q", name2, list.Text)
		}
	})
}
