package server

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
	"github.com/fadymondy/hosbah/internal/tokens"
)

// A team MCP token minted before the rename from Cloudy (cld_…) still authenticates, exactly like a
// new one (hbu_…); a token that only swaps the prefix of another does not.
func TestAPreRenameMCPTokenStillWorks(t *testing.T) {
	ts, cleanup := bootTestServer(t)
	defer cleanup()
	sqlDB := testDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	ua := newClient(t, ts.URL)
	ua.register("rename-" + suffix + "@example.test")
	var team struct{ ID string }
	ua.mustJSON("POST", "/api/teams", map[string]any{"name": "Rename " + suffix}, 201, &team)

	var tok struct{ Token string }
	ua.mustJSON("POST", "/api/teams/"+team.ID+"/mcp-tokens", map[string]any{"name": "new", "scopes": []string{"read"}, "expires_in_days": 1}, 201, &tok)
	if !strings.HasPrefix(tok.Token, tokens.PrefixUserMCP) {
		t.Fatalf("a new token is %q, want the %s prefix", tok.Token, tokens.PrefixUserMCP)
	}
	if res := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "whoami", nil); res.IsError {
		t.Fatalf("the new token: %+v", res)
	}

	// Turn a second token into one minted before the rename: same secret, cld_ prefix, its hash stored.
	var second struct{ Token string }
	ua.mustJSON("POST", "/api/teams/"+team.ID+"/mcp-tokens", map[string]any{"name": "old", "scopes": []string{"read"}, "expires_in_days": 1}, 201, &second)
	legacy := tokens.LegacyPrefixUserMCP + strings.TrimPrefix(second.Token, tokens.PrefixUserMCP)
	err := tenancy.Tx(context.Background(), sqlDB, tenancy.Scope{System: true, TeamID: team.ID}, func(_ *db.Queries, tx *sql.Tx) error {
		res, err := tx.ExecContext(context.Background(), `UPDATE mcp_tokens SET token_hash = $1 WHERE token_hash = $2 AND team_id = $3`,
			tokens.Hash(legacy), tokens.Hash(second.Token), team.ID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return fmt.Errorf("updated %d rows", n)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if res := mcpCall(t, ts.URL, "/api/mcp", legacy, "whoami", nil); res.IsError {
		t.Fatalf("the pre-rename token: %+v", res)
	}
	// The prefix is part of the secret: the new-prefixed twin of the old token is not a token.
	if code := mcpStatus(ts.URL, "/api/mcp", second.Token); code != 401 {
		t.Fatalf("a token whose stored hash is another prefix's: %d", code)
	}
	if code := mcpStatus(ts.URL, "/api/mcp", "xyz_"+strings.TrimPrefix(second.Token, tokens.PrefixUserMCP)); code != 401 {
		t.Fatalf("an unknown prefix: %d", code)
	}
}
