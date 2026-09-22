package server

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/fadymondy/hosbah/internal/agentproto"
	"github.com/fadymondy/hosbah/internal/deploy"
)

/*
The GitHub App wired to teams, against a fake GitHub API that checks the App's JWT (RS256 with a
test key): the install round trip and its setup callback (state, confirmation through the API),
the repository listing, the clone token a build receives, and the webhook (signature, installation
events, push → one deploy per delivery, only in the installation's team).
*/

type fakeGitHubAPI struct {
	t        *testing.T
	key      *rsa.PrivateKey
	srv      *httptest.Server
	mu       sync.Mutex
	installs map[int64][2]string // id → account login, type
	asked    []int64             // installations read with the App JWT
	minted   []string            // installation tokens handed out
	listings int                 // /installation/repositories pages served
}

func newFakeGitHubAPI(t *testing.T) *fakeGitHubAPI {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeGitHubAPI{t: t, key: key, installs: map[int64][2]string{}}
	appJWT := func(r *http.Request) bool {
		raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		claims := jwt.MapClaims{}
		_, err := jwt.ParseWithClaims(raw, claims, func(*jwt.Token) (any, error) { return &key.PublicKey, nil },
			jwt.WithValidMethods([]string{"RS256"}), jwt.WithIssuer("77"), jwt.WithExpirationRequired())
		return err == nil
	}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/app/installations/"):
			if !appJWT(r) {
				http.Error(w, "bad jwt", 401)
				return
			}
			id, _ := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/app/installations/"), 10, 64)
			f.asked = append(f.asked, id)
			in, ok := f.installs[id]
			if !ok {
				http.Error(w, `{"message":"Not Found"}`, 404)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "account": map[string]any{"login": in[0], "type": in[1]},
				"repository_selection": "selected", "suspended_at": nil})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/access_tokens"):
			if !appJWT(r) {
				http.Error(w, "bad jwt", 401)
				return
			}
			tok := fmt.Sprintf("ghs_FAKEINSTALLTOKEN%d", len(f.minted)+1)
			f.minted = append(f.minted, tok)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"token": tok, "expires_at": time.Now().Add(time.Hour)})
		case r.Method == http.MethodGet && r.URL.Path == "/installation/repositories":
			tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			known := false
			for _, m := range f.minted {
				known = known || m == tok
			}
			if !known {
				http.Error(w, "bad token", 401)
				return
			}
			f.listings++
			page, _ := strconv.Atoi(r.URL.Query().Get("page"))
			repos := []map[string]any{}
			for i := (page - 1) * 100; i < min(page*100, 130); i++ {
				repos = append(repos, map[string]any{"name": fmt.Sprintf("r%d", i), "full_name": fmt.Sprintf("acme/r%d", i), "private": i%2 == 0,
					"default_branch": "main", "html_url": fmt.Sprintf("https://github.com/acme/r%d", i), "updated_at": time.Now().Add(-time.Duration(i) * time.Minute)})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"total_count": 130, "repositories": repos})
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, 404)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func hmacSign(secret, body []byte) string {
	m := hmac.New(sha256.New, secret)
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func deliver(t *testing.T, base string, secret []byte, event, delivery string, payload any) (int, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", base+"/hooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", event)
	req.Header.Set("X-GitHub-Delivery", delivery)
	if secret != nil {
		req.Header.Set("X-Hub-Signature-256", hmacSign(secret, body))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// sysCount counts rows in system scope (the tables are FORCE RLS).
func sysCount(t *testing.T, sqlDB *sql.DB, query string, args ...any) int {
	t.Helper()
	tx, err := sqlDB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.Exec(`SELECT set_config('app.system', 'on', true), set_config('app.platform_admin', 'on', true)`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := tx.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func TestGitHubAppInstallReposCloneAndPush(t *testing.T) {
	ts, cleanup := bootTestServer(t)
	defer cleanup()
	sqlDB := testDB(t)
	gh := newFakeGitHubAPI(t)
	secret := []byte("webhook-secret")
	lastDeploy.SetGitHub(deploy.NewGitHubApp(77, "hosbah-test", gh.key, secret, gh.srv.URL, gh.srv.Client()))
	const instID = 4242
	gh.installs[instID] = [2]string{"acme", "Organization"}
	gh.installs[5151] = [2]string{"other-org", "Organization"}

	// Installation ids are unique across teams: forget the ones an earlier run may have left.
	func() {
		tx, err := sqlDB.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback() //nolint:errcheck
		_, _ = tx.Exec(`SELECT set_config('app.system', 'on', true)`)
		if _, err := tx.Exec(`DELETE FROM github_installations WHERE installation_id IN (4242, 5151)`); err != nil {
			t.Fatal(err)
		}
		_ = tx.Commit()
	}()

	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	owner := newClient(t, ts.URL)
	owner.register("gha-owner-" + suffix + "@example.test")
	var team struct{ ID, Slug string }
	owner.mustJSON("POST", "/api/teams", map[string]any{"name": "GHA " + suffix}, 201, &team)
	stranger := newClient(t, ts.URL)
	stranger.register("gha-stranger-" + suffix + "@example.test")
	var teamB struct{ ID, Slug string }
	stranger.mustJSON("POST", "/api/teams", map[string]any{"name": "GHB " + suffix}, 201, &teamB)

	// Another team cannot start an install, read the status or list repositories of team A.
	for _, p := range []string{"/github/install", "/github/app", "/github/app/repos"} {
		if status, _ := stranger.do("GET", "/api/teams/"+team.ID+p, nil); status != 404 {
			t.Errorf("stranger GET %s -> %d (want 404)", p, status)
		}
	}

	begin := func(c *client, teamID string) string {
		t.Helper()
		status, loc := location(t, c, "/api/teams/"+teamID+"/github/install?locale=ar")
		if status != http.StatusSeeOther || loc.Host != "github.com" || loc.Path != "/apps/hosbah-test/installations/new" || loc.Query().Get("state") == "" {
			t.Fatalf("install -> %d %s", status, loc)
		}
		return loc.Query().Get("state")
	}
	setup := func(c *client, q url.Values) *url.URL {
		t.Helper()
		status, loc := location(t, c, "/api/github/setup?"+q.Encode())
		if status != http.StatusSeeOther {
			t.Fatalf("setup -> %d", status)
		}
		return loc
	}
	stored := func() int {
		return sysCount(t, sqlDB, `SELECT count(*) FROM github_installations WHERE installation_id = $1`, instID)
	}

	// With "Request user authorization during installation" on, GitHub returns an installation to
	// the sign-in callback: it is handed to the setup handler as is (the user code is dropped), not
	// refused as a sign-in with an unknown state.
	if status, loc := location(t, owner, "/api/auth/oauth/github/callback?code=abc&installation_id=4242&setup_action=install&state=st1"); status != http.StatusSeeOther ||
		loc.Path != "/api/github/setup" || loc.Query().Get("state") != "st1" || loc.Query().Get("installation_id") != "4242" || loc.Query().Get("code") != "" {
		t.Fatalf("installation at the sign-in callback -> %d %s", status, loc)
	}

	// A made-up state is refused, and nothing is stored.
	begin(owner, team.ID)
	loc := setup(owner, url.Values{"installation_id": {"4242"}, "setup_action": {"install"}, "state": {"forged"}})
	if loc.Query().Get("github_error") != "state_invalid" || stored() != 0 {
		t.Fatalf("forged state: %s (stored %d)", loc, stored())
	}
	// The owner's real state, taken to another browser (the stranger's), is refused too — and spent.
	state := begin(owner, team.ID)
	if loc := setup(stranger, url.Values{"installation_id": {"4242"}, "setup_action": {"install"}, "state": {state}}); loc.Query().Get("github_error") != "state_invalid" {
		t.Fatalf("another browser's state: %s", loc)
	}
	if loc := setup(owner, url.Values{"installation_id": {"4242"}, "setup_action": {"install"}, "state": {state}}); loc.Query().Get("github_error") != "state_invalid" {
		t.Fatalf("a spent state: %s", loc)
	}
	// An installation GitHub does not know is refused after asking GitHub.
	state = begin(owner, team.ID)
	if loc := setup(owner, url.Values{"installation_id": {"999"}, "setup_action": {"install"}, "state": {state}}); loc.Query().Get("github_error") != "installation_unknown" {
		t.Fatalf("unknown installation: %s", loc)
	}
	// The real thing: confirmed through the API with the App's JWT, then stored with GitHub's answer.
	gh.mu.Lock()
	gh.asked = nil
	gh.mu.Unlock()
	state = begin(owner, team.ID)
	loc = setup(owner, url.Values{"installation_id": {"4242"}, "setup_action": {"install"}, "state": {state}})
	if loc.Path != "/ar/t/"+team.Slug+"/settings" || loc.Query().Get("github") != "connected" {
		t.Fatalf("setup landed on %s", loc)
	}
	gh.mu.Lock()
	asked := append([]int64(nil), gh.asked...)
	gh.mu.Unlock()
	if len(asked) != 1 || asked[0] != instID || stored() != 1 {
		t.Fatalf("GitHub was asked about %v; stored %d", asked, stored())
	}
	// Reusing that state does nothing.
	if loc := setup(owner, url.Values{"installation_id": {"4242"}, "setup_action": {"install"}, "state": {state}}); loc.Query().Get("github_error") != "state_invalid" {
		t.Fatalf("state reuse: %s", loc)
	}
	// Another team cannot take the installation over, even through its own valid state.
	stateB := begin(stranger, teamB.ID)
	if loc := setup(stranger, url.Values{"installation_id": {"4242"}, "setup_action": {"install"}, "state": {stateB}}); loc.Query().Get("github_error") != "installation_taken" {
		t.Fatalf("takeover: %s", loc)
	}
	// setup_action=update without a state refreshes only an installation of the user's own team.
	if loc := setup(stranger, url.Values{"installation_id": {"4242"}, "setup_action": {"update"}}); loc.Query().Get("github_error") == "" {
		t.Fatalf("a stranger's update of team A's installation: %s", loc)
	}
	if loc := setup(owner, url.Values{"installation_id": {"4242"}, "setup_action": {"update"}}); loc.Query().Get("github") != "connected" {
		t.Fatalf("the owner's update: %s", loc)
	}

	var st struct {
		Configured    bool `json:"configured"`
		CanManage     bool `json:"can_manage"`
		Installations []struct {
			Login     string `json:"account_login"`
			Selection string `json:"repository_selection"`
		} `json:"installations"`
	}
	owner.mustJSON("GET", "/api/teams/"+team.ID+"/github/app", nil, 200, &st)
	if !st.Configured || !st.CanManage || len(st.Installations) != 1 || st.Installations[0].Login != "acme" || st.Installations[0].Selection != "selected" {
		t.Fatalf("status: %+v", st)
	}
	var stB struct {
		Installations []any `json:"installations"`
	}
	stranger.mustJSON("GET", "/api/teams/"+teamB.ID+"/github/app", nil, 200, &stB)
	if len(stB.Installations) != 0 {
		t.Fatalf("team B sees installations: %+v", stB)
	}

	// Repositories: every page, then cached for a minute.
	var repos struct {
		Repos []deploy.GitHubAppRepo `json:"repos"`
	}
	owner.mustJSON("GET", "/api/teams/"+team.ID+"/github/app/repos", nil, 200, &repos)
	if len(repos.Repos) != 130 || repos.Repos[0].NameWithOwner != "acme/r0" {
		t.Fatalf("repos: %d, first %+v", len(repos.Repos), repos.Repos[0])
	}
	gh.mu.Lock()
	pages := gh.listings
	gh.mu.Unlock()
	owner.mustJSON("GET", "/api/teams/"+team.ID+"/github/app/repos", nil, 200, &repos)
	gh.mu.Lock()
	if gh.listings != pages || pages != 2 {
		t.Errorf("listing pages: %d then %d (want 2, cached)", pages, gh.listings)
	}
	gh.mu.Unlock()
	// MCP: the same, read-only.
	var tok struct{ Token string }
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/mcp-tokens", map[string]any{"name": "gha", "scopes": []string{"read"}, "expires_in_days": 1}, 201, &tok)
	if res := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "github_app_status", nil); res.IsError || !strings.Contains(res.Text, "acme") {
		t.Errorf("github_app_status: %+v", res)
	}
	if res := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "github_app_repos", map[string]any{"limit": 5}); res.IsError || !strings.Contains(res.Text, "acme/r0") || !strings.Contains(res.Text, "125 more") {
		t.Errorf("github_app_repos: %+v", res)
	}
	var tokB struct{ Token string }
	stranger.mustJSON("POST", "/api/teams/"+teamB.ID+"/mcp-tokens", map[string]any{"name": "ghb", "scopes": []string{"read"}, "expires_in_days": 1}, 201, &tokB)
	for _, tool := range []string{"github_app_status", "github_app_repos"} {
		if res := mcpCall(t, ts.URL, "/api/mcp", tokB.Token, tool, nil); strings.Contains(res.Text, "acme") {
			t.Errorf("%s as team B leaked team A's installation: %s", tool, res.Text)
		}
	}

	// ---- webhooks ----------------------------------------------------------------------------
	srv, priv := enrolServer(t, owner, ts.URL, team.ID, "gha-"+suffix)
	var app struct {
		App struct{ ID, Name string } `json:"app"`
	}
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/apps", map[string]any{"name": "gha-" + suffix, "server": srv, "repo_url": "https://github.com/acme/r7", "branch": "main"}, 201, &app)
	// Team B points an app at the very same repository: the installation is not B's, so B's app never deploys from it.
	srvB, _ := enrolServer(t, stranger, ts.URL, teamB.ID, "ghb-"+suffix)
	var appB struct {
		App struct{ ID string } `json:"app"`
	}
	stranger.mustJSON("POST", "/api/teams/"+teamB.ID+"/apps", map[string]any{"name": "ghb-" + suffix, "server": srvB, "repo_url": "https://github.com/acme/r7", "branch": "main"}, 201, &appB)
	deployments := func(appID string) int {
		return sysCount(t, sqlDB, `SELECT count(*) FROM deployments WHERE app_id = $1`, appID)
	}
	push := map[string]any{"ref": "refs/heads/main", "installation": map[string]any{"id": instID},
		"repository": map[string]any{"html_url": "https://github.com/acme/r7", "full_name": "acme/r7"}}

	// The signature is required.
	if status, _ := deliver(t, ts.URL, nil, "push", suffix+"-d-nosig", push); status != 401 {
		t.Fatalf("unsigned push -> %d", status)
	}
	if status, _ := deliver(t, ts.URL, []byte("wrong"), "push", suffix+"-d-badsig", push); status != 401 {
		t.Fatalf("badly signed push -> %d", status)
	}
	if deployments(app.App.ID) != 0 {
		t.Fatal("an unsigned push deployed")
	}

	// The fake agent: provisioning passes; the build shows what it received, then stops the pipeline.
	var (
		amu       sync.Mutex
		buildSeen *agentproto.BuildParams
		cloneTok  string
	)
	startFakeAgent(t, ts.URL, srv, priv, func(op agentproto.SignedOp) (any, string) {
		switch op.Kind {
		case agentproto.KindProvisionStep, agentproto.KindAppDrain:
			return map[string]any{"ok": true}, ""
		case agentproto.KindAppBuild:
			var p agentproto.BuildParams
			_ = json.Unmarshal(op.Params, &p)
			pt, err := agentproto.OpenBox(priv, agentproto.GitTokenContext(p.RepoURL), p.Token)
			amu.Lock()
			buildSeen = &p
			if err == nil {
				cloneTok = string(pt)
			}
			amu.Unlock()
			return nil, "stop here (test)"
		}
		return nil, "this fake agent does not run " + op.Kind
	})

	status, out := deliver(t, ts.URL, secret, "push", suffix+"-d-1", push)
	if status != 202 || out["queued"] != float64(1) {
		t.Fatalf("push -> %d %v", status, out)
	}
	status, out = deliver(t, ts.URL, secret, "push", suffix+"-d-1", push) // GitHub's redelivery
	if status != 202 || out["queued"] != float64(0) || out["duplicate"] != true {
		t.Fatalf("redelivery -> %d %v", status, out)
	}
	if n := deployments(app.App.ID); n != 1 {
		t.Fatalf("deployments after a delivery and its redelivery: %d (want 1)", n)
	}
	if n := deployments(appB.App.ID); n != 0 {
		t.Fatalf("team B's app on the same repository deployed from team A's installation: %d", n)
	}
	// Another branch, and a push of an installation no team holds: nothing.
	other := map[string]any{"ref": "refs/heads/dev", "installation": map[string]any{"id": instID}, "repository": map[string]any{"html_url": "https://github.com/acme/r7"}}
	if _, out := deliver(t, ts.URL, secret, "push", suffix+"-d-2", other); out["queued"] != float64(0) {
		t.Fatalf("push to another branch: %v", out)
	}
	stray := map[string]any{"ref": "refs/heads/main", "installation": map[string]any{"id": 5151}, "repository": map[string]any{"html_url": "https://github.com/acme/r7"}}
	if _, out := deliver(t, ts.URL, secret, "push", suffix+"-d-3", stray); out["skipped"] == nil {
		t.Fatalf("push of an unconnected installation: %v", out)
	}

	// The build got a clone token sealed to its server, for that repository, that opens to the token
	// GitHub minted — and that token is nowhere in the database.
	deadline := time.Now().Add(60 * time.Second)
	for {
		amu.Lock()
		seen, tokOpened := buildSeen, cloneTok
		amu.Unlock()
		if seen != nil {
			if seen.Token == "" || strings.HasPrefix(seen.Token, "ghs_") || !strings.HasPrefix(tokOpened, "ghs_FAKEINSTALLTOKEN") {
				t.Fatalf("clone token: box %q opened to %q", seen.Token, tokOpened)
			}
			for _, q := range []string{
				`SELECT count(*) FROM operations WHERE params::text LIKE '%' || $1 || '%' OR coalesce(result::text, '') LIKE '%' || $1 || '%' OR coalesce(error, '') LIKE '%' || $1 || '%'`,
				`SELECT count(*) FROM activity_log WHERE changes LIKE '%' || $1 || '%'`,
				`SELECT count(*) FROM apps WHERE coalesce(repo_url, '') LIKE '%' || $1 || '%'`,
				`SELECT count(*) FROM deployments WHERE coalesce(error, '') LIKE '%' || $1 || '%'`,
			} {
				if n := sysCount(t, sqlDB, q, "ghs_FAKEINSTALL"); n != 0 {
					t.Errorf("the installation token is stored in the clear: %s -> %d", q, n)
				}
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the build never reached the agent")
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Paused automatic deploys: a push queues nothing.
	owner.mustJSON("PATCH", "/api/teams/"+team.ID+"/apps/"+app.App.ID, map[string]any{"autodeploy_paused": true}, 200, nil)
	if _, out := deliver(t, ts.URL, secret, "push", suffix+"-d-4", push); out["queued"] != float64(0) {
		t.Fatalf("push with automatic deploys paused: %v", out)
	}

	// Installation events: suspend → the listing and pushes stop; unsuspend; deleted → the row goes.
	evt := func(action string) map[string]any {
		return map[string]any{"action": action, "installation": map[string]any{"id": instID, "account": map[string]any{"login": "acme"}}}
	}
	if status, _ := deliver(t, ts.URL, nil, "installation", suffix+"-d-5", evt("deleted")); status != 401 || stored() != 1 {
		t.Fatalf("an unsigned installation event -> %d (stored %d)", status, stored())
	}
	deliver(t, ts.URL, secret, "installation", suffix+"-d-6", evt("suspend"))
	if n := sysCount(t, sqlDB, `SELECT count(*) FROM github_installations WHERE installation_id = $1 AND suspended_at IS NOT NULL`, instID); n != 1 {
		t.Fatal("suspend was not recorded")
	}
	deliver(t, ts.URL, secret, "installation_repositories", suffix+"-d-7", map[string]any{"action": "added", "repository_selection": "all", "installation": map[string]any{"id": instID}})
	deliver(t, ts.URL, secret, "installation", suffix+"-d-8", evt("unsuspend"))
	if n := sysCount(t, sqlDB, `SELECT count(*) FROM github_installations WHERE installation_id = $1 AND suspended_at IS NULL AND repository_selection = 'all'`, instID); n != 1 {
		t.Fatal("unsuspend / repository selection was not recorded")
	}
	deliver(t, ts.URL, secret, "installation", suffix+"-d-9", evt("deleted"))
	if stored() != 0 {
		t.Fatal("a deleted installation stayed")
	}
}
