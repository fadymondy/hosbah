package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/importer"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
The "Repository" section of a site: an app's GitHub repository read through the GitHub CLI on the
app's server. Every route and MCP tool queues the right typed, signed github.* read with validated
params (a fake agent verifies them exactly as cloudy-agent does); commits are marked against the
live release and the app's deployments; what an agent reports is cleaned before it is shown; an
imported app or a non-GitHub repository is a 422, a server not connected to GitHub a 409, another
team a 404.
*/
func TestAppRepository(t *testing.T) {
	ts, stop := bootTestServer(t)
	defer stop()
	sqlDB := testDB(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	short := suffix[len(suffix)-6:]

	const (
		shaA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" // live
		shaB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" // a failed deployment
		shaC = "cccccccccccccccccccccccccccccccccccccccc" // the branch head
	)

	owner := newClient(t, ts.URL)
	owner.register("repo-owner-" + suffix + "@example.test")
	var team struct{ ID string }
	owner.mustJSON("POST", "/api/teams", map[string]any{"name": "Repo " + suffix}, 201, &team)
	var tok struct{ Token string }
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/mcp-tokens", map[string]any{"name": "repo-read", "scopes": []string{"read"}, "expires_in_days": 30}, 201, &tok)

	viewerEmail := "repo-viewer-" + suffix + "@example.test"
	viewer := newClient(t, ts.URL)
	viewer.register(viewerEmail)
	var inv struct {
		AcceptURL string `json:"accept_url"`
	}
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/invitations", map[string]any{"email": viewerEmail, "role": "viewer"}, 201, &inv)
	viewer.mustJSON("POST", "/api/invitations/"+inv.AcceptURL[strings.LastIndex(inv.AcceptURL, "/")+1:]+"/accept", nil, 200, nil)

	stranger := newClient(t, ts.URL)
	stranger.register("repo-stranger-" + suffix + "@example.test")
	stranger.mustJSON("POST", "/api/teams", map[string]any{"name": "Elsewhere " + suffix}, 201, nil)

	srv, priv := enrolWithServicesKey(t, owner, ts.URL, team.ID, "repo-"+suffix, "10.9.8.6", nil)

	var (
		mu       sync.Mutex
		loggedIn = true
	)
	commit := func(sha, msg string) agentproto.GitHubCommit {
		return agentproto.GitHubCommit{SHA: sha, ShortSHA: sha[:7], Message: msg, AuthorName: "The Octocat", AuthorLogin: "octocat",
			AuthorAvatarURL: "https://avatars.githubusercontent.com/u/1?v=4", CommittedAt: time.Date(2026, 9, 2, 11, 0, 0, 0, time.UTC),
			URL: "https://github.com/octocat/api/commit/" + sha}
	}
	agent := startFakeAgent(t, ts.URL, srv, priv, func(op agentproto.SignedOp) (any, string) {
		switch op.Kind {
		case agentproto.KindGitHubStatus:
			mu.Lock()
			defer mu.Unlock()
			if !loggedIn {
				return agentproto.GitHubStatus{Installed: true, Version: "2.63.2"}, ""
			}
			return agentproto.GitHubStatus{Installed: true, Version: "2.63.2", LoggedIn: true, User: "octocat", Scopes: []string{"repo"}}, ""
		case agentproto.KindGitHubCommits:
			var p agentproto.GitHubCommitsParams
			if json.Unmarshal(op.Params, &p) != nil || p.Validate() != nil {
				return nil, "bad params"
			}
			// An agent is not trusted to shape links: one commit carries a hostile URL and avatar,
			// and one entry is not a commit at all.
			evil := commit(shaB, "Break the build")
			evil.URL, evil.AuthorAvatarURL = "javascript:alert(1)", "http://tracker.invalid/a.png"
			all := []agentproto.GitHubCommit{commit(shaC, "Add billing"), evil, commit(shaA, "First release"), {SHA: "not-a-sha", Message: "bogus"}}
			if p.Limit > 0 && p.Limit < len(all) {
				all = all[:p.Limit]
			}
			return agentproto.GitHubCommitsResult{Commits: all}, ""
		case agentproto.KindGitHubCompare:
			var p agentproto.GitHubCompareParams
			if json.Unmarshal(op.Params, &p) != nil || p.Validate() != nil {
				return nil, "bad params"
			}
			return agentproto.GitHubCompareResult{AheadBy: 2, BehindBy: 0, TotalCommits: 2,
				Commits: []agentproto.GitHubCommit{commit(shaC, "Add billing"), commit(shaB, "Break the build")},
				Files:   []agentproto.GitHubFile{{Filename: "billing/plan.go", Status: "added", Additions: 40}, {Filename: "main.go", Status: "modified", Additions: 2, Deletions: 1}}}, ""
		case agentproto.KindGitHubPulls:
			var p agentproto.GitHubPullsParams
			if json.Unmarshal(op.Params, &p) != nil || p.Validate() != nil {
				return nil, "bad params"
			}
			return agentproto.GitHubPullsResult{Pulls: []agentproto.GitHubPull{{Number: 12, Title: "Add invoices", AuthorLogin: "hubot", HeadRef: "feat/invoices",
				BaseRef: "main", Draft: true, URL: "javascript:alert(2)", UpdatedAt: time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)}}}, ""
		}
		return nil, "this fake agent does not run " + op.Kind
	})
	lastParams := func(kind string) string {
		ops := agentOps(agent, kind)
		if len(ops) == 0 {
			return ""
		}
		return string(ops[len(ops)-1].Params)
	}
	// githubOpCount: the github.* operations the agent has received (a queued deploy's own
	// operations arrive whenever the worker gets to them, and are not what the refusals are about).
	githubOpCount := func() int {
		n := 0
		for _, k := range agent.kinds() {
			if strings.HasPrefix(k, "github.") {
				n++
			}
		}
		return n
	}
	refresh := func(connected bool) {
		t.Helper()
		mu.Lock()
		loggedIn = connected
		mu.Unlock()
		var out struct {
			GitHub struct {
				Connected bool `json:"connected"`
			} `json:"github"`
		}
		owner.mustJSON("GET", "/api/teams/"+team.ID+"/servers/"+srv+"/github?refresh=true", nil, 200, &out)
		if out.GitHub.Connected != connected {
			t.Fatalf("github connected = %v, want %v", out.GitHub.Connected, connected)
		}
	}
	refresh(true)

	var app struct {
		App struct{ ID, Name string } `json:"app"`
	}
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/apps", map[string]any{"name": "repo-" + short, "server": srv,
		"repo_url": "https://github.com/octocat/api.git", "branch": "main"}, 201, &app)
	var other struct {
		App struct{ ID, Name string } `json:"app"`
	}
	owner.mustJSON("POST", "/api/teams/"+team.ID+"/apps", map[string]any{"name": "lab-" + short, "server": srv,
		"repo_url": "https://gitlab.com/octocat/api"}, 201, &other)
	base := "/api/teams/" + team.ID + "/apps/" + app.App.ID + "/repository"

	type commitOut struct {
		SHA             string `json:"sha"`
		ShortSHA        string `json:"short_sha"`
		Message         string `json:"message"`
		AuthorLogin     string `json:"author_login"`
		AuthorAvatarURL string `json:"author_avatar_url"`
		URL             string `json:"url"`
		IsLive          bool   `json:"is_live"`
		Deployed        *struct {
			Number int64  `json:"number"`
			Status string `json:"status"`
		} `json:"deployed"`
	}
	type repositoryOut struct {
		Repo struct{ Owner, Name, URL, Branch string } `json:"repo"`
		Live *struct {
			SHA     *string `json:"sha"`
			Version int64   `json:"version"`
		} `json:"live"`
		Head *struct {
			SHA string `json:"sha"`
		} `json:"head"`
		AheadBy *int `json:"ahead_by"`
	}

	t.Run("nothing live yet", func(t *testing.T) {
		var r repositoryOut
		viewer.mustJSON("GET", base, nil, 200, &r)
		if r.Repo.Owner != "octocat" || r.Repo.Name != "api" || r.Repo.URL != "https://github.com/octocat/api" || r.Repo.Branch != "main" {
			t.Errorf("repo: %+v", r.Repo)
		}
		if r.Live != nil || r.Head == nil || r.Head.SHA != shaC || r.AheadBy != nil {
			t.Errorf("before a release: %+v", r)
		}
		if got := lastParams(agentproto.KindGitHubCommits); got != `{"repo":"octocat/api","branch":"main","limit":1}` {
			t.Errorf("head lookup params: %s", got)
		}
		if len(agentOps(agent, agentproto.KindGitHubCompare)) != 0 {
			t.Error("nothing is live, so nothing should have been compared")
		}
		if status, text := owner.do("GET", base+"/compare", nil); status != 422 || !strings.Contains(text, "nothing is live") {
			t.Errorf("compare without a base and nothing live -> %d %s", status, text)
		}
	})

	// What Hosbah deployed: #1 built shaA and is live (v1); #2 built shaB and failed.
	err := tenancy.Tx(context.Background(), sqlDB, tenancy.Scope{System: true, TeamID: team.ID}, func(q *db.Queries, tx *sql.Tx) error {
		for n, d := range []struct{ sha, status string }{{shaA, "succeeded"}, {shaB, "failed"}} {
			if _, err := tx.Exec(`INSERT INTO deployments (id, team_id, app_id, server_id, number, ref, commit_sha, trigger, kind, status, created_by, finished_at)
				VALUES ($7, $1, $2, $3, $4, 'main', $5, 'manual', 'deploy', $6, 'test', now())`, team.ID, app.App.ID, srv, n+1, d.sha, d.status, fmt.Sprintf("dep%d-%s", n+1, suffix)); err != nil {
				return err
			}
		}
		sha := shaA
		_, err := q.InsertRelease(context.Background(), db.InsertReleaseParams{ID: "rel-" + suffix, TeamID: team.ID, AppID: app.App.ID,
			DeploymentID: "dep1-" + suffix, Image: "cloudy/repo:1", CommitSha: &sha, Port: 8080, Config: "{}"})
		return err
	})
	if err != nil {
		t.Fatalf("seeding deployments and a live release: %v", err)
	}

	t.Run("deploying a chosen commit is a deployment of its sha", func(t *testing.T) {
		var dep struct {
			Number int64  `json:"number"`
			Ref    string `json:"ref"`
		}
		owner.mustJSON("POST", "/api/teams/"+team.ID+"/apps/"+app.App.ID+"/deployments", map[string]any{"ref": shaC}, 201, &dep)
		if dep.Ref != shaC || dep.Number != 3 {
			t.Fatalf("deployment: %+v", dep)
		}
		owner.expect("POST", "/api/teams/"+team.ID+"/apps/"+app.App.ID+"/deployments", map[string]any{"ref": "--upload-pack=x"}, 422)
		owner.expect("POST", "/api/teams/"+team.ID+"/apps/"+app.App.ID+"/deployments", map[string]any{"ref": shaC + "..main"}, 422)
	})

	t.Run("commits are marked live and deployed", func(t *testing.T) {
		var out struct {
			Branch  string      `json:"branch"`
			LiveSHA *string     `json:"live_sha"`
			Commits []commitOut `json:"commits"`
		}
		viewer.mustJSON("GET", base+"/commits", nil, 200, &out)
		if got := lastParams(agentproto.KindGitHubCommits); got != `{"repo":"octocat/api","branch":"main","limit":30}` {
			t.Errorf("github.commits params: %s", got)
		}
		if out.Branch != "main" || out.LiveSHA == nil || *out.LiveSHA != shaA || len(out.Commits) != 3 {
			t.Fatalf("commits: %+v", out)
		}
		c, b, a := out.Commits[0], out.Commits[1], out.Commits[2]
		if c.SHA != shaC || c.ShortSHA != "ccccccc" || c.Message != "Add billing" || c.AuthorLogin != "octocat" || c.IsLive ||
			c.Deployed == nil || c.Deployed.Number != 3 || c.Deployed.Status == "" {
			t.Errorf("head commit (asked for by its sha in deployment #3): %+v %+v", c, c.Deployed)
		}
		if b.SHA != shaB || b.IsLive || b.Deployed == nil || b.Deployed.Number != 2 || b.Deployed.Status != "failed" {
			t.Errorf("failed commit: %+v %+v", b, b.Deployed)
		}
		if a.SHA != shaA || !a.IsLive || a.Deployed == nil || a.Deployed.Number != 1 || a.Deployed.Status != "succeeded" {
			t.Errorf("live commit: %+v %+v", a, a.Deployed)
		}
		if b.URL != "https://github.com/octocat/api/commit/"+shaB || b.AuthorAvatarURL != "" || !strings.HasPrefix(c.AuthorAvatarURL, "https://") {
			t.Errorf("links must be rebuilt, not trusted: %q %q", b.URL, b.AuthorAvatarURL)
		}

		owner.mustJSON("GET", base+"/commits?branch=release/1.x&limit=2", nil, 200, &out)
		if got := lastParams(agentproto.KindGitHubCommits); got != `{"repo":"octocat/api","branch":"release/1.x","limit":2}` {
			t.Errorf("github.commits params: %s", got)
		}
		if out.Branch != "release/1.x" || len(out.Commits) != 2 {
			t.Errorf("a branch and a limit: %+v", out)
		}

		before := len(agentOps(agent, agentproto.KindGitHubCommits))
		for _, q := range []string{"?branch=-x", "?branch=a..b", "?branch=main%26per_page%3D1000", "?branch=a%20b", "?branch=x%3Fy", "?branch=%7Bbranch%7D",
			"?limit=101", "?limit=0", "?branch=" + strings.Repeat("a", 201)} {
			owner.expect("GET", base+"/commits"+q, nil, 422)
		}
		if n := len(agentOps(agent, agentproto.KindGitHubCommits)); n != before {
			t.Errorf("refused arguments reached the agent %d time(s)", n-before)
		}
	})

	t.Run("repository: live, head and ahead_by", func(t *testing.T) {
		var r repositoryOut
		viewer.mustJSON("GET", base, nil, 200, &r)
		if r.Live == nil || r.Live.SHA == nil || *r.Live.SHA != shaA || r.Live.Version != 1 || r.Head == nil || r.Head.SHA != shaC || r.AheadBy == nil || *r.AheadBy != 2 {
			t.Errorf("repository: %+v", r)
		}
		if got := lastParams(agentproto.KindGitHubCompare); got != `{"repo":"octocat/api","base":"`+shaA+`","head":"`+shaC+`"}` {
			t.Errorf("github.compare params: %s", got)
		}
		// By name too.
		owner.mustJSON("GET", "/api/teams/"+team.ID+"/apps/"+app.App.Name+"/repository", nil, 200, &r)
	})

	t.Run("compare", func(t *testing.T) {
		var out struct {
			Base, Head   string
			AheadBy      int         `json:"ahead_by"`
			BehindBy     int         `json:"behind_by"`
			TotalCommits int         `json:"total_commits"`
			Commits      []commitOut `json:"commits"`
			Files        []struct {
				Filename, Status     string
				Additions, Deletions int
			} `json:"files"`
			Truncated bool `json:"truncated"`
		}
		_, text := viewer.do("GET", base+"/compare", nil)
		viewer.mustJSON("GET", base+"/compare", nil, 200, &out)
		if got := lastParams(agentproto.KindGitHubCompare); got != `{"repo":"octocat/api","base":"`+shaA+`","head":"main"}` {
			t.Errorf("default comparison params: %s", got)
		}
		if out.Base != shaA || out.Head != "main" || out.AheadBy != 2 || out.BehindBy != 0 || out.TotalCommits != 2 || len(out.Commits) != 2 ||
			out.Commits[0].SHA != shaC || out.Commits[1].Deployed == nil || out.Commits[1].Deployed.Number != 2 || out.Truncated {
			t.Errorf("compare: %+v", out)
		}
		if len(out.Files) != 2 || out.Files[0].Filename != "billing/plan.go" || out.Files[0].Status != "added" || out.Files[0].Additions != 40 || out.Files[1].Deletions != 1 {
			t.Errorf("files: %+v", out.Files)
		}
		if strings.Contains(text, `"patch"`) {
			t.Errorf("a comparison never carries patches: %s", text)
		}
		owner.mustJSON("GET", base+"/compare?base=v1.0&head="+shaB[:7], nil, 200, &out)
		if got := lastParams(agentproto.KindGitHubCompare); got != `{"repo":"octocat/api","base":"v1.0","head":"bbbbbbb"}` {
			t.Errorf("explicit comparison params: %s", got)
		}
		before := len(agentOps(agent, agentproto.KindGitHubCompare))
		for _, q := range []string{"?base=a...b", "?head=-x", "?base=main&head=x%3Fy", "?base=a%23b", "?head=x/", "?base=" + strings.Repeat("a", 201)} {
			owner.expect("GET", base+"/compare"+q, nil, 422)
		}
		if n := len(agentOps(agent, agentproto.KindGitHubCompare)); n != before {
			t.Errorf("refused arguments reached the agent %d time(s)", n-before)
		}
	})

	t.Run("pull requests", func(t *testing.T) {
		var out struct {
			Pulls []struct {
				Number      int    `json:"number"`
				Title       string `json:"title"`
				AuthorLogin string `json:"author_login"`
				HeadRef     string `json:"head_ref"`
				BaseRef     string `json:"base_ref"`
				Draft       bool   `json:"draft"`
				URL         string `json:"url"`
				UpdatedAt   string `json:"updated_at"`
			} `json:"pulls"`
		}
		viewer.mustJSON("GET", base+"/pulls", nil, 200, &out)
		if got := lastParams(agentproto.KindGitHubPulls); got != `{"repo":"octocat/api","limit":50}` {
			t.Errorf("github.pulls params: %s", got)
		}
		if len(out.Pulls) != 1 {
			t.Fatalf("pulls: %+v", out)
		}
		if p := out.Pulls[0]; p.Number != 12 || p.Title != "Add invoices" || p.AuthorLogin != "hubot" || p.HeadRef != "feat/invoices" || p.BaseRef != "main" ||
			!p.Draft || p.URL != "https://github.com/octocat/api/pull/12" || !strings.HasPrefix(p.UpdatedAt, "2026-09-10") {
			t.Errorf("pull: %+v", p)
		}
	})

	if _, err := importer.Sync(context.Background(), sqlDB, team.ID, srv, []agentproto.Service{{Kind: "container", Name: "imp-repo", Image: "nginx", Status: "running"}}); err != nil {
		t.Fatalf("import: %v", err)
	}

	t.Run("refusals", func(t *testing.T) {
		before := githubOpCount()
		for _, suffix := range []string{"", "/commits", "/compare?base=main", "/pulls"} {
			if status, text := owner.do("GET", "/api/teams/"+team.ID+"/apps/imp-repo/repository"+suffix, nil); status != 422 || !strings.Contains(text, "imported") {
				t.Errorf("an imported app %s -> %d %s", suffix, status, text)
			}
			if status, text := owner.do("GET", "/api/teams/"+team.ID+"/apps/"+other.App.ID+"/repository"+suffix, nil); status != 422 || !strings.Contains(text, "github.com") {
				t.Errorf("a GitLab repository %s -> %d %s", suffix, status, text)
			}
			owner.expect("GET", "/api/teams/"+team.ID+"/apps/no-such-app/repository"+suffix, nil, 404)
			stranger.expect("GET", base+suffix, nil, 404)
		}
		if n := githubOpCount(); n != before {
			t.Errorf("a refusal reached the agent (%d github operations)", n-before)
		}
	})

	t.Run("mcp", func(t *testing.T) {
		r := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "get_app_repository", map[string]any{"app": app.App.Name})
		if r.IsError || !strings.Contains(r.Text, "octocat/api") || !strings.Contains(r.Text, "live: v1  commit "+shaA) ||
			!strings.Contains(r.Text, "head: "+shaC) || !strings.Contains(r.Text, "2 commits ahead") {
			t.Errorf("get_app_repository: %+v", r)
		}
		r = mcpCall(t, ts.URL, "/api/mcp", tok.Token, "list_app_commits", map[string]any{"app": app.App.ID, "limit": 3})
		if r.IsError || !strings.Contains(r.Text, shaA) || !strings.Contains(r.Text, "[live]") || !strings.Contains(r.Text, "[deployment #2 failed]") ||
			strings.Contains(r.Text, "javascript:") {
			t.Errorf("list_app_commits: %+v", r)
		}
		if got := lastParams(agentproto.KindGitHubCommits); got != `{"repo":"octocat/api","branch":"main","limit":3}` {
			t.Errorf("list_app_commits params: %s", got)
		}
		r = mcpCall(t, ts.URL, "/api/mcp", tok.Token, "compare_app_commits", map[string]any{"app": app.App.ID, "head": "dev"})
		if r.IsError || !strings.Contains(r.Text, "2 ahead, 0 behind") || !strings.Contains(r.Text, "billing/plan.go") {
			t.Errorf("compare_app_commits: %+v", r)
		}
		if got := lastParams(agentproto.KindGitHubCompare); got != `{"repo":"octocat/api","base":"`+shaA+`","head":"dev"}` {
			t.Errorf("compare_app_commits params: %s", got)
		}
		before := githubOpCount()
		for name, args := range map[string]map[string]any{
			"list_app_commits":    {"app": app.App.ID, "branch": "main&per_page=1000"},
			"compare_app_commits": {"app": app.App.ID, "base": "--help"},
			"get_app_repository":  {"app": "imp-repo"},
		} {
			if r := mcpCall(t, ts.URL, "/api/mcp", tok.Token, name, args); !r.IsError {
				t.Errorf("%s %v should be refused: %+v", name, args, r)
			}
		}
		if r := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "list_app_commits", map[string]any{"app": other.App.ID}); !r.IsError || !strings.Contains(r.Text, "github.com") {
			t.Errorf("list_app_commits of a GitLab app: %+v", r)
		}
		if n := githubOpCount(); n != before {
			t.Errorf("a refused MCP call reached the agent (%d github operations)", n-before)
		}
	})

	t.Run("not connected", func(t *testing.T) {
		refresh(false)
		before := githubOpCount()
		for _, suffix := range []string{"", "/commits", "/compare", "/pulls"} {
			if status, text := viewer.do("GET", base+suffix, nil); status != 409 || !strings.Contains(text, "not connected") {
				t.Errorf("GET …/repository%s when GitHub is not connected -> %d %s", suffix, status, text)
			}
		}
		for _, name := range []string{"get_app_repository", "list_app_commits", "compare_app_commits"} {
			if r := mcpCall(t, ts.URL, "/api/mcp", tok.Token, name, map[string]any{"app": app.App.ID}); !r.IsError || !strings.Contains(r.Text, "not connected") {
				t.Errorf("%s when GitHub is not connected: %+v", name, r)
			}
		}
		if n := githubOpCount(); n != before {
			t.Errorf("a not-connected refusal reached the agent (%d github operations)", n-before)
		}
	})

	if rej := agent.rejectedList(); len(rej) > 0 {
		t.Errorf("the agent refused operations: %v", rej)
	}
}
