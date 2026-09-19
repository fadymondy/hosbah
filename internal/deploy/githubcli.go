package deploy

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
A server's own GitHub connection, through the GitHub CLI (gh) on it.

This sits beside the GitHub App (github.go), not in place of it: with gh logged in on a server, the
new-app form can list the repositories and branches the account sees, and deploy clones of private
github.com repositories on that server succeed through gh's git credential helper. The login lives
on the server (in /etc/cloudy/gh); the control plane keeps only what gh last reported
(servers.github). A token given for the token mode is sealed to the server's agent key at once —
operations.params holds ciphertext only that server opens, and no log or audit entry carries it.
*/

// GitHubConnectModes are the ways to log gh in.
var GitHubConnectModes = []string{agentproto.GitHubModeDevice, agentproto.GitHubModeToken}

func (s *Service) githubServer(srv db.Server) error {
	if srv.Status != "enrolled" || srv.AgentPublicKey == nil {
		return conflict("that server's agent has not enrolled yet")
	}
	return nil
}

func (s *Service) githubEnqueue(ctx context.Context, q *db.Queries, teamID string, actor Actor, srv db.Server, kind string, params any) (db.Operation, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return db.Operation{}, err
	}
	op, _, err := s.ops.Enqueue(ctx, q, ops.EnqueueSpec{TeamID: teamID, ServerID: srv.ID, Kind: kind, Params: raw, CreatedBy: actorID(actor)})
	return op, err
}

// ValidGitHubToken is the shape check on a token before it is sealed (GitHub decides the rest).
func ValidGitHubToken(t string) bool {
	return t != "" && len(t) <= 255 && !strings.ContainsAny(t, " \t\r\n")
}

// GitHubConnect installs gh on the server if needed and logs it in. device: the returned
// operation's log carries the one-time code (an info line whose data is a github.device_code) as
// soon as gh prints it; token: the token is sealed to the server's agent here and never stored
// otherwise. Audited (never with the token).
func (s *Service) GitHubConnect(ctx context.Context, q *db.Queries, teamID string, actor Actor, srv db.Server, mode, token string) (db.Operation, error) {
	if err := s.githubServer(srv); err != nil {
		return db.Operation{}, err
	}
	p := agentproto.GitHubLoginParams{Mode: mode}
	switch mode {
	case agentproto.GitHubModeDevice:
		if token != "" {
			return db.Operation{}, invalid("device mode takes no token")
		}
	case agentproto.GitHubModeToken:
		token = strings.TrimSpace(token)
		if !ValidGitHubToken(token) {
			return db.Operation{}, invalid("give a GitHub token (a personal access token with the repo and read:org scopes)")
		}
		box, err := agentproto.SealBox(*srv.AgentPublicKey, agentproto.GitHubTokenContext(srv.ID), []byte(token))
		if err != nil {
			return db.Operation{}, conflict("the token could not be sealed to that server's key")
		}
		p.Token = box
	default:
		return db.Operation{}, invalid("mode is device or token")
	}
	if err := p.Validate(); err != nil {
		return db.Operation{}, invalid(err.Error())
	}
	// Same lane on the agent, so the install runs first; the login also installs gh if needed.
	if _, err := s.githubEnqueue(ctx, q, teamID, actor, srv, agentproto.KindGitHubInstall, struct{}{}); err != nil {
		return db.Operation{}, err
	}
	op, err := s.githubEnqueue(ctx, q, teamID, actor, srv, agentproto.KindGitHubLogin, p)
	if err != nil {
		return db.Operation{}, err
	}
	return op, tenancy.Record(ctx, q, teamID, actor.UserID, "server.github_connect", "server", srv.ID,
		actor.audit(map[string]any{"name": srv.Name, "mode": mode, "operation": op.ID}))
}

// GitHubDisconnect logs gh out on the server and removes its git credential helper. Audited.
func (s *Service) GitHubDisconnect(ctx context.Context, q *db.Queries, teamID string, actor Actor, srv db.Server) (db.Operation, error) {
	if err := s.githubServer(srv); err != nil {
		return db.Operation{}, err
	}
	op, err := s.githubEnqueue(ctx, q, teamID, actor, srv, agentproto.KindGitHubLogout, struct{}{})
	if err != nil {
		return db.Operation{}, err
	}
	return op, tenancy.Record(ctx, q, teamID, actor.UserID, "server.github_disconnect", "server", srv.ID,
		actor.audit(map[string]any{"name": srv.Name, "operation": op.ID}))
}

// GitHubRefresh asks the server what gh says now (servers.github follows the result).
func (s *Service) GitHubRefresh(ctx context.Context, q *db.Queries, teamID string, actor Actor, srv db.Server) (db.Operation, error) {
	if err := s.githubServer(srv); err != nil {
		return db.Operation{}, err
	}
	return s.githubEnqueue(ctx, q, teamID, actor, srv, agentproto.KindGitHubStatus, struct{}{})
}

// GitHubRepos lists the repositories the server's GitHub account sees (owner "" = its own and its
// organisations').
func (s *Service) GitHubRepos(ctx context.Context, q *db.Queries, teamID string, actor Actor, srv db.Server, owner string, limit int) (db.Operation, error) {
	if err := s.githubServer(srv); err != nil {
		return db.Operation{}, err
	}
	p := agentproto.GitHubReposParams{Owner: strings.TrimSpace(owner), Limit: limit}
	if err := p.Validate(); err != nil {
		return db.Operation{}, invalid(err.Error())
	}
	return s.githubEnqueue(ctx, q, teamID, actor, srv, agentproto.KindGitHubRepos, p)
}

// GitHubBranches lists a repository's branches and its default branch.
func (s *Service) GitHubBranches(ctx context.Context, q *db.Queries, teamID string, actor Actor, srv db.Server, repo string) (db.Operation, error) {
	if err := s.githubServer(srv); err != nil {
		return db.Operation{}, err
	}
	p := agentproto.GitHubBranchesParams{Repo: strings.TrimSpace(repo)}
	if err := p.Validate(); err != nil {
		return db.Operation{}, invalid(err.Error())
	}
	return s.githubEnqueue(ctx, q, teamID, actor, srv, agentproto.KindGitHubBranches, p)
}

// GitHubStateOf reads servers.github; a device code past its expiry is dropped.
func GitHubStateOf(srv db.Server, now time.Time) ops.GitHubState {
	var st ops.GitHubState
	if srv.Github != nil {
		_ = json.Unmarshal([]byte(*srv.Github), &st)
	}
	if st.Pending != nil && now.After(st.Pending.ExpiresAt) {
		st.Pending = nil
	}
	return st
}
