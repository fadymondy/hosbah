package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fadymondy/hosbah/catalog"
	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

type installJSON struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Recipe        string   `json:"recipe"`
	RecipeName    string   `json:"recipe_name"`
	RecipeVersion string   `json:"recipe_version"`
	Image         string   `json:"image"`
	HasWebUI      bool     `json:"has_web_ui"`
	HostPort      *int64   `json:"host_port"`
	ProxyHostID   *string  `json:"proxy_host_id"`
	Hostname      *string  `json:"hostname"`
	EnvInjected   []string `json:"env_injected"`
	Status        string   `json:"status"`
	Error         *string  `json:"error"`
	Running       *bool    `json:"running"`
	Restarts      *int64   `json:"restarts"`
	MemoryBytes   *int64   `json:"memory_bytes"`
	Skipped       []string `json:"skipped"`
	Applies       string   `json:"applies"`
	Operation     string   `json:"operation"`
}

type installsJSON struct {
	Installs        []installJSON `json:"installs"`
	StatusOperation *string       `json:"status_operation"`
}

/*
"Apps & stacks" end to end with a fake agent: the catalog, what an install sends (a recipe's id and
version — never anything to run), generated secrets sealed at rest, sealed to the agent in the
operation and nowhere in plaintext (params, result, log, REST, audit), the variables set on the site
and their collisions, the proxy host an install creates and follows, the cap, removal that unsets
exactly its own variables and deletes its own proxy host, roles, other teams, imported apps, MCP and
the cascade.
*/
func TestAppInstalls(t *testing.T) {
	ts, stop := bootTestServer(t)
	defer stop()
	sqlDB := testDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	short := suffix[len(suffix)-6:]
	ctx := context.Background()

	owner := newClient(t, ts.URL)
	owner.register("in-owner-" + suffix + "@example.test")
	var team struct{ ID string }
	owner.mustJSON("POST", "/api/teams", map[string]any{"name": "Installs " + suffix}, 201, &team)
	var readTok, writeTok struct{ Token string }
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/mcp-tokens", map[string]any{"name": "in-read", "scopes": []string{"read"}, "expires_in_days": 30}, 201, &readTok)
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/mcp-tokens", map[string]any{"name": "in-write", "scopes": []string{"read", "write"}, "expires_in_days": 30}, 201, &writeTok)

	viewerEmail := "in-viewer-" + suffix + "@example.test"
	viewer := newClient(t, ts.URL)
	viewer.register(viewerEmail)
	var inv struct {
		AcceptURL string `json:"accept_url"`
	}
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/invitations", map[string]any{"email": viewerEmail, "role": "viewer"}, 201, &inv)
	viewer.mustJSON("POST", "/api/invitations/"+inv.AcceptURL[strings.LastIndex(inv.AcceptURL, "/")+1:]+"/accept", nil, 200, nil)

	stranger := newClient(t, ts.URL)
	stranger.register("in-stranger-" + suffix + "@example.test")
	stranger.mustJSON("POST", "/api/teams", map[string]any{"name": "Elsewhere " + suffix}, 201, nil)

	srv, priv := enrolWithServicesKey(t, owner, ts.URL, team.ID, "in-"+short, "203.0.113.47", []agentproto.Service{
		{Kind: "container", Name: "imp-in-" + short, Image: "nginx:1.27", Status: "running", Ports: []string{"0.0.0.0:8085->80/tcp"}}})
	var app struct {
		App struct{ ID, Name string } `json:"app"`
	}
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/apps", map[string]any{"name": "in-" + short, "server": srv, "repo_url": "https://github.com/octocat/api.git"}, 201, &app)
	appBase := func(id string) string { return "/api/teams/" + team.ID + "/apps/" + id }
	base := appBase(app.App.ID)
	cat, err := catalog.Builtin()
	if err != nil {
		t.Fatal(err)
	}
	mailpit, _ := cat.Get("mailpit")
	searxng, _ := cat.Get("searxng")

	var (
		mu           sync.Mutex
		statusSilent bool
		removeErr    string
		installErr   string
		port         = 49300 // the loopback port "docker" picks; recipe.status reports statusPort
		statusPort   = map[string]int{}
	)
	agent := startFakeAgent(t, ts.URL, srv, priv, func(op agentproto.SignedOp) (any, string) {
		mu.Lock()
		defer mu.Unlock()
		switch op.Kind {
		case agentproto.KindRecipeInstall:
			var p agentproto.RecipeInstallParams
			_ = json.Unmarshal(op.Params, &p)
			if err := p.Validate(); err != nil {
				return nil, err.Error()
			}
			if installErr != "" {
				return nil, installErr
			}
			port++
			statusPort[p.InstallID] = port
			return agentproto.RecipeInstallResult{Container: agentproto.InstallContainerName(p.InstallID), HostPort: port}, ""
		case agentproto.KindRecipeRemove:
			var p agentproto.RecipeRemoveParams
			_ = json.Unmarshal(op.Params, &p)
			if err := p.Validate(); err != nil {
				return nil, err.Error()
			}
			if removeErr != "" {
				return nil, removeErr
			}
			return agentproto.RecipeRemoveResult{Container: agentproto.InstallContainerName(p.InstallID), Removed: true, VolumesRemoved: []string{}}, ""
		case agentproto.KindRecipeStatus:
			if statusSilent {
				return nil, "docker is not answering"
			}
			var p agentproto.RecipeStatusParams
			_ = json.Unmarshal(op.Params, &p)
			if err := p.Validate(); err != nil {
				return nil, err.Error()
			}
			res := agentproto.RecipeStatusResult{}
			for _, id := range p.InstallIDs {
				mem := int64(99 << 20)
				res.Installs = append(res.Installs, agentproto.InstallState{InstallID: id, Running: true, Restarts: 1, MemoryBytes: &mem, HostPort: statusPort[id]})
			}
			return res, ""
		case agentproto.KindAppRemove:
			return map[string]any{"containers": []string{}}, ""
		case agentproto.KindProxySet, agentproto.KindProvisionStep:
			return map[string]any{}, ""
		}
		return nil, "this fake agent does not run " + op.Kind
	})

	list := func(c *client, id string) installsJSON {
		t.Helper()
		var s installsJSON
		c.mustJSON("GET", appBase(id)+"/installs", nil, 200, &s)
		return s
	}
	wait := func(ref, want string) installJSON {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for {
			for _, b := range list(owner, app.App.ID).Installs {
				if (b.ID == ref || b.Name == ref) && b.Status == want {
					return b
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("install %s never became %s: %+v", ref, want, list(owner, app.App.ID).Installs)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	envNames := func() map[string]bool {
		t.Helper()
		var env struct {
			Variables []struct{ Name string } `json:"variables"`
		}
		owner.mustJSON("GET", base+"/env", nil, 200, &env)
		out := map[string]bool{}
		for _, v := range env.Variables {
			out[v.Name] = true
		}
		return out
	}
	reveal := func(name string) string {
		t.Helper()
		var out struct{ Value string }
		owner.mustJSON("POST", base+"/env/"+name+"/reveal", nil, 200, &out)
		return out.Value
	}
	type proxyJSON struct {
		ID, Hostname, Upstream string
		Enabled                bool
	}
	proxies := func() map[string]proxyJSON {
		t.Helper()
		out := map[string]proxyJSON{}
		if err := tenancy.Tx(ctx, sqlDB, tenancy.Scope{System: true}, func(_ *db.Queries, tx *sql.Tx) error {
			rows, err := tx.Query(`SELECT id, hostname, upstream, enabled FROM proxy_hosts WHERE team_id = $1`, team.ID)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var p proxyJSON
				if err := rows.Scan(&p.ID, &p.Hostname, &p.Upstream, &p.Enabled); err != nil {
					return err
				}
				out[p.Hostname] = p
			}
			return rows.Err()
		}); err != nil {
			t.Fatal(err)
		}
		return out
	}

	t.Run("the catalog: any member reads it, another team's URL is 404", func(t *testing.T) {
		var c struct {
			Recipes []struct {
				ID, Name, Summary, Kind, Licence, Homepage, Version string
				Verified                                            bool
				Needs                                               struct{ Postgres, Redis bool }
				HasWebUI                                            bool `json:"has_web_ui"`
			} `json:"recipes"`
		}
		viewer.mustJSON("GET", "/api/teams/"+team.ID+"/catalog", nil, 200, &c)
		if len(c.Recipes) != len(cat.List("", "")) {
			t.Fatalf("catalog: %+v", c.Recipes)
		}
		for _, r := range c.Recipes {
			full, _ := cat.Get(r.ID)
			if r.Verified != full.Verified || r.Version != full.Version || r.Licence == "" || r.HasWebUI != full.HasWebUI || r.Kind != full.Kind {
				t.Errorf("summary of %s: %+v", r.ID, r)
			}
		}
		viewer.mustJSON("GET", "/api/teams/"+team.ID+"/catalog?kind=stack", nil, 200, &c)
		for _, r := range c.Recipes {
			if r.Kind != "stack" {
				t.Errorf("kind=stack lists %s (%s)", r.ID, r.Kind)
			}
		}
		viewer.mustJSON("GET", "/api/teams/"+team.ID+"/catalog?kind=app&q=MAILPIT", nil, 200, &c)
		if len(c.Recipes) != 1 || c.Recipes[0].ID != "mailpit" {
			t.Errorf("q: %+v", c.Recipes)
		}
		viewer.mustJSON("GET", "/api/teams/"+team.ID+"/catalog?kind=plugin", nil, 422, nil)
		var full struct {
			ID, Image string
			Verified  bool
			Limits    struct {
				MemoryMB int `json:"memory_mb"`
			}
			Env []struct {
				Name     string
				Generate *struct{ Length int }
			}
			HasWebUI bool `json:"has_web_ui"`
		}
		viewer.mustJSON("GET", "/api/teams/"+team.ID+"/catalog/mailpit", nil, 200, &full)
		if full.Image != mailpit.Image || full.Limits.MemoryMB != mailpit.Limits.MemoryMB || !full.HasWebUI || len(full.Env) != len(mailpit.Env) {
			t.Errorf("recipe: %+v", full)
		}
		var stack struct {
			Verified bool
			Stack    *struct {
				Repo, Branch, Runtime string
				HosbahTOML            string `json:"hosbah_toml"`
				CloudyTOML            string `json:"cloudy_toml"` // the pre-rename key, still sent
			}
		}
		viewer.mustJSON("GET", "/api/teams/"+team.ID+"/catalog/laravel", nil, 200, &stack)
		if stack.Stack == nil || !strings.HasPrefix(stack.Stack.Repo, "https://github.com/") || stack.Stack.HosbahTOML == "" || stack.Stack.CloudyTOML != stack.Stack.HosbahTOML {
			t.Errorf("stack: %+v", stack)
		}
		viewer.mustJSON("GET", "/api/teams/"+team.ID+"/catalog/no-such-recipe", nil, 404, nil)
		stranger.mustJSON("GET", "/api/teams/"+team.ID+"/catalog", nil, 404, nil)
		stranger.mustJSON("GET", "/api/teams/"+team.ID+"/catalog/mailpit", nil, 404, nil)
	})

	t.Run("what an install must be", func(t *testing.T) {
		if s := list(viewer, app.App.ID); s.Installs == nil || len(s.Installs) != 0 || s.StatusOperation != nil {
			t.Fatalf("no installs is an empty list and no operation: %+v", s)
		}
		for name, c := range map[string]struct {
			body map[string]any
			want int
		}{
			"upper-case name":         {map[string]any{"recipe": "mailpit", "name": "Mail"}, 422},
			"digit first":             {map[string]any{"recipe": "mailpit", "name": "1mail"}, 422},
			"no name":                 {map[string]any{"recipe": "mailpit"}, 422},
			"no recipe":               {map[string]any{"name": "mail"}, 422},
			"an unknown recipe":       {map[string]any{"recipe": "no-such-recipe", "name": "mail"}, 404},
			"a stack":                 {map[string]any{"recipe": "laravel", "name": "site"}, 422},
			"an image of theirs":      {map[string]any{"recipe": "mailpit", "name": "mail", "image": "evil/miner:1"}, 422},
			"a service it cannot use": {map[string]any{"recipe": "mailpit", "name": "mail", "postgres": "some-pg"}, 422},
			"a redis it cannot use":   {map[string]any{"recipe": "mailpit", "name": "mail", "redis": "some-redis"}, 422},
			"a bad hostname":          {map[string]any{"recipe": "mailpit", "name": "mail", "hostname": "not a hostname"}, 422},
		} {
			if status, text := owner.do("POST", base+"/installs", c.body); status != c.want {
				t.Errorf("%s: %d (want %d): %s", name, status, c.want, text)
			}
		}
		viewer.mustJSON("POST", base+"/installs", map[string]any{"recipe": "mailpit", "name": "mail"}, 403, nil)
		stranger.mustJSON("POST", base+"/installs", map[string]any{"recipe": "mailpit", "name": "mail"}, 404, nil)
		stranger.mustJSON("GET", base+"/installs", nil, 404, nil)
		if s := list(owner, app.App.ID); len(s.Installs) != 0 || len(envNames()) != 0 || len(agentOps(agent, agentproto.KindRecipeInstall)) != 0 || len(proxies()) != 0 {
			t.Fatalf("refused installs leave nothing: %+v %v %v", s.Installs, envNames(), proxies())
		}
	})

	t.Run("an imported app has no installs", func(t *testing.T) {
		apps, _ := listImported(t, owner, team.ID, srv)
		imp, ok := apps["imp-in-"+short]
		if !ok {
			t.Fatalf("no imported app: %+v", apps)
		}
		owner.mustJSON("POST", appBase(imp.ID)+"/installs", map[string]any{"recipe": "mailpit", "name": "mail"}, 422, nil)
		owner.mustJSON("DELETE", appBase(imp.ID)+"/installs/mail", map[string]any{"confirm": "mail"}, 422, nil)
		if s := list(owner, imp.ID); len(s.Installs) != 0 {
			t.Fatalf("imported app's installs: %+v", s)
		}
		if res := mcpCall(t, ts.URL, "/api/mcp", writeTok.Token, "install_recipe", map[string]any{"app": imp.ID, "recipe": "mailpit", "name": "mail", "confirm": "mail"}); !res.IsError {
			t.Errorf("MCP install_recipe on an imported app: %+v", res)
		}
	})

	var password string
	mailHost := "mail-" + short + ".example.test"
	t.Run("an install: the wire names a recipe, the secret is sealed at rest and to the agent, and nowhere else", func(t *testing.T) {
		status, text := owner.do("POST", base+"/installs", map[string]any{"recipe": "mailpit", "name": "mail", "hostname": mailHost})
		if status != 201 {
			t.Fatalf("create: %d %s", status, text)
		}
		var created installJSON
		_ = json.Unmarshal([]byte(text), &created)
		if created.Name != "mail" || created.Recipe != "mailpit" || created.RecipeName != "Mailpit" || created.RecipeVersion != mailpit.Version ||
			created.Image != mailpit.Image || !created.HasWebUI || created.HostPort != nil || created.ProxyHostID == nil ||
			created.Hostname == nil || *created.Hostname != mailHost ||
			fmt.Sprint(created.EnvInjected) != "[MAILPIT_PASSWORD MAILPIT_URL MAILPIT_USER]" || len(created.Skipped) != 0 ||
			created.Status != "creating" || created.Applies != "next_deploy" || created.Operation == "" {
			t.Fatalf("created: %+v", created)
		}
		ready := wait(created.ID, "ready")
		if ready.HostPort == nil || *ready.HostPort != 49301 {
			t.Fatalf("the loopback port the agent answered is recorded: %+v", ready)
		}
		// Its proxy host: reserved disabled with the request, pointed at the port and enabled once it is up.
		if p := proxies()[mailHost]; p.ID != *created.ProxyHostID || p.Upstream != "http://127.0.0.1:49301" || !p.Enabled {
			t.Fatalf("proxy host: %+v", p)
		}
		password = reveal("MAILPIT_PASSWORD")
		if !regexp.MustCompile(`^[A-Za-z0-9]{32}$`).MatchString(password) {
			t.Fatalf("the generated password is 32 alphanumerics: %d chars", len(password))
		}
		if u := reveal("MAILPIT_URL"); u != "https://"+mailHost {
			t.Errorf("MAILPIT_URL: %s", u)
		}
		if u := reveal("MAILPIT_USER"); u != "hosbah" {
			t.Errorf("MAILPIT_USER: %s", u)
		}
		// No response carries it; there is no reveal route for an install.
		if strings.Contains(text, password) || strings.Contains(text, "secrets") || strings.Contains(text, "csv1:") {
			t.Error("the create response carries a secret")
		}
		if _, body := owner.do("GET", base+"/installs", nil); strings.Contains(body, password) || strings.Contains(body, "secrets") || strings.Contains(body, "csv1:") {
			t.Errorf("GET installs carries a secret: %s", body)
		}
		for _, p := range []string{"/secrets", "/reveal", "/secrets/reveal"} {
			if status, _ := owner.do("POST", base+"/installs/mail"+p, nil); status == 200 {
				t.Errorf("POST …/installs/mail%s answers 200", p)
			}
			if status, body := owner.do("GET", base+"/installs/mail"+p, nil); status == 200 && strings.Contains(body, password) {
				t.Errorf("GET …/installs/mail%s reveals the secret", p)
			}
		}
		var sealed, params string
		var result sql.NullString
		if err := tenancy.Tx(ctx, sqlDB, tenancy.Scope{System: true}, func(_ *db.Queries, tx *sql.Tx) error {
			if err := tx.QueryRow(`SELECT secrets FROM app_installs WHERE id = $1`, created.ID).Scan(&sealed); err != nil {
				return err
			}
			return tx.QueryRow(`SELECT params::text, result::text FROM operations WHERE id = $1`, created.Operation).Scan(&params, &result)
		}); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(sealed, "csv1:") || strings.Contains(sealed, password) {
			t.Errorf("app_installs.secrets is not sealed: %.20s…", sealed)
		}
		if strings.Contains(params, password) || strings.Contains(result.String, password) {
			t.Error("operations.params or .result holds the secret in plaintext")
		}
		if _, body := owner.do("GET", "/api/teams/"+team.ID+"/servers/"+srv+"/operations/"+created.Operation, nil); strings.Contains(body, password) {
			t.Error("the operation (params, result or log) shows the secret")
		}
		ops := agentOps(agent, agentproto.KindRecipeInstall)
		if len(ops) != 1 {
			t.Fatalf("one recipe.install, got %d", len(ops))
		}
		// The wire: ids, the recipe's id and version, the image to compare, a box. Nothing to run.
		var wire map[string]any
		_ = json.Unmarshal(ops[0].Params, &wire)
		for k := range wire {
			switch k {
			case "install_id", "app_id", "recipe_id", "recipe_version", "image", "secrets":
			default:
				t.Errorf("recipe.install carries %q — the agent takes everything else from its own catalog", k)
			}
		}
		var p agentproto.RecipeInstallParams
		_ = json.Unmarshal(ops[0].Params, &p)
		if p.InstallID != created.ID || p.AppID != app.App.ID || p.RecipeID != "mailpit" || p.RecipeVersion != mailpit.Version || p.Image != mailpit.Image ||
			ops[0].TimeoutSeconds < 1200 {
			t.Fatalf("recipe.install params: %+v (timeout %d)", p, ops[0].TimeoutSeconds)
		}
		pt, err := agentproto.OpenBox(priv, agentproto.InstallSecretsContext(created.ID), p.Secrets)
		if err != nil {
			t.Fatalf("the agent's box does not open: %v", err)
		}
		var sec agentproto.InstallSecrets
		_ = json.Unmarshal(pt, &sec)
		if len(sec.Env) != 1 || sec.Env["MAILPIT_PASSWORD"] != password {
			t.Fatalf("the box carries exactly the generated values, and the site's password is the container's")
		}
		if _, err := agentproto.OpenBox(priv, agentproto.InstallSecretsContext("00000000-0000-4000-8000-0000000000e9"), p.Secrets); err == nil {
			t.Error("the box opens for another install")
		}
	})

	t.Run("status is best effort, and a port that moved is followed", func(t *testing.T) {
		s := list(viewer, app.App.ID)
		if len(s.Installs) != 1 || s.StatusOperation == nil {
			t.Fatalf("list: %+v", s)
		}
		b := s.Installs[0]
		if b.Running == nil || !*b.Running || b.Restarts == nil || *b.Restarts != 1 || b.MemoryBytes == nil || *b.MemoryBytes != 99<<20 {
			t.Fatalf("status: %+v", b)
		}
		mu.Lock()
		statusSilent = true
		mu.Unlock()
		s = list(owner, app.App.ID)
		mu.Lock()
		statusSilent = false
		mu.Unlock()
		if b := s.Installs[0]; b.Running != nil || b.Restarts != nil || b.MemoryBytes != nil || b.Status != "ready" {
			t.Fatalf("a server that does not answer leaves nulls, still 200: %+v", b)
		}
		// The container restarted: Docker picked another loopback port. Even a viewer's listing follows it.
		mu.Lock()
		statusPort[b.ID] = 49555
		mu.Unlock()
		if b := list(viewer, app.App.ID).Installs[0]; b.HostPort == nil || *b.HostPort != 49555 {
			t.Fatalf("the moved port: %+v", b)
		}
		if p := proxies()[mailHost]; p.Upstream != "http://127.0.0.1:49555" || !p.Enabled {
			t.Fatalf("the proxy host follows the port: %+v", p)
		}
	})

	t.Run("collisions: name, hostname, env names, no hostname, cap", func(t *testing.T) {
		owner.mustJSON("POST", base+"/installs", map[string]any{"recipe": "searxng", "name": "mail"}, 409, nil)
		if status, text := owner.do("POST", base+"/installs", map[string]any{"recipe": "mailpit", "name": "mail2"}); status != 409 || !strings.Contains(text, "MAILPIT_") {
			t.Fatalf("env collision: %d %s", status, text)
		}
		if status, text := owner.do("POST", base+"/installs", map[string]any{"recipe": "searxng", "name": "search", "hostname": mailHost}); status != 409 {
			t.Fatalf("a taken hostname: %d %s", status, text)
		}
		owner.mustJSON("PUT", base+"/env/SEARXNG_URL", map[string]any{"value": "mine"}, 204, nil)
		if status, text := owner.do("POST", base+"/installs", map[string]any{"recipe": "searxng", "name": "search", "hostname": "s-" + short + ".example.test"}); status != 409 || !strings.Contains(text, "SEARXNG_URL") {
			t.Fatalf("hand-set variable: %d %s", status, text)
		}
		if reveal("SEARXNG_URL") != "mine" || len(list(owner, app.App.ID).Installs) != 1 || len(proxies()) != 1 {
			t.Fatalf("a refused install must leave nothing: %v", proxies())
		}
		// Without a hostname the site has no address for it: nothing that needs one is set, and the answer says so.
		var search installJSON
		owner.mustJSON("POST", base+"/installs", map[string]any{"recipe": "searxng", "name": "search"}, 201, &search)
		if len(search.EnvInjected) != 0 || fmt.Sprint(search.Skipped) != "[SEARXNG_URL]" || search.ProxyHostID != nil || search.Hostname != nil {
			t.Fatalf("no hostname: %+v", search)
		}
		wait("search", "ready")
		if reveal("SEARXNG_URL") != "mine" {
			t.Error("a skipped variable must not be touched")
		}
		ops := agentOps(agent, agentproto.KindRecipeInstall)
		var p agentproto.RecipeInstallParams
		_ = json.Unmarshal(ops[len(ops)-1].Params, &p)
		if p.RecipeID != "searxng" || p.Image != searxng.Image {
			t.Fatalf("searxng params: %+v", p)
		}
		if pt, err := agentproto.OpenBox(priv, agentproto.InstallSecretsContext(search.ID), p.Secrets); err != nil || !strings.Contains(string(pt), "SEARXNG_SECRET") {
			t.Fatalf("searxng's generated secret travels in the box: %v", err)
		}

		// A failed install: failed with the agent's words, and the hostname it reserved is given back.
		mu.Lock()
		installErr = "recipe searxng is version 0.9.0 in this agent's catalog, the control plane asked for 1.0.0 — update the agent on this server and try again"
		mu.Unlock()
		failHost := "f-" + short + ".example.test"
		owner.mustJSON("DELETE", base+"/env/SEARXNG_URL", nil, 204, nil)
		owner.mustJSON("POST", base+"/installs", map[string]any{"recipe": "searxng", "name": "old-agent", "hostname": failHost}, 201, nil)
		if b := wait("old-agent", "failed"); b.Error == nil || !strings.Contains(*b.Error, "update the agent") || b.ProxyHostID != nil {
			t.Fatalf("failed install: %+v", b)
		}
		mu.Lock()
		installErr = ""
		mu.Unlock()
		if _, held := proxies()[failHost]; held {
			t.Errorf("a failed install must give its hostname back: %v", proxies())
		}
		if !envNames()["SEARXNG_URL"] {
			t.Error("a failed install keeps what it set until it is removed")
		}
		owner.mustJSON("DELETE", base+"/installs/old-agent", map[string]any{"confirm": "old-agent"}, 200, nil)
		if envNames()["SEARXNG_URL"] {
			t.Error("removing the failed install unsets what it set")
		}

		for i := 3; i <= agentproto.MaxAppInstalls; i++ {
			owner.mustJSON("POST", base+"/installs", map[string]any{"recipe": "searxng", "name": fmt.Sprintf("s%d", i)}, 201, nil)
		}
		if status, text := owner.do("POST", base+"/installs", map[string]any{"recipe": "searxng", "name": "one-too-many"}); status != 409 || !strings.Contains(text, "at most 10") {
			t.Fatalf("the cap: %d %s", status, text)
		}
		for i := 3; i <= agentproto.MaxAppInstalls; i++ {
			wait(fmt.Sprintf("s%d", i), "ready")
		}
	})

	t.Run("removing an install unsets exactly its own variables and deletes its own proxy host", func(t *testing.T) {
		otherHost := "keep-" + short + ".example.test"
		owner.mustJSON("POST", "/api/teams/"+team.ID+"/proxy-hosts", map[string]any{"server": srv, "hostname": otherHost, "upstream": "10.0.0.5:8080"}, 201, nil)
		owner.mustJSON("PUT", base+"/env/MAILPIT_NOTE", map[string]any{"value": "mine"}, 204, nil)
		ref := base + "/installs/mail"
		viewer.mustJSON("DELETE", ref, map[string]any{"confirm": "mail"}, 403, nil)
		stranger.mustJSON("DELETE", ref, map[string]any{"confirm": "mail"}, 404, nil)
		owner.mustJSON("DELETE", ref, map[string]any{"confirm": "nope"}, 422, nil)
		owner.mustJSON("DELETE", base+"/installs/nope", map[string]any{"confirm": "nope"}, 404, nil)

		mu.Lock()
		removeErr = "could not remove the install's container"
		mu.Unlock()
		if status, text := owner.do("DELETE", ref, map[string]any{"confirm": "mail"}); status != 409 {
			t.Fatalf("a failed removal: %d %s", status, text)
		}
		if b := wait("mail", "ready"); b.Error == nil || !envNames()["MAILPIT_PASSWORD"] || proxies()[mailHost].ID == "" {
			t.Fatalf("a refused removal leaves the install, its variables and its proxy host: %+v", b)
		}
		mu.Lock()
		removeErr = ""
		mu.Unlock()

		var out struct {
			Removed     bool     `json:"removed"`
			Unset       []string `json:"unset"`
			DataDeleted bool     `json:"data_deleted"`
			Applies     string   `json:"applies"`
			Operation   *string  `json:"operation"`
		}
		owner.mustJSON("DELETE", ref, map[string]any{"confirm": "mail", "delete_data": true}, 200, &out)
		if !out.Removed || fmt.Sprint(out.Unset) != "[MAILPIT_PASSWORD MAILPIT_URL MAILPIT_USER]" || !out.DataDeleted || out.Operation == nil || out.Applies != "next_deploy" {
			t.Fatalf("removed: %+v", out)
		}
		rm := agentOps(agent, agentproto.KindRecipeRemove)
		var p agentproto.RecipeRemoveParams
		_ = json.Unmarshal(rm[len(rm)-1].Params, &p)
		if p.AppID != app.App.ID || !agentproto.ValidID(p.InstallID) || !p.DeleteData {
			t.Errorf("recipe.remove params: %+v", p)
		}
		_ = json.Unmarshal(rm[0].Params, &p)
		if p.DeleteData {
			t.Error("delete_data is false unless asked")
		}
		names := envNames()
		for _, n := range []string{"MAILPIT_PASSWORD", "MAILPIT_URL", "MAILPIT_USER"} {
			if names[n] {
				t.Errorf("%s is still set: %v", n, names)
			}
		}
		if !names["MAILPIT_NOTE"] || reveal("MAILPIT_NOTE") != "mine" {
			t.Errorf("a variable the install did not set went too: %v", names)
		}
		ps := proxies()
		if _, there := ps[mailHost]; there {
			t.Errorf("the install's proxy host is still there: %v", ps)
		}
		if _, there := ps[otherHost]; !there {
			t.Errorf("a proxy host the install did not create went too: %v", ps)
		}

		// MCP: catalog, list, install and remove, with confirm; never a secret.
		res := mcpCall(t, ts.URL, "/api/mcp", readTok.Token, "list_catalog", map[string]any{"kind": "app"})
		if res.IsError || !strings.Contains(res.Text, "mailpit") || !strings.Contains(res.Text, "verified") || strings.Contains(res.Text, "laravel") {
			t.Fatalf("MCP list_catalog: %+v", res)
		}
		if res := mcpCall(t, ts.URL, "/api/mcp", readTok.Token, "list_catalog", map[string]any{"kind": "stack"}); res.IsError || !strings.Contains(res.Text, "not yet verified") {
			t.Errorf("MCP list_catalog says what is not verified: %+v", res)
		}
		if res := mcpCall(t, ts.URL, "/api/mcp", readTok.Token, "get_recipe", map[string]any{"recipe": "mailpit"}); res.IsError || !strings.Contains(res.Text, mailpit.Image) {
			t.Errorf("MCP get_recipe: %+v", res)
		}
		if res := mcpCall(t, ts.URL, "/api/mcp", readTok.Token, "get_recipe", map[string]any{"recipe": "nope"}); !res.IsError {
			t.Errorf("MCP get_recipe of nothing: %+v", res)
		}
		if res := mcpCall(t, ts.URL, "/api/mcp", readTok.Token, "list_app_installs", map[string]any{"app": app.App.ID}); res.IsError || !strings.Contains(res.Text, "search") || !strings.Contains(res.Text, "running=true") {
			t.Fatalf("MCP list_app_installs: %+v", res)
		}
		owner.mustJSON("DELETE", base+"/installs/s3", map[string]any{"confirm": "s3"}, 200, nil)
		args := map[string]any{"app": app.App.ID, "recipe": "mailpit", "name": "mcp-mail"}
		if res := mcpCall(t, ts.URL, "/api/mcp", writeTok.Token, "install_recipe", args); !res.IsError || !strings.Contains(res.Text, "confirm") {
			t.Errorf("MCP install_recipe without confirm: %+v", res)
		}
		args["confirm"] = "mcp-mail"
		if res := mcpCall(t, ts.URL, "/api/mcp", readTok.Token, "install_recipe", args); !res.IsError {
			t.Errorf("a read token must not install: %+v", res)
		}
		res = mcpCall(t, ts.URL, "/api/mcp", writeTok.Token, "install_recipe", args)
		if res.IsError || !strings.Contains(res.Text, "MAILPIT_PASSWORD") || !strings.Contains(res.Text, "MAILPIT_URL") || strings.Contains(res.Text, reveal("MAILPIT_PASSWORD")) {
			t.Fatalf("MCP install_recipe: %+v", res)
		}
		wait("mcp-mail", "ready")
		if envNames()["MAILPIT_URL"] {
			t.Error("without a hostname MAILPIT_URL is skipped")
		}
		if res := mcpCall(t, ts.URL, "/api/mcp", writeTok.Token, "remove_install", map[string]any{"app": app.App.ID, "install": "mcp-mail"}); !res.IsError || !strings.Contains(res.Text, "confirm") {
			t.Errorf("MCP remove_install without confirm: %+v", res)
		}
		if res := mcpCall(t, ts.URL, "/api/mcp", readTok.Token, "remove_install", map[string]any{"app": app.App.ID, "install": "mcp-mail", "confirm": "mcp-mail"}); !res.IsError {
			t.Errorf("a read token must not remove: %+v", res)
		}
		if res := mcpCall(t, ts.URL, "/api/mcp", writeTok.Token, "remove_install", map[string]any{"app": app.App.ID, "install": "mcp-mail", "confirm": "mcp-mail"}); res.IsError || !strings.Contains(res.Text, "MAILPIT_PASSWORD") {
			t.Errorf("MCP remove_install: %+v", res)
		}
		if envNames()["MAILPIT_PASSWORD"] {
			t.Error("MCP removal left the variables")
		}
	})

	t.Run("audited, without the secret", func(t *testing.T) {
		var audit struct {
			Entries []struct {
				Action  string          `json:"action"`
				Changes json.RawMessage `json:"changes"`
			} `json:"entries"`
		}
		owner.mustJSON("GET", "/api/teams/"+team.ID+"/audit?limit=200", nil, 200, &audit)
		got := map[string]int{}
		for _, e := range audit.Entries {
			got[e.Action]++
			if strings.Contains(string(e.Changes), password) || strings.Contains(string(e.Changes), "csv1:") {
				t.Errorf("the audit log carries a secret: %s", e.Action)
			}
		}
		if got["install.created"] != 12 || got["install.removed"] != 4 {
			t.Errorf("audit: %v", got)
		}
	})

	t.Run("installs go with their app, on the server too", func(t *testing.T) {
		count := func() (n int) {
			t.Helper()
			if err := tenancy.Tx(ctx, sqlDB, tenancy.Scope{System: true}, func(_ *db.Queries, tx *sql.Tx) error {
				return tx.QueryRow(`SELECT count(*) FROM app_installs WHERE app_id = $1`, app.App.ID).Scan(&n)
			}); err != nil {
				t.Fatal(err)
			}
			return n
		}
		if n := count(); n != 8 {
			t.Fatalf("before: %d installs", n)
		}
		owner.mustJSON("DELETE", base, map[string]any{"confirm": app.App.Name}, 204, nil)
		if n := count(); n != 0 {
			t.Fatalf("after deleting the app: %d installs", n)
		}
		// The agent's app.remove (which takes the app's install containers: internal/agent/recipes_test.go).
		deadline := time.Now().Add(15 * time.Second)
		for len(agentOps(agent, agentproto.KindAppRemove)) == 0 && time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
		}
		if len(agentOps(agent, agentproto.KindAppRemove)) != 1 {
			t.Error("app.remove never reached the agent")
		}
	})
}

/*
MH-576 D6: the app catalogue's recipes that need a managed database (WordPress needs MySQL, Odoo
needs Postgres) — the connection travels apart (host, user, password, …) in the same sealed box as a
generated secret, never as a site variable and never in plaintext in operations.params, and a service
of another team cannot be named.
*/
func TestAppInstallsWithDataServices(t *testing.T) {
	ts, stop := bootTestServer(t)
	defer stop()
	sqlDB := testDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	short := suffix[len(suffix)-6:]
	ctx := context.Background()

	owner := newClient(t, ts.URL)
	owner.register("in-ds-owner-" + suffix + "@example.test")
	var team struct{ ID string }
	owner.mustJSON("POST", "/api/teams", map[string]any{"name": "Installs DS " + suffix}, 201, &team)

	other := newClient(t, ts.URL)
	other.register("in-ds-other-" + suffix + "@example.test")
	var otherTeam struct{ ID string }
	other.mustJSON("POST", "/api/teams", map[string]any{"name": "Elsewhere DS " + suffix}, 201, &otherTeam)

	cat, err := catalog.Builtin()
	if err != nil {
		t.Fatal(err)
	}
	wordpress, _ := cat.Get("wordpress")
	odoo, _ := cat.Get("odoo")

	srv, priv := enrolWithServicesKey(t, owner, ts.URL, team.ID, "inds-"+short, "203.0.113.61", nil)
	var app struct {
		App struct{ ID, Name string } `json:"app"`
	}
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/apps", map[string]any{"name": "inds-" + short, "server": srv, "repo_url": "https://github.com/octocat/api.git"}, 201, &app)
	appBase := func(id string) string { return "/api/teams/" + team.ID + "/apps/" + id }
	base := appBase(app.App.ID)

	agent := startFakeAgent(t, ts.URL, srv, priv, func(op agentproto.SignedOp) (any, string) {
		switch op.Kind {
		case agentproto.KindDBProvision:
			var p agentproto.DBProvisionParams
			_ = json.Unmarshal(op.Params, &p)
			return agentproto.DBProvisionResult{Container: p.Container, Volume: p.Volume, Host: "172.17.0.1", Port: p.Port}, ""
		case agentproto.KindDBCreate:
			var p agentproto.DBCreateParams
			_ = json.Unmarshal(op.Params, &p)
			return agentproto.DBCreateResult{Database: p.Database, User: p.User}, ""
		case agentproto.KindRecipeInstall:
			var p agentproto.RecipeInstallParams
			_ = json.Unmarshal(op.Params, &p)
			if err := p.Validate(); err != nil {
				return nil, err.Error()
			}
			return agentproto.RecipeInstallResult{Container: agentproto.InstallContainerName(p.InstallID), HostPort: 49700}, ""
		case agentproto.KindRecipeStatus:
			var p agentproto.RecipeStatusParams
			_ = json.Unmarshal(op.Params, &p)
			res := agentproto.RecipeStatusResult{}
			for _, id := range p.InstallIDs {
				res.Installs = append(res.Installs, agentproto.InstallState{InstallID: id, Running: true, HostPort: 49700})
			}
			return res, ""
		case agentproto.KindAppRemove:
			return map[string]any{"containers": []string{}}, ""
		case agentproto.KindProxySet, agentproto.KindProvisionStep:
			return map[string]any{}, ""
		}
		return nil, "this fake agent does not run " + op.Kind
	})

	// Two managed services on the app's own server: MySQL for WordPress, Postgres for Odoo.
	var mysqlSvc, pgSvc struct{ ID, Name string }
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/data-services", map[string]any{"name": "wpdb-" + short, "server": srv, "kind": "mysql"}, 201, &mysqlSvc)
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/data-services", map[string]any{"name": "odoodb-" + short, "server": srv, "kind": "postgres"}, 201, &pgSvc)
	waitRunning := func(id string) {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for {
			var d struct {
				Service struct{ Status string } `json:"service"`
			}
			owner.mustJSON("GET", "/api/teams/"+team.ID+"/data-services/"+id, nil, 200, &d)
			if d.Service.Status == "running" {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("data service %s never ran: %+v", id, d)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	waitRunning(mysqlSvc.ID)
	waitRunning(pgSvc.ID)

	// A database and user on each — CreateInstall reads the first user's connection apart.
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/data-services/"+mysqlSvc.ID+"/databases", map[string]any{"name": "wp_content", "user": "wp_user"}, 201, nil)
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/data-services/"+pgSvc.ID+"/databases", map[string]any{"name": "odoo_db", "user": "odoo_user"}, 201, nil)

	wait := func(ref, want string) installJSON {
		t.Helper()
		var s installsJSON
		deadline := time.Now().Add(30 * time.Second)
		for {
			owner.mustJSON("GET", base+"/installs", nil, 200, &s)
			for _, b := range s.Installs {
				if (b.ID == ref || b.Name == ref) && b.Status == want {
					return b
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("install %s never became %s: %+v", ref, want, s.Installs)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	installOp := func(installID string) agentproto.SignedOp {
		t.Helper()
		for _, op := range agentOps(agent, agentproto.KindRecipeInstall) {
			var p agentproto.RecipeInstallParams
			_ = json.Unmarshal(op.Params, &p)
			if p.InstallID == installID {
				return op
			}
		}
		t.Fatalf("no recipe.install for %s", installID)
		return agentproto.SignedOp{}
	}
	installParams := func(installID string) agentproto.RecipeInstallParams {
		t.Helper()
		var p agentproto.RecipeInstallParams
		_ = json.Unmarshal(installOp(installID).Params, &p)
		return p
	}
	operationText := func(opID string) string {
		t.Helper()
		var params string
		var result sql.NullString
		if err := tenancy.Tx(ctx, sqlDB, tenancy.Scope{System: true}, func(_ *db.Queries, tx *sql.Tx) error {
			return tx.QueryRow(`SELECT params::text, result::text FROM operations WHERE id = $1`, opID).Scan(&params, &result)
		}); err != nil {
			t.Fatal(err)
		}
		return params + " " + result.String
	}

	t.Run("wordpress: a managed MySQL service is needed, another team's cannot be named", func(t *testing.T) {
		other.mustJSON("POST", base+"/installs", map[string]any{"recipe": "wordpress", "name": "wp"}, 404, nil) // not even a member
		owner.mustJSON("POST", base+"/installs", map[string]any{"recipe": "wordpress", "name": "wp"}, 422, nil) // no mysql given

		host := "wp-" + short + ".example.test"
		status, text := owner.do("POST", base+"/installs", map[string]any{"recipe": "wordpress", "name": "wp", "mysql": mysqlSvc.Name, "hostname": host})
		if status != 201 {
			t.Fatalf("create: %d %s", status, text)
		}
		var created installJSON
		_ = json.Unmarshal([]byte(text), &created)
		if created.Recipe != "wordpress" || created.Image != wordpress.Image || fmt.Sprint(created.EnvInjected) != "[WORDPRESS_URL]" {
			t.Fatalf("created: %+v", created)
		}
		wait(created.ID, "ready")

		p := installParams(created.ID)
		var wire map[string]any
		_ = json.Unmarshal(installOp(created.ID).Params, &wire)
		for k := range wire {
			switch k {
			case "install_id", "app_id", "recipe_id", "recipe_version", "image", "secrets":
			default:
				t.Errorf("recipe.install carries %q — the agent takes everything else from its own catalog", k)
			}
		}
		pt, err := agentproto.OpenBox(priv, agentproto.InstallSecretsContext(created.ID), p.Secrets)
		if err != nil {
			t.Fatalf("the agent's box does not open: %v", err)
		}
		var sec agentproto.InstallSecrets
		_ = json.Unmarshal(pt, &sec)
		host2, user, pass := sec.Env["WORDPRESS_DB_HOST"], sec.Env["WORDPRESS_DB_USER"], sec.Env["WORDPRESS_DB_PASSWORD"]
		if !strings.HasPrefix(host2, "172.17.0.1:") {
			t.Errorf("WORDPRESS_DB_HOST is host:port, the agent's DBProvision answer: %q", host2)
		}
		if sec.Env["WORDPRESS_DB_NAME"] != "wp_content" {
			t.Errorf("WORDPRESS_DB_NAME: %q", sec.Env["WORDPRESS_DB_NAME"])
		}
		if user != "wp_user" {
			t.Errorf("WORDPRESS_DB_USER: %q", user)
		}
		if !regexp.MustCompile(`^[A-Za-z0-9]{24}$`).MatchString(pass) {
			t.Fatalf("WORDPRESS_DB_PASSWORD: the database user's own generated password, 24 alnum: %q", pass)
		}
		if sec.Env["WORDPRESS_TABLE_PREFIX"] == "" || sec.Env["WORDPRESS_AUTH_KEY"] == "" {
			t.Errorf("the recipe's own generated secrets travel too: %+v", sec.Env)
		}
		// operations.params/result never carry the plaintext password, host, user or generated secrets —
		// only the sealed box (already asserted above to be the sole non-id field).
		text2 := operationText(created.Operation)
		for _, secret := range []string{pass, sec.Env["WORDPRESS_TABLE_PREFIX"], sec.Env["WORDPRESS_AUTH_KEY"]} {
			if strings.Contains(text2, secret) {
				t.Errorf("operations.params/result carries a plaintext secret: %q", secret)
			}
		}
		// Nothing db-shaped ever became a site variable: env_injected was already checked above.
		var env struct {
			Variables []struct{ Name string } `json:"variables"`
		}
		owner.mustJSON("GET", base+"/env", nil, 200, &env)
		for _, v := range env.Variables {
			if strings.HasPrefix(v.Name, "WORDPRESS_DB_") {
				t.Errorf("a database credential reached the site's variables: %s", v.Name)
			}
		}

		// An unknown service name is not found (still within the owning team).
		owner.mustJSON("POST", base+"/installs", map[string]any{"recipe": "wordpress", "name": "wp2", "mysql": "does-not-exist-" + short}, 404, nil)
	})

	t.Run("odoo: a managed Postgres service, split into host/port/user/password (no database part)", func(t *testing.T) {
		host := "odoo-" + short + ".example.test"
		status, text := owner.do("POST", base+"/installs", map[string]any{"recipe": "odoo", "name": "odoo", "postgres": pgSvc.Name, "hostname": host})
		if status != 201 {
			t.Fatalf("create: %d %s", status, text)
		}
		var created installJSON
		_ = json.Unmarshal([]byte(text), &created)
		if created.Recipe != "odoo" || created.Image != odoo.Image {
			t.Fatalf("created: %+v", created)
		}
		wait(created.ID, "ready")
		p := installParams(created.ID)
		pt, err := agentproto.OpenBox(priv, agentproto.InstallSecretsContext(created.ID), p.Secrets)
		if err != nil {
			t.Fatalf("the agent's box does not open: %v", err)
		}
		var sec agentproto.InstallSecrets
		_ = json.Unmarshal(pt, &sec)
		if sec.Env["HOST"] != "172.17.0.1" || sec.Env["PORT"] == "" {
			t.Errorf("HOST/PORT: %+v", sec.Env)
		}
		if sec.Env["USER"] != "odoo_user" {
			t.Errorf("USER: %q", sec.Env["USER"])
		}
		if !regexp.MustCompile(`^[A-Za-z0-9]{24}$`).MatchString(sec.Env["PASSWORD"]) {
			t.Fatalf("PASSWORD: %q", sec.Env["PASSWORD"])
		}
		text2 := operationText(created.Operation)
		if strings.Contains(text2, sec.Env["PASSWORD"]) {
			t.Error("operations.params/result carries the plaintext database password")
		}

		// Cross-team isolation: the other team's own app cannot use this team's Postgres service —
		// not found even by exact id, and it cannot see it in its own catalog install form either.
		var otherSrv struct{ ID string }
		otherSrv.ID, _ = enrolWithServicesKey(t, other, ts.URL, otherTeam.ID, "od-"+short, "203.0.113.62", nil)
		var otherApp struct {
			App struct{ ID string } `json:"app"`
		}
		other.mustJSON("POST", "/api/teams/"+otherTeam.ID+"/apps", map[string]any{"name": "od-" + short, "server": otherSrv.ID, "repo_url": "https://github.com/octocat/api.git"}, 201, &otherApp)
		other.mustJSON("POST", "/api/teams/"+otherTeam.ID+"/apps/"+otherApp.App.ID+"/installs",
			map[string]any{"recipe": "odoo", "name": "odoo", "postgres": pgSvc.ID}, 404, nil)
		other.mustJSON("POST", "/api/teams/"+otherTeam.ID+"/apps/"+otherApp.App.ID+"/installs",
			map[string]any{"recipe": "odoo", "name": "odoo", "postgres": pgSvc.Name}, 404, nil)
		other.mustJSON("GET", "/api/teams/"+otherTeam.ID+"/data-services/"+pgSvc.ID, nil, 404, nil)
	})
}
