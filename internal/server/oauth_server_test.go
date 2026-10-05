package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
	"github.com/fadymondy/hosbah/internal/tokens"
)

// TestOAuthAuthorizationServer covers the OAuth authorization server (MH-1153,
// internal/api/oauth.go) end to end: the consent data lists only manageable apps, an unknown
// redirect URI is refused (never redirected to), deny and missing PKCE come back as redirect
// errors, a code exchanges once for an hba_ app token, and a wrong secret, verifier or
// redirect URI fails with the RFC 6749 shape and burns the code.
func TestOAuthAuthorizationServer(t *testing.T) {
	ts, cleanup := bootTestServer(t)
	defer cleanup()
	sqlDB := testDB(t)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	short := suffix[len(suffix)-6:]
	owner := newClient(t, ts.URL)
	owner.register("oauth-owner-" + suffix + "@example.test")
	var team struct{ ID, Slug string }
	owner.mustJSON("POST", "/api/teams", map[string]any{"name": "OAuth " + suffix}, 201, &team)
	srv, _ := enrolServer(t, owner, ts.URL, team.ID, "oauth-"+short)
	var app struct {
		App struct{ ID, Name string } `json:"app"`
	}
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/apps", map[string]any{"name": "oauth-a-" + short, "server": srv,
		"repo_url": "https://github.com/octocat/api.git"}, 201, &app)

	// A registered client, as `go run ./cmd/admin oauth-client add` makes it.
	redirect := "https://mahaam.example.test/hosbah/callback"
	clientID, _ := tokens.New("hbc_")
	secret, secretHash := tokens.New(tokens.PrefixOAuthSecret)
	if err := tenancy.Tx(context.Background(), sqlDB, tenancy.Scope{PlatformAdmin: true}, func(q *db.Queries, _ *sql.Tx) error {
		_, err := q.InsertOAuthClient(context.Background(), db.InsertOAuthClientParams{ClientID: clientID, Name: "Mahaam " + short,
			SecretHash: secretHash, RedirectUris: "https://other.example.test/cb\n" + redirect})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	consentPath := "/api/oauth/authorize?" + url.Values{"client_id": {clientID}, "redirect_uri": {redirect}}.Encode()
	if code, _ := newClient(t, ts.URL).do("GET", consentPath, nil); code != 401 {
		t.Errorf("consent data signed out -> %d, want 401", code)
	}
	var consent struct {
		Client struct{ Name string } `json:"client"`
		Apps   []struct {
			ID       string `json:"id"`
			TeamSlug string `json:"team_slug"`
		} `json:"apps"`
	}
	owner.mustJSON("GET", consentPath, nil, 200, &consent)
	if consent.Client.Name != "Mahaam "+short || len(consent.Apps) != 1 || consent.Apps[0].ID != app.App.ID || consent.Apps[0].TeamSlug != team.Slug {
		t.Fatalf("consent data: %+v", consent)
	}
	bad := "/api/oauth/authorize?" + url.Values{"client_id": {clientID}, "redirect_uri": {"https://evil.example.test/cb"}}.Encode()
	if code, _ := owner.do("GET", bad, nil); code != 400 {
		t.Errorf("unregistered redirect URI -> %d, want 400", code)
	}
	if code, _ := owner.do("GET", "/api/oauth/authorize?client_id=hbc_nope&redirect_uri="+url.QueryEscape(redirect), nil); code != 400 {
		t.Errorf("unknown client -> %d, want 400", code)
	}

	verifier := strings.Repeat("v", 20) + "-" + suffix + strings.Repeat("x", 20)
	challenge := pkceChallenge(verifier)
	authorize := func(c *client, over map[string]any, want int) url.Values {
		t.Helper()
		body := map[string]any{"response_type": "code", "client_id": clientID, "redirect_uri": redirect, "state": "st-" + short,
			"code_challenge": challenge, "code_challenge_method": "S256", "scope": "app", "team": team.ID, "app": app.App.ID, "allow": true}
		for k, v := range over {
			body[k] = v
		}
		var out struct{ Redirect string }
		c.mustJSON("POST", "/api/oauth/authorize", body, want, &out)
		if want != 200 {
			return nil
		}
		u, err := url.Parse(out.Redirect)
		if err != nil || !strings.HasPrefix(out.Redirect, redirect+"?") {
			t.Fatalf("redirect %q is not the registered URI", out.Redirect)
		}
		if u.Query().Get("state") != "st-"+short {
			t.Fatalf("redirect %q lost the state", out.Redirect)
		}
		return u.Query()
	}
	if q := authorize(owner, map[string]any{"allow": false}, 200); q.Get("error") != "access_denied" || q.Get("code") != "" {
		t.Errorf("deny: %v", q)
	}
	if q := authorize(owner, map[string]any{"code_challenge_method": "plain"}, 200); q.Get("error") != "invalid_request" {
		t.Errorf("plain PKCE: %v", q)
	}
	if q := authorize(owner, map[string]any{"scope": "admin"}, 200); q.Get("error") != "invalid_scope" {
		t.Errorf("unknown scope: %v", q)
	}
	authorize(owner, map[string]any{"redirect_uri": "https://evil.example.test/cb"}, 400)

	// Someone outside the team: no apps offered, and naming the app anyway is refused.
	stranger := newClient(t, ts.URL)
	stranger.register("oauth-stranger-" + suffix + "@example.test")
	var none struct{ Apps []any }
	stranger.mustJSON("GET", consentPath, nil, 200, &none)
	if len(none.Apps) != 0 {
		t.Errorf("a stranger is offered %d apps", len(none.Apps))
	}
	authorize(stranger, nil, 404)

	exchange := func(form url.Values) (int, map[string]any) {
		t.Helper()
		res, err := http.PostForm(ts.URL+"/oauth/token", form)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		var out map[string]any
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatalf("token response is not JSON: %d %s", res.StatusCode, b)
		}
		if res.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("token response without Cache-Control: no-store")
		}
		return res.StatusCode, out
	}
	form := func(code string) url.Values {
		return url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect},
			"code_verifier": {verifier}, "client_id": {clientID}, "client_secret": {secret}}
	}
	expect := func(what string, status int, out map[string]any, wantStatus int, wantErr string) {
		t.Helper()
		if status != wantStatus || out["error"] != wantErr || (wantErr != "" && out["error_description"] == "") {
			t.Errorf("%s -> %d %v, want %d %s", what, status, out, wantStatus, wantErr)
		}
	}

	// A wrong secret is invalid_client and leaves the code usable; a wrong verifier burns it.
	code1 := authorize(owner, nil, 200).Get("code")
	if !strings.HasPrefix(code1, tokens.PrefixOAuthCode) {
		t.Fatalf("code %q", code1)
	}
	f := form(code1)
	f.Set("client_secret", "hbs_wrong")
	st, out := exchange(f)
	expect("wrong secret", st, out, 401, "invalid_client")
	f = form(code1)
	f.Set("grant_type", "password")
	st, out = exchange(f)
	expect("password grant", st, out, 400, "unsupported_grant_type")
	f = form(code1)
	f.Set("code_verifier", strings.Repeat("w", 60))
	st, out = exchange(f)
	expect("wrong verifier", st, out, 400, "invalid_grant")
	st, out = exchange(form(code1))
	expect("a code after a failed exchange", st, out, 400, "invalid_grant")

	// A wrong redirect URI (even another registered one) fails too.
	code2 := authorize(owner, nil, 200).Get("code")
	f = form(code2)
	f.Set("redirect_uri", "https://other.example.test/cb")
	st, out = exchange(f)
	expect("other redirect URI", st, out, 400, "invalid_grant")

	// The happy path: an hba_ token for exactly the chosen app, once.
	code3 := authorize(owner, nil, 200).Get("code")
	st, out = exchange(form(code3))
	if st != 200 || out["token_type"] != "Bearer" || !strings.HasPrefix(fmt.Sprint(out["access_token"]), tokens.PrefixApp) {
		t.Fatalf("exchange -> %d %v", st, out)
	}
	got, _ := out["app"].(map[string]any)
	if got["id"] != app.App.ID || got["server_id"] != srv || got["panel_url"] == "" {
		t.Errorf("exchange app: %v", got)
	}
	access := fmt.Sprint(out["access_token"])
	var me struct {
		App struct{ ID string } `json:"app"`
	}
	keyJSON(t, ts.URL, access, "GET", "/api/integrations/app", nil, 200, &me)
	if me.App.ID != app.App.ID {
		t.Errorf("the token reaches app %q, want %q", me.App.ID, app.App.ID)
	}
	st, out = exchange(form(code3))
	expect("replay", st, out, 400, "invalid_grant")

	// The client in HTTP Basic works the same.
	code4 := authorize(owner, nil, 200).Get("code")
	f = form(code4)
	f.Del("client_id")
	f.Del("client_secret")
	req, _ := http.NewRequest("POST", ts.URL+"/oauth/token", strings.NewReader(f.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(url.QueryEscape(clientID), url.QueryEscape(secret))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Errorf("basic auth exchange -> %d", res.StatusCode)
	}
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
