package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fadymondy/hosbah/internal/scopes"
)

// Every tool on both servers declares a scope, and only one its server grants. This is the
// fix for fadymondy.com's FM-22 (a tool with no scope was callable by any token): a tool
// without a scope fails this test, so it cannot ship.
func TestEveryToolDeclaresAValidScope(t *testing.T) {
	for _, srv := range []*Server{UserServer(), AdminServer()} {
		seen := map[string]bool{}
		for _, tool := range srv.Tools {
			if tool.Name == "" {
				t.Errorf("%s: a tool has no name", srv.Name)
			}
			if seen[tool.Name] {
				t.Errorf("%s: duplicate tool %q", srv.Name, tool.Name)
			}
			seen[tool.Name] = true
			if tool.Scope == "" {
				t.Errorf("%s/%s declares no scope", srv.Name, tool.Name)
				continue
			}
			if !contains(srv.AllowedScopes, tool.Scope) {
				t.Errorf("%s/%s declares scope %q, which %s does not grant", srv.Name, tool.Name, tool.Scope, srv.Name)
			}
			if tool.Run == nil {
				t.Errorf("%s/%s has no Run", srv.Name, tool.Name)
			}
		}
	}
}

// The user server's destructive tools are exactly the ones with the destructive scope, and
// every destructive tool (on either server) requires a confirm argument in its schema.
func TestDestructiveToolsRequireConfirm(t *testing.T) {
	for _, tool := range UserServer().Tools {
		if (tool.Scope == scopes.Destructive) != tool.Destructive {
			t.Errorf("user/%s: Destructive=%v but scope %q", tool.Name, tool.Destructive, tool.Scope)
		}
	}
	for _, srv := range []*Server{UserServer(), AdminServer()} {
		for _, tool := range srv.Tools {
			if !tool.Destructive {
				continue
			}
			props, _ := tool.InputSchema["properties"].(map[string]any)
			req, _ := tool.InputSchema["required"].([]string)
			if _, ok := props["confirm"]; !ok || !contains(req, "confirm") {
				t.Errorf("%s/%s is destructive but its schema does not require confirm", srv.Name, tool.Name)
			}
		}
	}
}

/*
Every destructive tool names a ResourceType, and it is one the database will accept.

The gate files an approval request and writes an audit entry against this value, so a tool that
leaves it empty would file rows with an empty subject — or, once approval_requests_resource_type_check
rejects them, fail at the moment someone turns the team setting on. That is the worst possible time
to find out, so it is a build-time failure instead.
*/
func TestDestructiveToolsNameAKnownResourceType(t *testing.T) {
	// Mirrors approval_requests_resource_type_check in internal/db/schema/approval_request.sql.
	known := []string{"app", "server", "database", "domain", "data_service", "backup", "invitation", "member", "user", "team", "log_drain",
		"script", "script_run", "service", "site_template", "product", "product_instance", "ssh_key", "mail_relay_credential"}
	for _, srv := range []*Server{UserServer(), AdminServer()} {
		for _, tool := range srv.Tools {
			if !tool.Destructive {
				continue
			}
			if tool.ResourceType == "" {
				t.Errorf("%s/%s is destructive but names no ResourceType", srv.Name, tool.Name)
				continue
			}
			if !contains(known, tool.ResourceType) {
				t.Errorf("%s/%s has ResourceType %q, which approval_requests_resource_type_check does not allow",
					srv.Name, tool.Name, tool.ResourceType)
			}
		}
	}
}

// The plan's destructive set for the team server. Named explicitly so that removing a tool's
// destructive scope — the exact mistake that would silently un-gate a delete — fails here.
func TestDestructiveToolInventory(t *testing.T) {
	want := map[string]bool{
		"delete_app": true, "delete_server": true, "remove_domain": true,
		"restore_backup": true, "revoke_invitation": true, "remove_member": true,
		"drop_database": true, "delete_data_service": true, "db_user_delete": true, "db_execute": true,
		// Imported workloads: destroying the real guest or container, and deleting a guest snapshot.
		"permanently_destroy_app": true, "permanently_destroy_data_service": true, "delete_snapshot": true,
		"sftp_user_delete": true,
		// DB metrics and slow queries (MH-575 F6): cancelling a running query/backend.
		"db_cancel_query": true,
		"notification_channel_delete": true, "status_page_delete": true,
		"log_drain_delete": true,
		// MH-576 D4/D5/D3: deleting a script, running one, and a systemd service action.
		"delete_script": true, "run_script": true, "service_action": true,
		// MH-576 B11: deleting a team site template.
		"delete_site_template": true,
		// MH-576 D7: destroying a SaaS product's customer instance.
		"destroy_instance": true,
		// MH-580 J1: loading a firewall ruleset can cut a server off from everything not explicitly allowed.
		"server_firewall_apply": true,
		// MH-578 C8: running a package upgrade changes installed packages and may force a reboot.
		"server_updates_run": true,
		// MH-578 C10: removing a team SSH key, and installing/uninstalling one on a server.
		"ssh_keys_remove": true, "ssh_keys_install": true, "ssh_keys_uninstall": true,
		// MH-577 I6: revoking an SMTP relay credential removes its Stalwart principal.
		"mail_relay_revoke": true,
	}
	got := map[string]bool{}
	for _, tool := range UserServer().Tools {
		if tool.Destructive {
			got[tool.Name] = true
		}
	}
	for name := range want {
		if !got[name] {
			t.Errorf("%s is missing from the user server's destructive tools", name)
		}
	}
	for name := range got {
		if !want[name] {
			t.Errorf("%s is destructive but not in the expected inventory — add it here deliberately", name)
		}
	}
}

// An out-of-scope call fails in-band (a tools/call result with isError), never as a transport
// error, and never reaches the tool or the database.
func TestOutOfScopeCallFailsInBand(t *testing.T) {
	srv := UserServer()
	caller := Caller{UserID: "u", TeamID: "t", Scopes: []string{scopes.Read}}

	res := srv.Dispatch(context.Background(), nil, caller, rpcRequest{
		JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/call",
		Params: json.RawMessage(`{"name":"invite_member","arguments":{"email":"x@example.com","role":"viewer"}}`),
	})
	if res.Error != nil {
		t.Fatalf("expected an in-band result, got a JSON-RPC error: %+v", res.Error)
	}
	body, _ := json.Marshal(res.Result)
	if !strings.Contains(string(body), `"isError":true`) || !strings.Contains(string(body), `\"write\" scope`) {
		t.Fatalf("expected isError naming the missing write scope, got %s", body)
	}
}

// A destructive call without confirm is refused before the tool runs, even with the scope.
func TestDestructiveCallWithoutConfirmIsRefused(t *testing.T) {
	srv := UserServer()
	caller := Caller{UserID: "u", TeamID: "t", Scopes: scopes.All}
	res := srv.Dispatch(context.Background(), nil, caller, rpcRequest{
		JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/call",
		Params: json.RawMessage(`{"name":"remove_member","arguments":{"member_id":"m1"}}`),
	})
	body, _ := json.Marshal(res.Result)
	if !strings.Contains(string(body), `"isError":true`) || !strings.Contains(string(body), "confirm") {
		t.Fatalf("expected an in-band refusal asking for confirm, got %s", body)
	}
}

func TestInitializeNegotiatesVersion(t *testing.T) {
	srv := UserServer()
	for req, want := range map[string]string{
		`{"protocolVersion":"2024-11-05"}`: "2024-11-05",
		`{"protocolVersion":"2025-06-18"}`: "2025-06-18",
		`{"protocolVersion":"1999-01-01"}`: supportedVersions[0],
		`{}`:                               supportedVersions[0],
	} {
		res := srv.Dispatch(context.Background(), nil, Caller{}, rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "initialize", Params: json.RawMessage(req)})
		got := res.Result.(map[string]any)["protocolVersion"]
		if got != want {
			t.Errorf("initialize %s: got %v want %s", req, got, want)
		}
	}
}

func TestArgsStrAcceptsNumbers(t *testing.T) {
	a := Args{"deployment": float64(12), "s": " x ", "b": true, "big": float64(47001)}
	if a.Str("deployment") != "12" || a.Str("s") != "x" || a.Str("b") != "true" || a.Str("big") != "47001" || a.Str("none") != "" {
		t.Fatalf("got %q %q %q %q", a.Str("deployment"), a.Str("s"), a.Str("b"), a.Str("big"))
	}
}
