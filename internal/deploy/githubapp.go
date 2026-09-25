package deploy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
The GitHub App, wired to teams.

  - A team owner or admin installs the App (GET /api/teams/{team}/github/install → GitHub's
    installations/new with a single-use state bound to the team and the user). GitHub sends the
    browser to the App's Setup URL (GET /api/github/setup); the state is checked, and the
    installation is read back from GitHub with the App's own JWT before anything is stored: the
    query string only says which id to ask about. See internal/api/github_app.go.
  - Its repositories are listed across the team's installations with a fresh installation token
    each (cached a minute per team).
  - A git app whose github.com owner is one of the team's installations clones with a fresh
    installation token (valid an hour), sealed to the build server's agent for that repository
    (agentproto.GitSource.Token). The token is minted per clone and never stored or logged.
  - Pushes to a repository of an installation deploy the matching apps of that installation's team
    only, once per delivery (hooks.go).
*/

// SetGitHub replaces the GitHub App integration (tests point it at a fake GitHub).
func (s *Service) SetGitHub(g GitHubApp) {
	s.ghMu.Lock()
	s.github = g
	s.repoCache = map[string]repoCacheEntry{}
	s.ghMu.Unlock()
}

func (s *Service) gh() GitHubApp {
	s.ghMu.Lock()
	defer s.ghMu.Unlock()
	return s.github
}

type repoCacheEntry struct {
	at    time.Time
	key   string
	repos []GitHubAppRepo
}

const repoCacheTTL = time.Minute

// ForgetAppRepos drops a team's cached repository listing (an installation changed).
func (s *Service) ForgetAppRepos(teamID string) {
	s.ghMu.Lock()
	delete(s.repoCache, teamID)
	s.ghMu.Unlock()
}

// AppRepos lists the repositories of a team's (unsuspended) installations. Call it outside any
// transaction: it talks to GitHub.
func (s *Service) AppRepos(ctx context.Context, teamID string, insts []db.GithubInstallation) ([]GitHubAppRepo, error) {
	g := s.gh()
	if !g.Configured() {
		return nil, ErrGitHubNotConfigured
	}
	ids := []string{}
	for _, in := range insts {
		if in.SuspendedAt == nil {
			ids = append(ids, in.AccountLogin+":"+in.RepositorySelection)
		}
	}
	key := strings.Join(ids, ",")
	s.ghMu.Lock()
	if e, ok := s.repoCache[teamID]; ok && e.key == key && time.Since(e.at) < repoCacheTTL {
		s.ghMu.Unlock()
		return e.repos, nil
	}
	s.ghMu.Unlock()
	out := []GitHubAppRepo{}
	for _, in := range insts {
		if in.SuspendedAt != nil {
			continue
		}
		repos, err := g.InstallationRepos(ctx, in.InstallationID, MaxAppRepos-len(out))
		if err != nil {
			return nil, err
		}
		out = append(out, repos...)
		if len(out) >= MaxAppRepos {
			break
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	s.ghMu.Lock()
	s.repoCache[teamID] = repoCacheEntry{at: time.Now(), key: key, repos: out}
	s.ghMu.Unlock()
	return out, nil
}

// installationForRepo is the team's unsuspended installation on a github.com repository's owner.
func installationForRepo(ctx context.Context, q *db.Queries, teamID, repoURL string) (db.GithubInstallation, bool) {
	owner, _, ok := strings.Cut(agentproto.GitHubRepoOf(repoURL), "/")
	if !ok || owner == "" {
		return db.GithubInstallation{}, false
	}
	insts, err := q.GitHubInstallationsOfTeam(ctx, teamID)
	if err != nil {
		return db.GithubInstallation{}, false
	}
	for _, in := range insts {
		if in.SuspendedAt == nil && strings.EqualFold(in.AccountLogin, owner) {
			return in, true
		}
	}
	return db.GithubInstallation{}, false
}

// CloneToken returns a GitHub App installation token for repoURL sealed to the agent whose public
// key is agentKey (agentproto.GitSource.Token), or "" when no installation of the team covers the
// repository. The token is minted now (GitHub makes it valid for an hour) and exists in the clear
// only in this function's memory. It reads the team's installations in its own short transaction
// and talks to GitHub outside any.
func (s *Service) CloneToken(ctx context.Context, teamID, repoURL, agentKey string) (string, error) {
	g := s.gh()
	if !g.Configured() || agentKey == "" || agentproto.GitHubRepoOf(repoURL) == "" {
		return "", nil
	}
	var (
		in db.GithubInstallation
		ok bool
	)
	if err := tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{TeamID: teamID}, func(q *db.Queries, _ *sql.Tx) error {
		in, ok = installationForRepo(ctx, q, teamID, repoURL)
		return nil
	}); err != nil || !ok {
		return "", err
	}
	token, _, err := g.InstallationToken(ctx, in.InstallationID)
	if err != nil {
		return "", err
	}
	return agentproto.SealBox(agentKey, agentproto.GitTokenContext(repoURL), []byte(token))
}

// AddDeployKeyToRepo adds publicKey (one OpenSSH authorized_keys line) as a read-only deploy key
// of ownerRepo ("owner/name") on GitHub, through the team's installation that covers it. It fails
// with ErrGitHubNotConfigured (no App), or a plain error naming the repository when no installation
// of the team covers it, or whatever GitHub itself refused with.
func (s *Service) AddDeployKeyToRepo(ctx context.Context, teamID, ownerRepo, title, publicKey string) (int64, error) {
	g := s.gh()
	if !g.Configured() {
		return 0, ErrGitHubNotConfigured
	}
	owner, repo, ok := strings.Cut(ownerRepo, "/")
	if !ok || owner == "" || repo == "" {
		return 0, errors.New("repo must be \"owner/name\"")
	}
	var (
		in db.GithubInstallation
		found bool
	)
	if err := tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{TeamID: teamID}, func(q *db.Queries, _ *sql.Tx) error {
		in, found = installationForRepo(ctx, q, teamID, "https://github.com/"+ownerRepo)
		return nil
	}); err != nil {
		return 0, err
	}
	if !found {
		return 0, fmt.Errorf("no connected GitHub installation of this team covers %s", ownerRepo)
	}
	return g.AddDeployKey(ctx, in.InstallationID, owner, repo, title, publicKey, true)
}

// InstallationError is a refusal of the setup callback, with the code the web page shows.
type InstallationError struct{ Code, Msg string }

func (e *InstallationError) Error() string { return e.Msg }

// ConfirmInstallation asks GitHub, with the App's JWT, whether installationID exists and on which
// account. Nothing about it is taken from the browser.
func (s *Service) ConfirmInstallation(ctx context.Context, installationID int64) (GitHubInstallation, error) {
	g := s.gh()
	if !g.Configured() {
		return GitHubInstallation{}, &InstallationError{Code: "not_configured", Msg: ErrGitHubNotConfigured.Error()}
	}
	in, err := g.Installation(ctx, installationID)
	if errors.Is(err, ErrInstallationNotFound) {
		return in, &InstallationError{Code: "installation_unknown", Msg: "GitHub does not know that installation of the App"}
	}
	if err != nil {
		return in, &InstallationError{Code: "github_unavailable", Msg: "GitHub could not be asked about the installation — try again"}
	}
	switch in.AccountType {
	case "User", "Organization", "Enterprise":
	default:
		in.AccountType = "User"
	}
	if in.RepositorySelection != "selected" {
		in.RepositorySelection = "all"
	}
	return in, nil
}

// StoreInstallation binds a confirmed installation to teamID. An installation another team holds
// is refused (409 installation_taken): one installation serves one team.
func (s *Service) StoreInstallation(ctx context.Context, teamID, userID string, in GitHubInstallation) (db.GithubInstallation, error) {
	var row db.GithubInstallation
	err := tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{System: true, TeamID: teamID, UserID: userID}, func(q *db.Queries, _ *sql.Tx) error {
		existing, err := q.GitHubInstallationByInstallationID(ctx, in.ID)
		isNew := errors.Is(err, sql.ErrNoRows)
		switch {
		case err == nil && existing.TeamID != teamID:
			return &InstallationError{Code: "installation_taken", Msg: "that GitHub installation is already connected to another team"}
		case err != nil && !isNew:
			return err
		}
		row, err = q.UpsertGitHubInstallation(ctx, db.UpsertGitHubInstallationParams{TeamID: teamID, InstallationID: in.ID,
			AccountLogin: in.AccountLogin, AccountType: in.AccountType, RepositorySelection: in.RepositorySelection,
			InstalledBy: userID, SuspendedAt: in.SuspendedAt})
		if errors.Is(err, sql.ErrNoRows) {
			return &InstallationError{Code: "installation_taken", Msg: "that GitHub installation is already connected to another team"}
		}
		if err != nil {
			return err
		}
		action := "github.app_updated"
		if isNew {
			action = "github.app_installed"
		}
		return tenancy.Record(ctx, q, teamID, userID, action, "github_installation", row.ID, map[string]any{
			"installation_id": in.ID, "account": in.AccountLogin, "account_type": in.AccountType, "repository_selection": in.RepositorySelection})
	})
	if err == nil {
		s.ForgetAppRepos(teamID)
	}
	return row, err
}
