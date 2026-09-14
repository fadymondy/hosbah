package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fadymondy/cloudy/internal/scopes"
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
