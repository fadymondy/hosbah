package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/fadymondy/hosbah/internal/tokens"
)

// TestMCPOAuth covers OAuth 2.1 for MCP connectors (internal/api/mcp_oauth.go) end to end, the way
// claude.ai, Claude Code and ChatGPT use it: the 401 points at protected-resource metadata, which
// names the authorization server; the client registers itself (DCR, public, no secret), the person
// picks a team on the consent page (default: the one with servers and apps, not an empty one), the
// code trades for a 1-hour team MCP token plus a rotating refresh token bound to that team, and
// revoking ends the connection. Also: list_teams, and /api/me/mcp-tokens showing the team.
func TestMCPOAuth(t *testing.T) {
	ts, cleanup := bootTestServer(t)
	defer cleanup()
	// The issuer and resource come from APP_URL (a .env may set the web's); here the API is the origin.
	t.Setenv("APP_URL", ts.URL)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	short := suffix[len(suffix)-6:]
	owner := newClient(t, ts.URL)
	ownerEmail := "mcp-oauth-" + suffix + "@example.test"
	owner.register(ownerEmail)
	// The empty team comes first (like a team made at sign-up), the live one second.
	var empty, live struct{ ID, Slug string }
	owner.mustJSON("POST", "/api/teams", map[string]any{"name": "Empty " + short}, 201, &empty)
	owner.mustJSON("POST", "/api/teams", map[string]any{"name": "Live " + short}, 201, &live)
	enrolServer(t, owner, ts.URL, live.ID, "mcp-oauth-"+short)

	getJSON := func(path string, want int, out any) http.Header {
		t.Helper()
		res, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != want {
			b, _ := io.ReadAll(res.Body)
			t.Fatalf("GET %s -> %d %s, want %d", path, res.StatusCode, b, want)
		}
		if out != nil {
			if err := json.NewDecoder(res.Body).Decode(out); err != nil {
				t.Fatalf("GET %s: %v", path, err)
			}
		}
		return res.Header
	}

	// 1. Discovery: the 401 names the protected-resource metadata, which names the server.
	res, err := http.Post(ts.URL+"/api/mcp", "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	challenge := res.Header.Get("WWW-Authenticate")
	wantMeta := ts.URL + "/.well-known/oauth-protected-resource/api/mcp"
	if res.StatusCode != 401 || !strings.Contains(challenge, `resource_metadata="`+wantMeta+`"`) || strings.Contains(challenge, "invalid_token") {
		t.Fatalf("401 challenge: %d %q", res.StatusCode, challenge)
	}
	if st, h := mcpRaw(t, ts.URL, "hbu_not-a-token"); st != 401 || !strings.Contains(h.Get("WWW-Authenticate"), `error="invalid_token"`) {
		t.Errorf("a bad token -> %d %q, want 401 invalid_token", st, h.Get("WWW-Authenticate"))
	}
	var prm struct {
		Resource             string   `json:"resource"`
		AuthorizationServers []string `json:"authorization_servers"`
		Scopes               []string `json:"scopes_supported"`
	}
	for _, p := range []string{"/.well-known/oauth-protected-resource/api/mcp", "/.well-known/oauth-protected-resource"} {
		h := getJSON(p, 200, &prm)
		if prm.Resource != ts.URL+"/api/mcp" || len(prm.AuthorizationServers) != 1 || prm.AuthorizationServers[0] != ts.URL || len(prm.Scopes) != 5 {
			t.Errorf("%s: %+v", p, prm)
		}
		if h.Get("Access-Control-Allow-Origin") != "*" {
			t.Errorf("%s has no CORS header", p)
		}
	}
	var asm map[string]any
	for _, p := range []string{"/.well-known/oauth-authorization-server", "/.well-known/oauth-authorization-server/api/mcp"} {
		getJSON(p, 200, &asm)
		if asm["issuer"] != ts.URL || asm["authorization_endpoint"] != ts.URL+"/oauth/authorize" ||
			asm["token_endpoint"] != ts.URL+"/oauth/token" || asm["registration_endpoint"] != ts.URL+"/oauth/register" ||
			asm["revocation_endpoint"] != ts.URL+"/oauth/revoke" || fmt.Sprint(asm["code_challenge_methods_supported"]) != "[S256]" ||
			fmt.Sprint(asm["grant_types_supported"]) != "[authorization_code refresh_token]" {
			t.Errorf("%s: %v", p, asm)
		}
	}

	// 2. Dynamic client registration.
	register := func(body map[string]any, want int) map[string]any {
		t.Helper()
		b, _ := json.Marshal(body)
		res, err := http.Post(ts.URL+"/oauth/register", "application/json", bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(res.Body).Decode(&out)
		if res.StatusCode != want {
			t.Fatalf("register %v -> %d %v, want %d", body, res.StatusCode, out, want)
		}
		return out
	}
	redirect := "https://claude.ai/api/mcp/auth_callback"
	reg := register(map[string]any{"client_name": "Claude", "redirect_uris": []string{redirect},
		"grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"}, "token_endpoint_auth_method": "none"}, 201)
	clientID := fmt.Sprint(reg["client_id"])
	if !strings.HasPrefix(clientID, "hbc_") || reg["client_secret"] != nil || reg["token_endpoint_auth_method"] != "none" {
		t.Fatalf("registration: %v", reg)
	}
	register(map[string]any{"redirect_uris": []string{"http://evil.example.test/cb"}}, 400)
	register(map[string]any{"redirect_uris": []string{"javascript:alert(1)"}}, 400)
	register(map[string]any{"redirect_uris": []string{}}, 400)
	register(map[string]any{"redirect_uris": []string{redirect}, "grant_types": []string{"client_credentials"}}, 400)
	native := register(map[string]any{"client_name": "Claude Code", "redirect_uris": []string{"http://localhost:33418/callback"}}, 201)
	confidential := register(map[string]any{"client_name": "Server app", "redirect_uris": []string{redirect}, "token_endpoint_auth_method": "client_secret_post"}, 201)
	if !strings.HasPrefix(fmt.Sprint(confidential["client_secret"]), tokens.PrefixOAuthSecret) {
		t.Errorf("a confidential client gets no secret: %v", confidential)
	}

	// 3. The consent page: every team, the live one pre-selected; never one created.
	consentPath := func(cid, redir string) string {
		return "/api/oauth/authorize?" + url.Values{"client_id": {cid}, "redirect_uri": {redir}, "scope": {"read deploy"},
			"resource": {ts.URL + "/api/mcp"}}.Encode()
	}
	type team struct {
		ID         string `json:"id"`
		Slug       string `json:"slug"`
		Servers    int64  `json:"servers"`
		CanConnect bool   `json:"can_connect"`
		Default    bool   `json:"default"`
	}
	var consent struct {
		Kind          string
		Client        struct{ Name string }
		Teams         []team
		DefaultScopes []string `json:"default_scopes"`
	}
	owner.mustJSON("GET", consentPath(clientID, redirect), nil, 200, &consent)
	if consent.Kind != "mcp" || consent.Client.Name != "Claude" || len(consent.Teams) != 2 || strings.Join(consent.DefaultScopes, ",") != "deploy,read" {
		t.Fatalf("consent: %+v", consent)
	}
	for _, tm := range consent.Teams {
		if tm.Default != (tm.ID == live.ID) || !tm.CanConnect {
			t.Errorf("team %s: %+v (the live team %s should be the default)", tm.Slug, tm, live.ID)
		}
	}
	if code, _ := newClient(t, ts.URL).do("GET", consentPath(clientID, redirect), nil); code != 401 {
		t.Errorf("consent signed out -> %d, want 401", code)
	}
	if code, _ := owner.do("GET", consentPath(clientID, "https://evil.example.test/cb"), nil); code != 400 {
		t.Errorf("unregistered redirect -> %d, want 400", code)
	}
	if code, _ := owner.do("GET", "/api/oauth/authorize?"+url.Values{"client_id": {clientID}, "redirect_uri": {redirect},
		"resource": {"https://elsewhere.example.test/mcp"}}.Encode(), nil); code != 400 {
		t.Errorf("another resource -> %d, want 400", code)
	}
	// A native client's loopback redirect may come back on another port (RFC 8252).
	owner.mustJSON("GET", consentPath(fmt.Sprint(native["client_id"]), "http://localhost:51234/callback"), nil, 200, nil)

	var teamsBefore struct{ Teams []any }
	owner.mustJSON("GET", "/api/me", nil, 200, &teamsBefore)

	verifier := strings.Repeat("a", 30) + "-" + suffix + strings.Repeat("z", 10)
	authorize := func(c *client, cid, redir string, over map[string]any, want int) url.Values {
		t.Helper()
		body := map[string]any{"response_type": "code", "client_id": cid, "redirect_uri": redir, "state": "s-" + short,
			"code_challenge": pkceChallenge(verifier), "code_challenge_method": "S256", "resource": ts.URL + "/api/mcp",
			"team": live.ID, "scopes": []string{"read"}, "allow": true}
		for k, v := range over {
			body[k] = v
		}
		var out struct{ Redirect string }
		c.mustJSON("POST", "/api/oauth/authorize", body, want, &out)
		if want != 200 {
			return nil
		}
		u, err := url.Parse(out.Redirect)
		if err != nil || u.Query().Get("state") != "s-"+short {
			t.Fatalf("redirect %q", out.Redirect)
		}
		return u.Query()
	}
	if q := authorize(owner, clientID, redirect, map[string]any{"allow": false}, 200); q.Get("error") != "access_denied" {
		t.Errorf("deny: %v", q)
	}
	if q := authorize(owner, clientID, redirect, map[string]any{"code_challenge": ""}, 200); q.Get("error") != "invalid_request" {
		t.Errorf("no PKCE: %v", q)
	}
	if q := authorize(owner, clientID, redirect, map[string]any{"resource": "https://elsewhere.example.test/mcp"}, 200); q.Get("error") != "invalid_target" {
		t.Errorf("another resource: %v", q)
	}
	authorize(owner, clientID, redirect, map[string]any{"scopes": []string{}}, 422)
	authorize(owner, clientID, redirect, map[string]any{"scopes": []string{"root"}}, 422)
	authorize(owner, clientID, redirect, map[string]any{"team": ""}, 422)
	stranger := newClient(t, ts.URL)
	stranger.register("mcp-oauth-stranger-" + suffix + "@example.test")
	authorize(stranger, clientID, redirect, nil, 404) // not their team
	var strangerConsent struct{ Teams []team }
	stranger.mustJSON("GET", consentPath(clientID, redirect), nil, 200, &strangerConsent)
	if len(strangerConsent.Teams) != 0 {
		t.Errorf("a person with no team is offered %v", strangerConsent.Teams)
	}

	// 4. The token endpoint: a public client, no secret.
	token := func(form url.Values) (int, map[string]any) {
		t.Helper()
		res, err := http.PostForm(ts.URL+"/oauth/token", form)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}
	codeForm := func(code string) url.Values {
		return url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect},
			"code_verifier": {verifier}, "client_id": {clientID}, "resource": {ts.URL + "/api/mcp"}}
	}
	code := authorize(owner, clientID, redirect, nil, 200).Get("code")
	f := codeForm(code)
	f.Set("code_verifier", strings.Repeat("b", 50))
	if st, out := token(f); st != 400 || out["error"] != "invalid_grant" {
		t.Errorf("wrong verifier -> %d %v", st, out)
	}
	if st, out := token(codeForm(code)); st != 400 || out["error"] != "invalid_grant" {
		t.Errorf("a code after a failed exchange -> %d %v", st, out)
	}
	code = authorize(owner, clientID, redirect, nil, 200).Get("code")
	f = codeForm(code)
	f.Set("client_id", fmt.Sprint(confidential["client_id"]))
	if st, out := token(f); st != 401 || out["error"] != "invalid_client" {
		t.Errorf("a confidential client without its secret -> %d %v", st, out)
	}
	f = codeForm(code)
	f.Set("resource", "https://elsewhere.example.test/mcp")
	if st, out := token(f); st != 400 || out["error"] != "invalid_target" {
		t.Errorf("another resource at the token endpoint -> %d %v", st, out)
	}
	st, tok := token(codeForm(code))
	access, refresh := fmt.Sprint(tok["access_token"]), fmt.Sprint(tok["refresh_token"])
	if st != 200 || tok["token_type"] != "Bearer" || tok["scope"] != "read" || tok["expires_in"] != float64(3600) ||
		!strings.HasPrefix(access, tokens.PrefixUserMCP) || !strings.HasPrefix(refresh, "hbf_") {
		t.Fatalf("exchange -> %d %v", st, tok)
	}
	if st, out := token(codeForm(code)); st != 400 || out["error"] != "invalid_grant" {
		t.Errorf("replay -> %d %v", st, out)
	}

	// 5. The token is bound to the live team, with the granted scope only.
	if res := mcpCall(t, ts.URL, "/api/mcp", access, "whoami", nil); res.IsError || !strings.Contains(res.Text, live.ID) {
		t.Errorf("whoami: %+v", res)
	}
	lt := mcpCall(t, ts.URL, "/api/mcp", access, "list_teams", nil)
	if lt.IsError || !strings.Contains(lt.Text, "* Live "+short) || !strings.Contains(lt.Text, "  Empty "+short) {
		t.Errorf("list_teams: %+v", lt)
	}
	if res := mcpCall(t, ts.URL, "/api/mcp", access, "invite_member", map[string]any{"email": "x-" + suffix + "@example.test", "role": "viewer"}); !res.IsError {
		t.Errorf("a read-only connector could invite: %+v", res)
	}

	// No team was created along the way.
	var teamsAfter struct{ Teams []any }
	owner.mustJSON("GET", "/api/me", nil, 200, &teamsAfter)
	if len(teamsAfter.Teams) != len(teamsBefore.Teams) {
		t.Errorf("OAuth changed the person's teams: %d -> %d", len(teamsBefore.Teams), len(teamsAfter.Teams))
	}

	// 6. The account lists the connection with its team; the team's page flags it.
	var mine struct {
		Tokens []struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			TeamID    string `json:"team_id"`
			Connector bool   `json:"connector"`
		}
	}
	owner.mustJSON("GET", "/api/me/mcp-tokens", nil, 200, &mine)
	if len(mine.Tokens) != 1 || mine.Tokens[0].TeamID != live.ID || !mine.Tokens[0].Connector || mine.Tokens[0].Name != "Claude" {
		t.Fatalf("my tokens: %+v", mine)
	}

	// 7. Refresh rotates both tokens; the old ones stop working.
	refreshForm := func(rt string) url.Values {
		return url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt}, "client_id": {clientID}}
	}
	f = refreshForm(refresh)
	f.Set("scope", "read write")
	if st, out := token(f); st != 400 || out["error"] != "invalid_scope" {
		t.Errorf("refresh widening the scope -> %d %v", st, out)
	}
	f = refreshForm(refresh)
	f.Set("client_id", fmt.Sprint(native["client_id"]))
	if st, out := token(f); st != 400 || out["error"] != "invalid_grant" {
		t.Errorf("another client's refresh -> %d %v", st, out)
	}
	st, tok2 := token(refreshForm(refresh))
	access2, refresh2 := fmt.Sprint(tok2["access_token"]), fmt.Sprint(tok2["refresh_token"])
	if st != 200 || access2 == access || refresh2 == refresh || tok2["scope"] != "read" {
		t.Fatalf("refresh -> %d %v", st, tok2)
	}
	if st, _ := mcpRaw(t, ts.URL, access); st != 401 {
		t.Errorf("the rotated-out access token -> %d, want 401", st)
	}
	if st, out := token(refreshForm(refresh)); st != 400 || out["error"] != "invalid_grant" {
		t.Errorf("the rotated-out refresh token -> %d %v", st, out)
	}
	if res := mcpCall(t, ts.URL, "/api/mcp", access2, "whoami", nil); res.IsError {
		t.Errorf("the refreshed token: %+v", res)
	}

	// 8. Revocation (RFC 7009): 200 for anything; the connection ends.
	revoke := func(form url.Values) int {
		t.Helper()
		res, err := http.PostForm(ts.URL+"/oauth/revoke", form)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if st := revoke(url.Values{"token": {"hbf_nothing"}, "client_id": {clientID}}); st != 200 {
		t.Errorf("revoking an unknown token -> %d", st)
	}
	if st := revoke(url.Values{"token": {refresh2}, "client_id": {fmt.Sprint(native["client_id"])}}); st != 200 {
		t.Errorf("another client's revoke -> %d", st)
	}
	if res := mcpCall(t, ts.URL, "/api/mcp", access2, "whoami", nil); res.IsError {
		t.Errorf("another client revoked the connection: %+v", res)
	}
	if st := revoke(url.Values{"token": {refresh2}, "client_id": {clientID}}); st != 200 {
		t.Errorf("revoke -> %d", st)
	}
	if st, _ := mcpRaw(t, ts.URL, access2); st != 401 {
		t.Errorf("access after revoke -> %d, want 401", st)
	}
	if st, out := token(refreshForm(refresh2)); st != 400 || out["error"] != "invalid_grant" {
		t.Errorf("refresh after revoke -> %d %v", st, out)
	}
	owner.mustJSON("GET", "/api/me/mcp-tokens", nil, 200, &mine)
	if len(mine.Tokens) != 0 {
		t.Errorf("a revoked connection is still listed: %+v", mine)
	}

	// 9. A connection revoked on the tokens page cannot be refreshed back to life.
	code = authorize(owner, clientID, redirect, map[string]any{"team": empty.Slug}, 200).Get("code")
	st, tok3 := token(codeForm(code))
	if st != 200 {
		t.Fatalf("exchange for the other team -> %d %v", st, tok3)
	}
	owner.mustJSON("GET", "/api/me/mcp-tokens", nil, 200, &mine)
	if len(mine.Tokens) != 1 || mine.Tokens[0].TeamID != empty.ID {
		t.Fatalf("my tokens: %+v", mine)
	}
	owner.mustJSON("DELETE", "/api/teams/"+empty.ID+"/mcp-tokens/"+mine.Tokens[0].ID, nil, 204, nil)
	if st, out := token(refreshForm(fmt.Sprint(tok3["refresh_token"]))); st != 400 || out["error"] != "invalid_grant" {
		t.Errorf("refresh of a token revoked in the UI -> %d %v", st, out)
	}
}

// mcpRaw posts an initialize with a bearer and returns the status and headers.
func mcpRaw(t *testing.T, base, bearer string) (int, http.Header) {
	t.Helper()
	req, _ := http.NewRequest("POST", base+"/api/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearer)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode, res.Header
}
