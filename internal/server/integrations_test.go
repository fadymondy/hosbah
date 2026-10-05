package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"time"
)

// TestIntegrationAPI covers the integration API for outside systems (MH-1153,
// internal/api/integrations.go) end to end over real HTTP:
//
//  1. a user token (hbu_) lists only its team's apps, and cannot mint for an app its user cannot
//     manage (another team's: 404; a deployer's: 403);
//  2. an app token (hba_) for app A cannot reach app B, nor any non-integration route;
//  3. after DELETE /api/integrations/app/token every call with it is 401;
//  4. a panel link signs in once; a used or expired link is refused;
//  5. a panel session reaches its own app and nothing else (another app, a server, settings);
//  6. a panel link for a full user's email gives that user's rights to no one.
func TestIntegrationAPI(t *testing.T) {
	ts, cleanup := bootTestServer(t)
	defer cleanup()
	sqlDB := testDB(t)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	short := suffix[len(suffix)-6:]
	ownerEmail := "integ-owner-" + suffix + "@example.test"
	owner := newClient(t, ts.URL)
	owner.register(ownerEmail)
	var team struct{ ID, Slug string }
	owner.mustJSON("POST", "/api/teams", map[string]any{"name": "Integ " + suffix}, 201, &team)
	srv, _ := enrolServer(t, owner, ts.URL, team.ID, "integ-"+short)

	type appResp struct {
		App struct{ ID, Name string } `json:"app"`
	}
	var appA, appB appResp
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/apps", map[string]any{"name": "integ-a-" + short, "server": srv,
		"repo_url": "https://github.com/octocat/api.git"}, 201, &appA)
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/apps", map[string]any{"name": "integ-b-" + short, "server": srv,
		"repo_url": "https://github.com/octocat/api.git"}, 201, &appB)

	// Another team with an app of its own.
	stranger := newClient(t, ts.URL)
	stranger.register("integ-stranger-" + suffix + "@example.test")
	var teamX struct{ ID string }
	stranger.mustJSON("POST", "/api/teams", map[string]any{"name": "Integ X " + suffix}, 201, &teamX)
	srvX, _ := enrolServer(t, stranger, ts.URL, teamX.ID, "integ-x-"+short)
	var appX appResp
	stranger.mustJSON("POST", "/api/teams/"+teamX.ID+"/apps", map[string]any{"name": "integ-x-" + short, "server": srvX,
		"repo_url": "https://github.com/octocat/api.git"}, 201, &appX)

	var hbu struct{ ID, Token string }
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/mcp-tokens", map[string]any{"name": "mahaam", "scopes": []string{"read", "write"}, "expires_in_days": 30}, 201, &hbu)

	// ---- 1. hbu lists only its team's apps; minting refused where the user cannot manage --------
	var list struct {
		Apps []struct {
			ID, Name, Title, Status string
			ServerID                string   `json:"server_id"`
			ServerName              string   `json:"server_name"`
			Domains                 []string `json:"domains"`
			PanelURL                string   `json:"panel_url"`
		} `json:"apps"`
	}
	keyJSON(t, ts.URL, hbu.Token, "GET", "/api/integrations/apps", nil, 200, &list)
	seen := map[string]bool{}
	for _, a := range list.Apps {
		seen[a.ID] = true
		if a.Domains == nil {
			t.Errorf("app %s: domains is null, want []", a.Name)
		}
		if a.ServerID != srv || a.ServerName == "" || !strings.HasSuffix(a.PanelURL, "/en/panel/"+srv+"/apps/"+a.ID) {
			t.Errorf("app %s: server %q/%q panel_url %q", a.Name, a.ServerID, a.ServerName, a.PanelURL)
		}
	}
	if !seen[appA.App.ID] || !seen[appB.App.ID] || seen[appX.App.ID] || len(list.Apps) != 2 {
		t.Fatalf("hbu lists %+v, want exactly apps A and B", list.Apps)
	}
	if code, _ := keyDo(t, ts.URL, hbu.Token, "POST", "/api/integrations/apps/"+appX.App.ID+"/tokens", map[string]any{"label": "x"}); code != 404 {
		t.Errorf("minting for another team's app -> %d, want 404", code)
	}
	if code, _ := keyDo(t, ts.URL, hbu.Token, "POST", "/api/integrations/apps/no-such-app/tokens", map[string]any{"label": "x"}); code != 404 {
		t.Errorf("minting for an unknown app -> %d, want 404", code)
	}
	if code, _ := keyDo(t, ts.URL, "", "GET", "/api/integrations/apps", nil); code != 401 {
		t.Errorf("no bearer -> %d, want 401", code)
	}
	// A deployer's user token cannot mint (no manage permission).
	deployerEmail := "integ-deployer-" + suffix + "@example.test"
	deployer := newClient(t, ts.URL)
	deployer.register(deployerEmail)
	var inv struct {
		AcceptURL string `json:"accept_url"`
	}
	// Only admins mint user tokens, so the member mints as an admin and is then made a deployer:
	// the token acts with its user's current role.
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/invitations", map[string]any{"email": deployerEmail, "role": "admin"}, 201, &inv)
	deployer.mustJSON("POST", "/api/invitations/"+inv.AcceptURL[strings.LastIndex(inv.AcceptURL, "/")+1:]+"/accept", nil, 200, nil)
	var hbuDeployer struct{ Token string }
	deployer.mustJSON("POST", "/api/teams/"+team.ID+"/mcp-tokens", map[string]any{"name": "d", "scopes": []string{"read", "write"}, "expires_in_days": 30}, 201, &hbuDeployer)
	var members struct {
		Members []struct{ ID, Email string } `json:"members"`
	}
	owner.mustJSON("GET", "/api/teams/"+team.ID+"/members", nil, 200, &members)
	for _, mm := range members.Members {
		if mm.Email == deployerEmail {
			owner.mustJSON("PATCH", "/api/teams/"+team.ID+"/members/"+mm.ID, map[string]any{"role": "deployer"}, 200, nil)
		}
	}
	if code, text := keyDo(t, ts.URL, hbuDeployer.Token, "POST", "/api/integrations/apps/"+appA.App.ID+"/tokens", map[string]any{"label": "x"}); code != 403 {
		t.Errorf("deployer minting -> %d, want 403: %s", code, text)
	}

	// Mint app tokens for A (an Arabic label) and B.
	type minted struct {
		Token string `json:"token"`
		App   struct{ ID string }
	}
	var tokA, tokB minted
	keyJSON(t, ts.URL, hbu.Token, "POST", "/api/integrations/apps/"+appA.App.Name+"/tokens", map[string]any{"label": "مهام · حوسبة"}, 201, &tokA)
	keyJSON(t, ts.URL, hbu.Token, "POST", "/api/integrations/apps/"+appB.App.ID+"/tokens", map[string]any{"label": "Mahaam B"}, 201, &tokB)
	if !strings.HasPrefix(tokA.Token, "hba_") || tokA.App.ID != appA.App.ID {
		t.Fatalf("minted %+v", tokA)
	}
	// An hba_ token is not a user token, and an hbu_ token is not an app token.
	if code, _ := keyDo(t, ts.URL, tokA.Token, "GET", "/api/integrations/apps", nil); code != 401 {
		t.Errorf("hba on the hbu list -> %d, want 401", code)
	}
	if code, _ := keyDo(t, ts.URL, hbu.Token, "GET", "/api/integrations/app", nil); code != 401 {
		t.Errorf("hbu on the hba app read -> %d, want 401", code)
	}
	// The app settings page lists them (owner session), with the Arabic label intact.
	var tokens struct {
		Tokens []struct{ ID, Label string } `json:"tokens"`
	}
	owner.mustJSON("GET", "/api/teams/"+team.ID+"/apps/"+appA.App.ID+"/tokens", nil, 200, &tokens)
	if len(tokens.Tokens) != 1 || tokens.Tokens[0].Label != "مهام · حوسبة" {
		t.Fatalf("app A tokens %+v", tokens.Tokens)
	}
	if code, _ := deployer.do("GET", "/api/teams/"+team.ID+"/apps/"+appA.App.ID+"/tokens", nil); code != 403 {
		t.Errorf("deployer listing app tokens -> %d, want 403", code)
	}

	// ---- 2. hba for A sees A only, and no route outside the integration API ---------------------
	var got struct {
		App struct {
			ID      string
			Domains []string `json:"domains"`
		} `json:"app"`
		Deployments []any `json:"deployments"`
	}
	keyJSON(t, ts.URL, tokA.Token, "GET", "/api/integrations/app", nil, 200, &got)
	if got.App.ID != appA.App.ID || got.Deployments == nil || got.App.Domains == nil {
		t.Fatalf("hba A reads %+v", got)
	}
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/teams/" + team.ID + "/apps/" + appB.App.ID},
		{"GET", "/api/teams/" + team.ID + "/apps/" + appA.App.ID},
		{"POST", "/api/teams/" + team.ID + "/apps/" + appB.App.ID + "/deploy"},
		{"GET", "/api/me"},
		{"GET", "/api/teams"},
		{"POST", "/api/integrations/apps/" + appB.App.ID + "/tokens"},
		{"GET", "/api/integrations/apps"},
	} {
		if code, _ := keyDo(t, ts.URL, tokA.Token, c.method, c.path, map[string]any{"label": "x"}); code != 401 && code != 404 {
			t.Errorf("hba A on %s %s -> %d, want 401/404", c.method, c.path, code)
		}
	}
	// hba_ is also the admin MCP prefix: an app token must never authenticate there.
	if code := mcpStatus(ts.URL, "/api/admin/mcp", tokA.Token); code != 401 {
		t.Errorf("hba app token on the admin MCP -> %d, want 401", code)
	}
	if code := mcpStatus(ts.URL, "/api/mcp", tokA.Token); code != 401 {
		t.Errorf("hba app token on the team MCP -> %d, want 401", code)
	}

	// ---- 4. a panel link works once; reuse and expiry are refused --------------------------------
	issue := func(token, email, locale string) string {
		t.Helper()
		var link struct {
			URL       string    `json:"url"`
			ExpiresAt time.Time `json:"expires_at"`
		}
		keyJSON(t, ts.URL, token, "POST", "/api/integrations/app/panel-sessions", map[string]any{"email": email, "name": "عميل", "locale": locale}, 201, &link)
		if time.Until(link.ExpiresAt) > 6*time.Minute || time.Until(link.ExpiresAt) < 3*time.Minute {
			t.Errorf("panel link expires at %s, want about 5 minutes", link.ExpiresAt)
		}
		i := strings.Index(link.URL, "/api/integrations/panel/")
		if i < 0 {
			t.Fatalf("panel link url %q", link.URL)
		}
		return link.URL[i:]
	}
	customerEmail := "customer-" + suffix + "@example.test"
	path := issue(tokA.Token, customerEmail, "ar")
	customer := newClient(t, ts.URL)
	if loc := follow(t, customer, path); loc != "/ar/panel/"+srv+"/apps/"+appA.App.ID {
		t.Fatalf("panel link -> %q, want the app's panel page", loc)
	}
	again := newClient(t, ts.URL)
	if loc := follow(t, again, path); loc != "/ar/link-expired" {
		t.Errorf("used panel link -> %q, want /ar/link-expired", loc)
	}
	if code, _ := again.do("GET", "/api/me/panel-app", nil); code != 401 {
		t.Errorf("a refused link still signed someone in: /api/me/panel-app -> %d", code)
	}
	expiredPath := issue(tokA.Token, customerEmail, "en")
	tx, err := sqlDB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`SELECT set_config('app.team_id', $1, true)`, team.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE app_panel_links SET expires_at = now() - interval '1 minute' WHERE team_id = $1 AND used_at IS NULL`, team.ID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if loc := follow(t, newClient(t, ts.URL), expiredPath); loc != "/en/link-expired" {
		t.Errorf("expired panel link -> %q, want /en/link-expired", loc)
	}
	if loc := follow(t, newClient(t, ts.URL), "/api/integrations/panel/not-a-real-link"); loc != "/en/link-expired" {
		t.Errorf("unknown panel link -> %q", loc)
	}
	if n := sysCount(t, sqlDB, `SELECT count(*) FROM activity_log WHERE team_id = $1 AND action IN ('app_panel_link.issued','app_panel_link.used')`, team.ID); n < 3 {
		t.Errorf("audit holds %d panel-link events, want issue+use+issue", n)
	}

	// ---- 5. the panel session reaches its app and nothing else -----------------------------------
	var me struct {
		Email    string `json:"email"`
		TeamSlug string `json:"team_slug"`
		AppID    string `json:"app_id"`
		ServerID string `json:"server_id"`
	}
	customer.mustJSON("GET", "/api/me/panel-app", nil, 200, &me)
	if me.AppID != appA.App.ID || me.ServerID != srv || me.TeamSlug != team.Slug || me.Email != customerEmail {
		t.Fatalf("/api/me/panel-app %+v", me)
	}
	for _, p := range []string{
		"/api/teams/" + team.Slug,
		"/api/teams/" + team.ID + "/apps/" + appA.App.ID,
		"/api/teams/" + team.Slug + "/apps/" + appA.App.Name,
		"/api/teams/" + team.ID + "/apps/" + appA.App.ID + "/deployments",
	} {
		if code, text := customer.do("GET", p, nil); code != 200 {
			t.Errorf("panel session on its own %s -> %d: %s", p, code, text)
		}
	}
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/teams/" + team.ID + "/apps/" + appB.App.ID},
		{"GET", "/api/teams/" + team.ID + "/apps/" + appB.App.ID + "/deployments"},
		{"GET", "/api/teams/" + team.ID + "/apps"},
		{"GET", "/api/teams/" + team.ID + "/servers"},
		{"GET", "/api/teams/" + team.ID + "/servers/" + srv},
		{"GET", "/api/teams/" + team.ID + "/members"},
		{"GET", "/api/teams/" + team.ID + "/mcp-tokens"},
		{"GET", "/api/teams/" + team.ID + "/apps/" + appA.App.ID + "/tokens"},
		{"GET", "/api/teams/" + team.ID + "/apps/" + appA.App.ID + "/export-template"},
		{"PATCH", "/api/teams/" + team.ID + "/apps/" + appA.App.ID},
		{"PUT", "/api/teams/" + team.ID + "/apps/" + appA.App.ID + "/env/SECRET"},
		{"POST", "/api/teams/" + team.ID + "/apps/" + appA.App.ID + "/env/SECRET/reveal"},
		{"POST", "/api/teams/" + team.ID + "/apps/" + appA.App.ID + "/domains"},
		{"POST", "/api/teams/" + team.ID + "/apps/" + appA.App.ID + "/deploy-hook"},
		{"DELETE", "/api/teams/" + team.ID + "/apps/" + appA.App.ID},
		{"PATCH", "/api/teams/" + team.ID},
		{"GET", "/api/teams/" + teamX.ID + "/apps/" + appX.App.ID},
		{"GET", "/api/me"},
		{"GET", "/api/teams"},
	} {
		if code, _ := customer.do(c.method, c.path, map[string]any{}); code != 403 && code != 404 {
			t.Errorf("panel session on %s %s -> %d, want 403/404", c.method, c.path, code)
		}
	}

	// ---- 6. a full user's email gives no extra rights --------------------------------------------
	impostor := newClient(t, ts.URL)
	if loc := follow(t, impostor, issue(tokA.Token, ownerEmail, "en")); loc != "/en/panel/"+srv+"/apps/"+appA.App.ID {
		t.Fatalf("panel link for the owner's email -> %q", loc)
	}
	var meOwner struct {
		Email string `json:"email"`
	}
	impostor.mustJSON("GET", "/api/me/panel-app", nil, 200, &meOwner)
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/me"},
		{"GET", "/api/teams/" + team.ID + "/servers/" + srv},
		{"GET", "/api/teams/" + team.ID + "/apps/" + appB.App.ID},
		{"GET", "/api/teams/" + team.ID + "/members"},
		{"POST", "/api/teams/" + team.ID + "/mcp-tokens"},
		{"DELETE", "/api/teams/" + team.ID + "/apps/" + appA.App.ID},
		{"GET", "/api/teams/" + teamX.ID},
	} {
		if code, _ := impostor.do(c.method, c.path, map[string]any{"name": "x", "scopes": []string{"read"}, "confirm": appA.App.Name}); code != 403 && code != 404 {
			t.Errorf("owner-email panel session on %s %s -> %d, want 403/404", c.method, c.path, code)
		}
	}
	// The owner's own account is untouched: still signs in with their password, still the owner.
	fresh := newClient(t, ts.URL)
	fresh.login(ownerEmail)
	fresh.mustJSON("GET", "/api/teams/"+team.ID+"/servers/"+srv, nil, 200, nil)
	if code, _ := fresh.do("GET", "/api/me/panel-app", nil); code != 404 {
		t.Errorf("a full user's own session on /api/me/panel-app -> %d, want 404", code)
	}

	// The integration can deploy its app (audited with the token).
	var dep struct {
		Deployment struct{ ID, Status, Kind string } `json:"deployment"`
	}
	keyJSON(t, ts.URL, tokA.Token, "POST", "/api/integrations/app/deployments", map[string]any{}, 201, &dep)
	if dep.Deployment.ID == "" || dep.Deployment.Kind != "deploy" {
		t.Errorf("deploy via hba: %+v", dep)
	}

	// ---- 3. after DELETE token every hba call is 401 ---------------------------------------------
	if code, text := keyDo(t, ts.URL, tokA.Token, "DELETE", "/api/integrations/app/token", nil); code != 204 {
		t.Fatalf("revoke -> %d: %s", code, text)
	}
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/integrations/app"},
		{"POST", "/api/integrations/app/deployments"},
		{"POST", "/api/integrations/app/panel-sessions"},
		{"DELETE", "/api/integrations/app/token"},
	} {
		body := map[string]any{}
		if strings.HasSuffix(c.path, "/panel-sessions") {
			body = map[string]any{"email": customerEmail}
		}
		if code, _ := keyDo(t, ts.URL, tokA.Token, c.method, c.path, body); code != 401 {
			t.Errorf("revoked hba on %s %s -> %d, want 401", c.method, c.path, code)
		}
	}
	// B's token is untouched; revoking it from the settings page works too.
	keyJSON(t, ts.URL, tokB.Token, "GET", "/api/integrations/app", nil, 200, nil)
	var tokensB struct {
		Tokens []struct{ ID string } `json:"tokens"`
	}
	owner.mustJSON("GET", "/api/teams/"+team.ID+"/apps/"+appB.App.ID+"/tokens", nil, 200, &tokensB)
	owner.mustJSON("DELETE", "/api/teams/"+team.ID+"/apps/"+appB.App.ID+"/tokens/"+tokensB.Tokens[0].ID, nil, 204, nil)
	if code, _ := keyDo(t, ts.URL, tokB.Token, "GET", "/api/integrations/app", nil); code != 401 {
		t.Errorf("hba B after revoking from settings -> %d, want 401", code)
	}
}

// keyJSON is keyDo with a status check and JSON decode.
func keyJSON(t *testing.T, base, token, method, path string, body any, want int, out any) {
	t.Helper()
	code, text := keyDo(t, base, token, method, path, body)
	if code != want {
		t.Fatalf("%s %s -> %d (want %d): %s", method, path, code, want, text)
	}
	if out != nil && text != "" {
		if err := json.Unmarshal([]byte(text), out); err != nil {
			t.Fatalf("%s %s: decode %v: %s", method, path, err, text)
		}
	}
}

// follow opens a panel link with c's cookie jar and answers where it redirects to.
func follow(t *testing.T, c *client, path string) string {
	t.Helper()
	jar := c.http.Jar
	if jar == nil {
		jar, _ = cookiejar.New(nil)
	}
	h := &http.Client{Jar: jar, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := h.Get(c.base + path)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("GET %s -> %d, want 303", path, resp.StatusCode)
	}
	return resp.Header.Get("Location")
}
