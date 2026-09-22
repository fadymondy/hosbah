package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// GitHubTools: a server's own GitHub connection through the GitHub CLI on it — list the
// repositories and branches its account sees, and let deploys clone private repositories.
// Over MCP only the token mode connects (the device flow needs a human at github.com).
func GitHubTools() []Tool {
	serverArg := str("Server name or id")
	return []Tool{
		{
			Name: "github_status", Scope: scopes.Read,
			Description: "A server's GitHub connection (the GitHub CLI on it): installed, connected, account, scopes. " +
				"refresh=true asks the server now (owner, admin or deployer).",
			InputSchema: schema([]string{"server"}, map[string]any{"server": serverArg,
				"refresh": map[string]any{"type": "boolean", "description": "Ask the server now instead of the last report"}}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				srv, err := findServer(ctx, env, args)
				if err != nil {
					return "", err
				}
				if !args.Bool("refresh") {
					return githubStateText(srv), nil
				}
				if err := needs(ctx, env, tenancy.AbilityOperate); err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				op, err := s.GitHubRefresh(ctx, env.Q, env.Caller.TeamID, mcpActor(env), srv)
				if err != nil {
					return "", err
				}
				env.Then = func(ctx context.Context) (string, error) {
					var st agentproto.GitHubStatus
					if _, err := s.AwaitResult(ctx, op, 20*time.Second, &st); err != nil {
						if errors.Is(err, deploy.ErrStillRunning) {
							return fmt.Sprintf("The server has not answered yet (operation %s).", op.ID), nil
						}
						return "", err
					}
					return statusText(srv.Name, st), nil
				}
				return "", nil
			},
		},
		{
			Name: "github_connect", Scope: scopes.Write,
			Description: "Connect a server to GitHub with a token (a personal access token with the repo and read:org scopes): the " +
				"GitHub CLI is installed if missing and logged in, so github_repos works and deploys clone private repositories. " +
				"The token is sealed to the server at once and never echoed or stored in the clear. Owner or admin. " +
				"(The device-code login is for people, in the web app.)",
			InputSchema: schema([]string{"server", "token"}, map[string]any{"server": serverArg, "token": str("GitHub token")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				srv, err := findServer(ctx, env, args)
				if err != nil {
					return "", err
				}
				op, err := s.GitHubConnect(ctx, env.Q, env.Caller.TeamID, mcpActor(env), srv, agentproto.GitHubModeToken, args.Str("token"))
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Connecting %s to GitHub (operation %s). Follow it with github_status refresh=true.\n", srv.Name, op.ID), nil
			},
		},
		{
			Name: "github_disconnect", Scope: scopes.Write,
			Description: "Log a server's GitHub CLI out and remove its git credential helper. Deploys of private repositories on it stop working. Owner or admin.",
			InputSchema: schema([]string{"server"}, map[string]any{"server": serverArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageServers); err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				srv, err := findServer(ctx, env, args)
				if err != nil {
					return "", err
				}
				op, err := s.GitHubDisconnect(ctx, env.Q, env.Caller.TeamID, mcpActor(env), srv)
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Disconnecting %s from GitHub (operation %s).\n", srv.Name, op.ID), nil
			},
		},
		{
			Name: "github_repos", Scope: scopes.Read,
			Description: "Repositories a server's GitHub account can see — its own and its organisations', or one owner's — newest first, " +
				"with visibility and default branch. Use the URL as repo_url for create_app. Owner, admin or deployer.",
			InputSchema: schema([]string{"server"}, map[string]any{"server": serverArg, "owner": str("A user or organisation (optional)"),
				"limit": map[string]any{"type": "integer", "default": 200, "maximum": 1000}}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				return githubRead(ctx, env, args, func(s *deploy.Service, srv db.Server) (db.Operation, error) {
					return s.GitHubRepos(ctx, env.Q, env.Caller.TeamID, mcpActor(env), srv, args.Str("owner"), args.Int("limit", 200, 1, 1000))
				}, func(s *deploy.Service, op db.Operation) (string, error) {
					var r agentproto.GitHubReposResult
					if _, err := s.AwaitResult(ctx, op, 30*time.Second, &r); err != nil {
						return "", err
					}
					if len(r.Repos) == 0 {
						return "No repositories.", nil
					}
					var b strings.Builder
					for _, repo := range r.Repos {
						vis := "public"
						if repo.IsPrivate {
							vis = "private"
						}
						fmt.Fprintf(&b, "  %-45s %-8s %-12s %s\n", repo.NameWithOwner, vis, orDash(&repo.DefaultBranch), repo.URL)
					}
					if r.Truncated {
						b.WriteString("  (more exist: raise limit or pass owner)\n")
					}
					return b.String(), nil
				})
			},
		},
		{
			Name: "github_branches", Scope: scopes.Read,
			Description: "A repository's branches and default branch, read through a server's GitHub CLI. Owner, admin or deployer.",
			InputSchema: schema([]string{"server", "repo"}, map[string]any{"server": serverArg, "repo": str("owner/name")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				return githubRead(ctx, env, args, func(s *deploy.Service, srv db.Server) (db.Operation, error) {
					return s.GitHubBranches(ctx, env.Q, env.Caller.TeamID, mcpActor(env), srv, args.Str("repo"))
				}, func(s *deploy.Service, op db.Operation) (string, error) {
					var r agentproto.GitHubBranchesResult
					if _, err := s.AwaitResult(ctx, op, 30*time.Second, &r); err != nil {
						return "", err
					}
					return fmt.Sprintf("default: %s\nbranches: %s\n", r.Default, strings.Join(r.Branches, ", ")), nil
				})
			},
		},
	}
}

// RepositoryTools: the "Repository" section of an app — its GitHub repository read through the
// GitHub CLI connected on the app's server, set against what is live and what was deployed.
func RepositoryTools() []Tool {
	appArg := str("App name or id")
	return []Tool{
		{
			Name: "get_app_repository", Scope: scopes.Read,
			Description: "An app's GitHub repository: owner/name, branch, the live release's commit, the branch's newest commit and how many " +
				"commits the branch is ahead of what is live. Read through the GitHub CLI on the app's server (it must be connected to GitHub).",
			InputSchema: schema([]string{"app"}, map[string]any{"app": appArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				return repoRead(ctx, env, args, func(s *deploy.Service, rc deploy.RepoContext) (func(context.Context) (string, error), error) {
					actor := mcpActor(env)
					headOp, err := s.GitHubCommits(ctx, env.Q, env.Caller.TeamID, actor, rc.Server, rc.Repo.Slug(), rc.Repo.Branch, 1)
					if err != nil {
						return nil, err
					}
					var cmpOp *db.Operation
					if rc.LiveSHA != "" {
						op, err := s.GitHubCompare(ctx, env.Q, env.Caller.TeamID, actor, rc.Server, rc.Repo.Slug(), rc.LiveSHA, rc.Repo.Branch)
						if err != nil {
							return nil, err
						}
						cmpOp = &op
					}
					return func(ctx context.Context) (string, error) {
						var b strings.Builder
						fmt.Fprintf(&b, "repository: %s  (%s)\nbranch: %s\n", rc.Repo.Slug(), rc.Repo.URL, rc.Repo.Branch)
						switch {
						case !rc.HasLive:
							b.WriteString("live: nothing yet\n")
						case rc.LiveSHA == "":
							fmt.Fprintf(&b, "live: v%d  commit unknown\n", rc.LiveVersion)
						default:
							fmt.Fprintf(&b, "live: v%d  commit %s\n", rc.LiveVersion, rc.LiveSHA)
						}
						var head agentproto.GitHubCommitsResult
						if _, err := s.AwaitResult(ctx, headOp, 20*time.Second, &head); err != nil {
							if errors.Is(err, deploy.ErrStillRunning) {
								fmt.Fprintf(&b, "head: the server has not answered yet (operation %s)\n", headOp.ID)
								return b.String(), nil
							}
							return "", err
						}
						if cs := deploy.CleanCommits(rc.Repo, head.Commits); len(cs) > 0 {
							fmt.Fprintf(&b, "head: %s  %s\n", cs[0].SHA, cs[0].Message)
						}
						if cmpOp != nil {
							var cmp agentproto.GitHubCompareResult
							if _, err := s.AwaitResult(ctx, *cmpOp, 20*time.Second, &cmp); err == nil {
								fmt.Fprintf(&b, "the branch is %d commits ahead of what is live, %d behind\n", cmp.AheadBy, cmp.BehindBy)
							}
						}
						return b.String(), nil
					}, nil
				})
			},
		},
		{
			Name: "list_app_commits", Scope: scopes.Read,
			Description: "The newest commits of an app's branch (or another branch, tag or commit), newest first, marking the live one and the " +
				"newest deployment of each. Deploy one with deploy_app ref=<its full sha>.",
			InputSchema: schema([]string{"app"}, map[string]any{"app": appArg, "branch": str("Branch, tag or commit (default: the app's branch)"),
				"limit": map[string]any{"type": "integer", "default": 30, "maximum": 100}}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				return repoRead(ctx, env, args, func(s *deploy.Service, rc deploy.RepoContext) (func(context.Context) (string, error), error) {
					branch := strings.TrimSpace(args.Str("branch"))
					if branch == "" {
						branch = rc.Repo.Branch
					}
					byCommit, err := deploy.DeploymentsByCommit(ctx, env.Q, env.Caller.TeamID, rc.App)
					if err != nil {
						return nil, err
					}
					op, err := s.GitHubCommits(ctx, env.Q, env.Caller.TeamID, mcpActor(env), rc.Server, rc.Repo.Slug(), branch, args.Int("limit", 30, 1, 100))
					if err != nil {
						return nil, err
					}
					return func(ctx context.Context) (string, error) {
						var r agentproto.GitHubCommitsResult
						if _, err := s.AwaitResult(ctx, op, 30*time.Second, &r); err != nil {
							return "", err
						}
						return fmt.Sprintf("%s @ %s\n", rc.Repo.Slug(), branch) + commitLines(rc, byCommit, r.Commits), nil
					}, nil
				})
			},
		},
		{
			Name: "compare_app_commits", Scope: scopes.Read,
			Description: "Compare base...head in an app's repository: ahead/behind, the commits and the changed files (no patches) — what " +
				"deploying head would change. Defaults: base = the live release's commit, head = the app's branch.",
			InputSchema: schema([]string{"app"}, map[string]any{"app": appArg, "base": str("Branch, tag or commit (default: the live commit)"),
				"head": str("Branch, tag or commit (default: the app's branch)")}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				return repoRead(ctx, env, args, func(s *deploy.Service, rc deploy.RepoContext) (func(context.Context) (string, error), error) {
					base, head := strings.TrimSpace(args.Str("base")), strings.TrimSpace(args.Str("head"))
					if base == "" {
						base = rc.LiveSHA
					}
					if base == "" {
						return nil, errors.New("nothing is live yet, so there is no default base — give base (a branch, tag or commit)")
					}
					if head == "" {
						head = rc.Repo.Branch
					}
					byCommit, err := deploy.DeploymentsByCommit(ctx, env.Q, env.Caller.TeamID, rc.App)
					if err != nil {
						return nil, err
					}
					op, err := s.GitHubCompare(ctx, env.Q, env.Caller.TeamID, mcpActor(env), rc.Server, rc.Repo.Slug(), base, head)
					if err != nil {
						return nil, err
					}
					return func(ctx context.Context) (string, error) {
						var r agentproto.GitHubCompareResult
						if _, err := s.AwaitResult(ctx, op, 30*time.Second, &r); err != nil {
							return "", err
						}
						var b strings.Builder
						fmt.Fprintf(&b, "%s  %s...%s: %d ahead, %d behind, %d commits\n", rc.Repo.Slug(), base, head, r.AheadBy, r.BehindBy, r.TotalCommits)
						b.WriteString(commitLines(rc, byCommit, r.Commits))
						files, cut := deploy.CleanFiles(r.Files)
						fmt.Fprintf(&b, "files (%d):\n", len(files))
						for _, f := range files {
							fmt.Fprintf(&b, "  %-9s +%d -%d  %s\n", f.Status, f.Additions, f.Deletions, f.Filename)
						}
						if r.Truncated || cut {
							b.WriteString("  (more commits or files exist than are listed)\n")
						}
						return b.String(), nil
					}, nil
				})
			},
		},
	}
}

func commitLines(rc deploy.RepoContext, byCommit map[string]db.Deployment, in []agentproto.GitHubCommit) string {
	commits := deploy.CleanCommits(rc.Repo, in)
	if len(commits) == 0 {
		return "  no commits\n"
	}
	var b strings.Builder
	for _, c := range commits {
		mark := ""
		if rc.LiveSHA != "" && deploy.SameCommit(c.SHA, rc.LiveSHA) {
			mark = "  [live]"
		}
		if d, ok := byCommit[c.SHA]; ok {
			mark += fmt.Sprintf("  [deployment #%d %s]", d.Number, d.Status)
		}
		who := c.AuthorLogin
		if who == "" {
			who = c.AuthorName
		}
		fmt.Fprintf(&b, "  %s  %s  %s  %s%s\n", c.SHA, c.CommittedAt.UTC().Format("2006-01-02 15:04"), who, c.Message, mark)
	}
	return b.String()
}

// repoRead resolves the app and its repository, queues the read(s), and answers after commit.
func repoRead(ctx context.Context, env *Env, args Args,
	queue func(*deploy.Service, deploy.RepoContext) (func(context.Context) (string, error), error)) (string, error) {
	a, err := findApp(ctx, env, args)
	if err != nil {
		return "", err
	}
	s, err := deploys(env)
	if err != nil {
		return "", err
	}
	rc, err := s.RepositoryOf(ctx, env.Q, env.Caller.TeamID, a)
	if err != nil {
		return "", err
	}
	answer, err := queue(s, rc)
	if err != nil {
		return "", err
	}
	env.Then = func(ctx context.Context) (string, error) {
		out, err := answer(ctx)
		if errors.Is(err, deploy.ErrStillRunning) {
			return "The server has not answered yet — is its agent connected?", nil
		}
		return out, err
	}
	return "", nil
}

// githubRead queues a read-only github.* operation and answers once the server has (after commit).
func githubRead(ctx context.Context, env *Env, args Args, queue func(*deploy.Service, db.Server) (db.Operation, error),
	answer func(*deploy.Service, db.Operation) (string, error)) (string, error) {
	if err := needs(ctx, env, tenancy.AbilityOperate); err != nil {
		return "", err
	}
	s, err := deploys(env)
	if err != nil {
		return "", err
	}
	srv, err := findServer(ctx, env, args)
	if err != nil {
		return "", err
	}
	st := deploy.GitHubStateOf(srv, time.Now())
	if st.CheckedAt != nil && !st.Connected {
		return "", errors.New("GitHub is not connected on " + srv.Name + " — connect it first (github_connect, or the web app)")
	}
	op, err := queue(s, srv)
	if err != nil {
		return "", err
	}
	env.Then = func(context.Context) (string, error) {
		out, err := answer(s, op)
		if errors.Is(err, deploy.ErrStillRunning) {
			return fmt.Sprintf("The server has not answered yet (operation %s).", op.ID), nil
		}
		return out, err
	}
	return "", nil
}

func statusText(name string, st agentproto.GitHubStatus) string {
	switch {
	case !st.Installed:
		return name + ": the GitHub CLI is not installed (github_connect installs it)."
	case !st.LoggedIn:
		return fmt.Sprintf("%s: gh %s installed, not connected to GitHub.", name, st.Version)
	}
	return fmt.Sprintf("%s: connected to GitHub as %s (gh %s, scopes %s).", name, st.User, st.Version, strings.Join(st.Scopes, ", "))
}

func githubStateText(srv db.Server) string {
	st := deploy.GitHubStateOf(srv, time.Now())
	if st.CheckedAt == nil {
		return srv.Name + ": no GitHub connection reported yet (github_status refresh=true asks the server)."
	}
	text := statusText(srv.Name, agentproto.GitHubStatus{Installed: st.Installed, Version: st.Version, LoggedIn: st.Connected,
		User: st.User, Scopes: st.Scopes})
	text += " Checked " + st.CheckedAt.UTC().Format(time.RFC3339) + "."
	if st.Pending != nil {
		text += fmt.Sprintf(" A login is waiting: enter %s at %s.", st.Pending.UserCode, st.Pending.VerificationURI)
	}
	if st.Error != "" {
		text += " Last error: " + st.Error
	}
	return text
}

// GitHubAppTools: the team's GitHub App installations and the repositories they reach. Read-only:
// installing the App takes a person at github.com (the web app's team settings).
func GitHubAppTools() []Tool {
	return []Tool{
		{
			Name: "github_app_status", Scope: scopes.Read,
			Description: "The GitHub App: whether this platform has it configured, and the GitHub accounts (users or organisations) it is " +
				"installed on for this team, with their repository access. Deploys of those accounts' repositories clone with a fresh " +
				"installation token, and their pushes deploy automatically. Installing it is done in the web app (team settings).",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, env *Env, _ Args) (string, error) {
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				g := s.GitHub()
				rows, err := env.Q.GitHubInstallationsOfTeam(ctx, env.Caller.TeamID)
				if err != nil {
					return "", err
				}
				var b strings.Builder
				if g.Configured() {
					fmt.Fprintf(&b, "GitHub App: configured (%s)\n", orDash(ptrStr(g.Slug())))
				} else {
					b.WriteString("GitHub App: not configured on this platform\n")
				}
				if len(rows) == 0 {
					b.WriteString("No installations for this team.\n")
					return b.String(), nil
				}
				for _, in := range rows {
					state := "active"
					if in.SuspendedAt != nil {
						state = "suspended"
					}
					fmt.Fprintf(&b, "  %-30s %-12s repositories: %-8s %s (installation %d)\n", in.AccountLogin, in.AccountType, in.RepositorySelection, state, in.InstallationID)
				}
				return b.String(), nil
			},
		},
		{
			Name: "github_app_repos", Scope: scopes.Read,
			Description: "Repositories the team's GitHub App installations can reach, newest first, with visibility and default branch. " +
				"Use the URL as repo_url for create_app. Owner, admin or deployer.",
			InputSchema: schema(nil, map[string]any{"limit": map[string]any{"type": "integer", "default": 200, "maximum": 2000}}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityDeploy); err != nil {
					return "", err
				}
				s, err := deploys(env)
				if err != nil {
					return "", err
				}
				rows, err := env.Q.GitHubInstallationsOfTeam(ctx, env.Caller.TeamID)
				if err != nil {
					return "", err
				}
				if len(rows) == 0 {
					return "No GitHub App installations for this team — install the App from the team settings in the web app.", nil
				}
				teamID, limit := env.Caller.TeamID, args.Int("limit", 200, 1, 2000)
				// GitHub is asked after the transaction: no connection is held while it answers.
				env.Then = func(ctx context.Context) (string, error) {
					repos, err := s.AppRepos(ctx, teamID, rows)
					if err != nil {
						return "", err
					}
					if len(repos) == 0 {
						return "No repositories.", nil
					}
					var b strings.Builder
					for i, r := range repos {
						if i >= limit {
							fmt.Fprintf(&b, "  (%d more: raise limit)\n", len(repos)-limit)
							break
						}
						vis := "public"
						if r.IsPrivate {
							vis = "private"
						}
						fmt.Fprintf(&b, "  %-45s %-8s %-12s %s\n", r.NameWithOwner, vis, orDash(ptrStr(r.DefaultBranch)), r.URL)
					}
					return b.String(), nil
				}
				return "", nil
			},
		},
	}
}

func ptrStr(s string) *string { return &s }
