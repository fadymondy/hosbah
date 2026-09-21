package deploy

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
)

/*
The "Repository" section of a site: a git app's GitHub repository, read through the GitHub CLI
already connected on the app's server (github.commits, github.compare, github.pulls), and set
against what Hosbah knows — the live release's commit and the deployments of each commit.

Deploying a chosen commit needs nothing new: a deployment's ref is a branch, a tag or a full
40-character commit sha (the build fetches exactly that ref), so the sha of a listed commit is a ref.
*/

// RepoContext is what every repository read of an app starts from.
type RepoContext struct {
	App         db.App
	Server      db.Server
	Repo        AppRepo
	LiveSHA     string // the live release's commit ("" when nothing is live or its commit is unknown)
	LiveVersion int64
	HasLive     bool
}

// RepositoryOf resolves an app's repository and the server whose gh reads it. 422: imported, no
// repository, or not on github.com; 409: that server has said GitHub is not connected on it.
func (s *Service) RepositoryOf(ctx context.Context, q *db.Queries, teamID string, a db.App) (RepoContext, error) {
	rc := RepoContext{App: a}
	repo, err := AppRepository(a)
	if err != nil {
		return rc, err
	}
	rc.Repo = repo
	srv, err := q.ServerInTeam(ctx, db.ServerInTeamParams{ID: a.ServerID, TeamID: teamID})
	if err != nil {
		return rc, notFound("server")
	}
	rc.Server = srv
	if st := GitHubStateOf(srv, time.Now()); st.CheckedAt != nil && !st.Connected {
		return rc, conflict("GitHub is not connected on " + srv.Name + " — connect it first (the server's GitHub page, or github_connect)")
	}
	if r, err := q.LiveReleaseOfApp(ctx, a.ID); err == nil && r.TeamID == teamID {
		rc.HasLive, rc.LiveVersion = true, r.Version
		if r.CommitSha != nil && agentproto.ValidGitHubSHA(*r.CommitSha) {
			rc.LiveSHA = strings.ToLower(*r.CommitSha)
		}
	}
	return rc, nil
}

// DeploymentsByCommit maps a full commit sha to the newest deployment of it: one that built that
// commit, or one asked for by that sha and not built yet.
func DeploymentsByCommit(ctx context.Context, q *db.Queries, teamID string, a db.App) (map[string]db.Deployment, error) {
	rows, err := q.ListDeploymentsOfApp(ctx, db.ListDeploymentsOfAppParams{AppID: a.ID, TeamID: teamID, Limit: 1000})
	if err != nil {
		return nil, err
	}
	out := map[string]db.Deployment{}
	for _, d := range rows { // newest first: the first one seen wins
		sha := ""
		switch {
		case d.CommitSha != nil && *d.CommitSha != "":
			sha = *d.CommitSha
		case len(d.Ref) == 40 && agentproto.ValidGitHubSHA(d.Ref):
			sha = d.Ref
		}
		sha = strings.ToLower(sha)
		if _, seen := out[sha]; sha != "" && !seen {
			out[sha] = d
		}
	}
	return out, nil
}

// SameCommit: a full sha equals another, or a short one (≥ 7) prefixes it.
func SameCommit(a, b string) bool {
	a, b = strings.ToLower(a), strings.ToLower(b)
	if len(a) < 7 || len(b) < 7 {
		return false
	}
	return strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
}

// CleanCommits keeps what an agent reported only in the shape the control plane vouches for: full
// hex shas, links rebuilt from the repository, https avatars.
func CleanCommits(repo AppRepo, in []agentproto.GitHubCommit) []agentproto.GitHubCommit {
	out := make([]agentproto.GitHubCommit, 0, len(in))
	for _, c := range in {
		if len(c.SHA) != 40 || !agentproto.ValidGitHubSHA(c.SHA) || len(out) >= 250 {
			continue
		}
		c.SHA = strings.ToLower(c.SHA)
		c.ShortSHA = c.SHA[:7]
		c.URL = repo.URL + "/commit/" + c.SHA
		if !strings.HasPrefix(c.AuthorAvatarURL, "https://") {
			c.AuthorAvatarURL = ""
		}
		if c.AuthorLogin != "" && !agentproto.ValidGitHubOwner(strings.TrimSuffix(c.AuthorLogin, "[bot]")) {
			c.AuthorLogin = ""
		}
		c.Message, c.AuthorName = clipRunes(c.Message, 300), clipRunes(c.AuthorName, 100)
		out = append(out, c)
	}
	return out
}

// CleanFiles bounds a comparison's file list.
func CleanFiles(in []agentproto.GitHubFile) ([]agentproto.GitHubFile, bool) {
	out := make([]agentproto.GitHubFile, 0, len(in))
	for _, f := range in {
		if len(out) >= agentproto.MaxGitHubCompareFiles {
			return out, true
		}
		f.Filename, f.Status = clipRunes(f.Filename, 500), clipRunes(f.Status, 20)
		out = append(out, f)
	}
	return out, false
}

// CleanPulls rebuilds each pull request's link from the repository.
func CleanPulls(repo AppRepo, in []agentproto.GitHubPull) []agentproto.GitHubPull {
	out := make([]agentproto.GitHubPull, 0, len(in))
	for _, p := range in {
		if p.Number <= 0 || len(out) >= agentproto.MaxGitHubPulls {
			continue
		}
		p.URL = fmt.Sprintf("%s/pull/%d", repo.URL, p.Number)
		p.Title, p.HeadRef, p.BaseRef, p.AuthorLogin = clipRunes(p.Title, 300), clipRunes(p.HeadRef, 250), clipRunes(p.BaseRef, 250), clipRunes(p.AuthorLogin, 100)
		out = append(out, p)
	}
	return out
}

func clipRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
