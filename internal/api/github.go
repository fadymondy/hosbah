package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// ServerGitHubOut is a server's own GitHub connection (the GitHub CLI on it), as last reported by
// a github.* operation. Never a token: the login stays on the server.
type ServerGitHubOut struct {
	Connected bool                    `json:"connected"`
	User      *string                 `json:"user"`
	CheckedAt *time.Time              `json:"checked_at"`
	Installed bool                    `json:"installed" doc:"The GitHub CLI (gh) is installed on the server."`
	Version   *string                 `json:"version"`
	Scopes    []string                `json:"scopes"`
	Error     *string                 `json:"error" doc:"Why the last login or logout failed."`
	Pending   *ServerGitHubPendingOut `json:"pending,omitempty" doc:"A device login waiting for its one-time code to be entered."`
}

// ServerGitHubPendingOut is the device flow's code, while it is valid.
type ServerGitHubPendingOut struct {
	Operation       string    `json:"operation"`
	UserCode        string    `json:"user_code"`
	VerificationURI string    `json:"verification_uri"`
	ExpiresAt       time.Time `json:"expires_at"`
}

func toServerGitHubOut(st ops.GitHubState) ServerGitHubOut {
	out := ServerGitHubOut{Connected: st.Connected, User: optional(st.User), CheckedAt: st.CheckedAt, Installed: st.Installed,
		Version: optional(st.Version), Scopes: st.Scopes, Error: optional(st.Error)}
	if out.Scopes == nil {
		out.Scopes = []string{}
	}
	if p := st.Pending; p != nil {
		out.Pending = &ServerGitHubPendingOut{Operation: p.Operation, UserCode: p.UserCode, VerificationURI: p.VerificationURI, ExpiresAt: p.ExpiresAt}
	}
	return out
}

// githubAwait waits for a github.* operation (after its queuing transaction committed).
func (d *deps) githubAwait(ctx context.Context, op db.Operation, wait time.Duration, out any) (db.Operation, error) {
	done, err := d.deploys().AwaitResult(ctx, op, wait, out)
	var ae *deploy.ActionError
	switch {
	case err == nil:
		return done, nil
	case errors.Is(err, deploy.ErrStillRunning):
		return op, huma.Error504GatewayTimeout("the server did not answer in time — is its agent connected? (operation " + op.ID + ")")
	case errors.As(err, &ae) && ae.Code == "operation_failed":
		return done, huma.Error502BadGateway(ae.Msg)
	}
	return done, actionErr(err, "the operation failed")
}

// githubQueue resolves the server, checks ability, and queues one github.* operation.
func (d *deps) githubQueue(ctx context.Context, team, server, ability string,
	queue func(q *db.Queries, a deploy.Actor, m *tenancy.Membership, s db.Server) (db.Operation, error)) (db.Operation, db.Server, error) {
	var (
		op  db.Operation
		srv db.Server
	)
	err := d.inTeam(ctx, team, ability, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
		s, err := serverIn(ctx, q, m, server)
		if err != nil {
			return err
		}
		srv = s
		op, err = queue(q, webActor(id), m, s)
		return actionErr(err, "could not queue the operation")
	})
	return op, srv, err
}

// notConnected refuses a repository listing the server has already said it cannot do.
func notConnected(s db.Server) error {
	st := deploy.GitHubStateOf(s, time.Now())
	if st.CheckedAt != nil && !st.Connected {
		return huma.Error409Conflict("GitHub is not connected on this server — connect it first (POST …/github/connect)")
	}
	return nil
}

func (d *deps) registerGitHub(api huma.API) {
	tags := []string{"GitHub"}
	base := "/api/teams/{team}/servers/{server}/github"

	type connectOut struct {
		Status int
		Body   struct {
			Operation OperationOut    `json:"operation" doc:"The github.login operation. Device mode: stream it; an info line with data.type \"github.device_code\" carries the code."`
			GitHub    ServerGitHubOut `json:"github"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "connect-server-github", Method: http.MethodPost, Path: base + "/connect",
		DefaultStatus: http.StatusAccepted, Tags: tags,
		Summary: "Connect the server to GitHub through the GitHub CLI (installed if missing). device: the operation's log carries a " +
			"one-time code to enter at github.com/login/device; token: a token with the repo and read:org scopes, sealed to the " +
			"server's agent and never stored in the clear. Owner or admin; audited.",
	}, func(ctx context.Context, in *struct {
		ServerPath
		Body struct {
			Mode  string `json:"mode" enum:"device,token"`
			Token string `json:"token,omitempty" maxLength:"255" writeOnly:"true" doc:"token mode: a GitHub token (sealed at once; never returned)."`
		}
	}) (*connectOut, error) {
		op, srv, err := d.githubQueue(ctx, in.Team, in.Server, tenancy.AbilityManageServers,
			func(q *db.Queries, a deploy.Actor, m *tenancy.Membership, s db.Server) (db.Operation, error) {
				return d.deploys().GitHubConnect(ctx, q, m.TeamID, a, s, in.Body.Mode, in.Body.Token)
			})
		if err != nil {
			return nil, err
		}
		out := &connectOut{Status: http.StatusAccepted}
		out.Body.Operation, out.Body.GitHub = toOperationOut(op), toServerGitHubOut(deploy.GitHubStateOf(srv, time.Now()))
		return out, nil
	})

	type statusOut struct {
		Body struct {
			GitHub    ServerGitHubOut `json:"github"`
			Operation *OperationOut   `json:"operation,omitempty" doc:"refresh: the github.status operation."`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "get-server-github", Method: http.MethodGet, Path: base, Tags: tags,
		Summary: "The server's GitHub connection as last reported. refresh=1 asks the server now (owner, admin or deployer) and waits up to 20 s.",
	}, func(ctx context.Context, in *struct {
		ServerPath
		Refresh bool `query:"refresh" doc:"Run github.status on the server and wait for it (up to 20 s)."`
	}) (*statusOut, error) {
		out := &statusOut{}
		read := func() error {
			return d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
				s, err := serverIn(ctx, q, m, in.Server)
				if err != nil {
					return err
				}
				out.Body.GitHub = toServerGitHubOut(deploy.GitHubStateOf(s, time.Now()))
				return nil
			})
		}
		if in.Refresh {
			op, _, err := d.githubQueue(ctx, in.Team, in.Server, tenancy.AbilityOperate,
				func(q *db.Queries, a deploy.Actor, m *tenancy.Membership, s db.Server) (db.Operation, error) {
					return d.deploys().GitHubRefresh(ctx, q, m.TeamID, a, s)
				})
			if err != nil {
				return nil, err
			}
			done, err := d.githubAwait(ctx, op, 20*time.Second, nil)
			if err != nil {
				return nil, err
			}
			o := toOperationOut(done)
			out.Body.Operation = &o
		}
		return out, read()
	})

	type opBody struct {
		Status int
		Body   struct {
			Operation OperationOut `json:"operation"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "disconnect-server-github", Method: http.MethodDelete, Path: base,
		DefaultStatus: http.StatusAccepted, Tags: tags,
		Summary: "Log the server's GitHub CLI out and remove its git credential helper. Owner or admin; audited.",
	}, func(ctx context.Context, in *ServerPath) (*opBody, error) {
		op, _, err := d.githubQueue(ctx, in.Team, in.Server, tenancy.AbilityManageServers,
			func(q *db.Queries, a deploy.Actor, m *tenancy.Membership, s db.Server) (db.Operation, error) {
				return d.deploys().GitHubDisconnect(ctx, q, m.TeamID, a, s)
			})
		if err != nil {
			return nil, err
		}
		out := &opBody{Status: http.StatusAccepted}
		out.Body.Operation = toOperationOut(op)
		return out, nil
	})

	type reposOut struct {
		Body struct {
			Repos     []agentproto.GitHubRepo `json:"repos"`
			Owners    []string                `json:"owners"`
			Truncated bool                    `json:"truncated"`
			Operation string                  `json:"operation"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-server-github-repos", Method: http.MethodGet, Path: base + "/repos", Tags: tags,
		Summary: "Repositories the server's GitHub account can see (its own and its organisations', or one owner's), newest first. " +
			"Owner, admin or deployer; waits up to 30 s for the server.",
	}, func(ctx context.Context, in *struct {
		ServerPath
		Owner string `query:"owner" maxLength:"39" doc:"A user or organisation; empty = the account's own and its organisations'."`
		Limit int    `query:"limit" minimum:"1" maximum:"1000" default:"200"`
	}) (*reposOut, error) {
		op, _, err := d.githubQueue(ctx, in.Team, in.Server, tenancy.AbilityOperate,
			func(q *db.Queries, a deploy.Actor, m *tenancy.Membership, s db.Server) (db.Operation, error) {
				if err := notConnected(s); err != nil {
					return db.Operation{}, err
				}
				return d.deploys().GitHubRepos(ctx, q, m.TeamID, a, s, in.Owner, in.Limit)
			})
		if err != nil {
			return nil, err
		}
		var res agentproto.GitHubReposResult
		if _, err := d.githubAwait(ctx, op, 30*time.Second, &res); err != nil {
			return nil, err
		}
		out := &reposOut{}
		out.Body.Repos, out.Body.Owners, out.Body.Truncated, out.Body.Operation = res.Repos, res.Owners, res.Truncated, op.ID
		if out.Body.Repos == nil {
			out.Body.Repos = []agentproto.GitHubRepo{}
		}
		if out.Body.Owners == nil {
			out.Body.Owners = []string{}
		}
		return out, nil
	})

	type branchesOut struct {
		Body struct {
			Default   string   `json:"default"`
			Branches  []string `json:"branches"`
			Operation string   `json:"operation"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-server-github-branches", Method: http.MethodGet, Path: base + "/repos/{owner}/{name}/branches",
		Tags: tags, Summary: "A repository's branches and default branch, read through the server's GitHub CLI. Owner, admin or deployer.",
	}, func(ctx context.Context, in *struct {
		ServerPath
		Owner string `path:"owner" maxLength:"39"`
		Name  string `path:"name" maxLength:"100"`
	}) (*branchesOut, error) {
		op, _, err := d.githubQueue(ctx, in.Team, in.Server, tenancy.AbilityOperate,
			func(q *db.Queries, a deploy.Actor, m *tenancy.Membership, s db.Server) (db.Operation, error) {
				if err := notConnected(s); err != nil {
					return db.Operation{}, err
				}
				return d.deploys().GitHubBranches(ctx, q, m.TeamID, a, s, in.Owner+"/"+in.Name)
			})
		if err != nil {
			return nil, err
		}
		var res agentproto.GitHubBranchesResult
		if _, err := d.githubAwait(ctx, op, 30*time.Second, &res); err != nil {
			return nil, err
		}
		out := &branchesOut{}
		out.Body.Default, out.Body.Branches, out.Body.Operation = res.Default, res.Branches, op.ID
		if out.Body.Branches == nil {
			out.Body.Branches = []string{}
		}
		return out, nil
	})
}
