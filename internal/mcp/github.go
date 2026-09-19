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
