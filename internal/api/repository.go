package api

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/deploy"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
The "Repository" section of a site: the app's GitHub repository, read through the GitHub CLI
connected on the app's server. Reads only — any member. 422: an imported app, an app without a
repository, or one that is not on github.com. 409: GitHub is not connected on the app's server.
*/

// RepoOut is the repository an app deploys from.
type RepoOut struct {
	Owner  string `json:"owner"`
	Name   string `json:"name"`
	URL    string `json:"url"`
	Branch string `json:"branch" doc:"The branch the app deploys."`
}

// RepoLiveOut is the live release's commit.
type RepoLiveOut struct {
	SHA     *string `json:"sha" doc:"null when the live release's commit is unknown."`
	Version int64   `json:"version"`
}

// RepoHeadOut is the branch's newest commit.
type RepoHeadOut struct {
	SHA string `json:"sha"`
}

// RepoDeployedOut is the newest deployment of a commit.
type RepoDeployedOut struct {
	Number int64  `json:"number"`
	Status string `json:"status" enum:"queued,running,succeeded,failed,cancelled"`
}

// RepoCommitOut is one commit, set against what Hosbah deployed.
type RepoCommitOut struct {
	agentproto.GitHubCommit
	IsLive   bool             `json:"is_live" doc:"The live release was built from this commit."`
	Deployed *RepoDeployedOut `json:"deployed" doc:"The newest deployment of this commit, or null."`
}

func toRepoOut(r deploy.AppRepo) RepoOut {
	return RepoOut{Owner: r.Owner, Name: r.Name, URL: r.URL, Branch: r.Branch}
}

func toRepoCommits(rc deploy.RepoContext, byCommit map[string]db.Deployment, in []agentproto.GitHubCommit) []RepoCommitOut {
	out := []RepoCommitOut{}
	for _, c := range deploy.CleanCommits(rc.Repo, in) {
		o := RepoCommitOut{GitHubCommit: c, IsLive: rc.LiveSHA != "" && deploy.SameCommit(c.SHA, rc.LiveSHA)}
		if dep, ok := byCommit[c.SHA]; ok {
			o.Deployed = &RepoDeployedOut{Number: dep.Number, Status: dep.Status}
		}
		out = append(out, o)
	}
	return out
}

// repoQueue resolves the app and its repository (any member) and queues one github.* read on the
// app's server. queue may return no operation (an empty id): there was nothing to ask.
func (d *deps) repoQueue(ctx context.Context, team, app string,
	queue func(q *db.Queries, a deploy.Actor, m *tenancy.Membership, rc deploy.RepoContext) (db.Operation, error)) (db.Operation, deploy.RepoContext, map[string]db.Deployment, error) {
	var (
		op       db.Operation
		rc       deploy.RepoContext
		byCommit map[string]db.Deployment
	)
	err := d.inTeam(ctx, team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
		a, err := appIn(ctx, q, m, app)
		if err != nil {
			return err
		}
		if rc, err = d.deploys().RepositoryOf(ctx, q, m.TeamID, a); err != nil {
			return actionErr(err, "could not read the app's repository")
		}
		if byCommit, err = deploy.DeploymentsByCommit(ctx, q, m.TeamID, a); err != nil {
			return huma.Error500InternalServerError("could not list deployments")
		}
		op, err = queue(q, webActor(id), m, rc)
		return actionErr(err, "could not queue the operation")
	})
	return op, rc, byCommit, err
}

func (d *deps) registerRepository(api huma.API) {
	tags := []string{"Repository"}
	base := "/api/teams/{team}/apps/{app}/repository"

	type repositoryOut struct {
		Body struct {
			Repo     RepoOut      `json:"repo"`
			Live     *RepoLiveOut `json:"live" doc:"The live release; null when nothing is live."`
			Head     *RepoHeadOut `json:"head" doc:"The branch's newest commit; null when the server did not answer."`
			AheadBy  *int         `json:"ahead_by" doc:"Commits on the branch that are not live yet; null when unknown."`
			BehindBy *int         `json:"behind_by" doc:"Commits of the live release that the branch does not have (a rewritten branch); null when unknown."`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "get-app-repository", Method: http.MethodGet, Path: base, Tags: tags,
		Summary: "The app's GitHub repository, the live release's commit, the branch's newest commit and how far the branch is ahead of " +
			"what is live — read through the GitHub CLI on the app's server (waits up to 40 s; head and ahead_by are null when the server " +
			"does not answer). Any member. 422: imported or not a GitHub repository; 409: GitHub is not connected on the server.",
	}, func(ctx context.Context, in *AppPath) (*repositoryOut, error) {
		out := &repositoryOut{}
		op, rc, _, err := d.repoQueue(ctx, in.Team, in.App, func(q *db.Queries, a deploy.Actor, m *tenancy.Membership, rc deploy.RepoContext) (db.Operation, error) {
			return d.deploys().GitHubCommits(ctx, q, m.TeamID, a, rc.Server, rc.Repo.Slug(), rc.Repo.Branch, 1)
		})
		if err != nil {
			return nil, err
		}
		out.Body.Repo = toRepoOut(rc.Repo)
		if rc.HasLive {
			out.Body.Live = &RepoLiveOut{SHA: optional(rc.LiveSHA), Version: rc.LiveVersion}
		}
		var res agentproto.GitHubCommitsResult
		if _, err := d.deploys().AwaitResult(ctx, op, 20*time.Second, &res); err != nil {
			return out, nil // the page still shows the repository and what is live
		}
		head := deploy.CleanCommits(rc.Repo, res.Commits)
		if len(head) == 0 {
			return out, nil
		}
		out.Body.Head = &RepoHeadOut{SHA: head[0].SHA}
		switch {
		case rc.LiveSHA == "":
			return out, nil
		case deploy.SameCommit(head[0].SHA, rc.LiveSHA):
			zero, zero2 := 0, 0
			out.Body.AheadBy, out.Body.BehindBy = &zero, &zero2
			return out, nil
		}
		op, _, _, err = d.repoQueue(ctx, in.Team, in.App, func(q *db.Queries, a deploy.Actor, m *tenancy.Membership, rc deploy.RepoContext) (db.Operation, error) {
			return d.deploys().GitHubCompare(ctx, q, m.TeamID, a, rc.Server, rc.Repo.Slug(), rc.LiveSHA, head[0].SHA)
		})
		if err != nil {
			return out, nil
		}
		var cmp agentproto.GitHubCompareResult
		if _, err := d.deploys().AwaitResult(ctx, op, 20*time.Second, &cmp); err == nil && cmp.AheadBy >= 0 && cmp.BehindBy >= 0 {
			out.Body.AheadBy, out.Body.BehindBy = &cmp.AheadBy, &cmp.BehindBy
		}
		return out, nil
	})

	type commitsOut struct {
		Body struct {
			Branch    string          `json:"branch"`
			LiveSHA   *string         `json:"live_sha" doc:"The live release's commit, or null."`
			Commits   []RepoCommitOut `json:"commits" doc:"Newest first."`
			Operation string          `json:"operation"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-app-repository-commits", Method: http.MethodGet, Path: base + "/commits", Tags: tags,
		Summary: "The newest commits of a branch (default: the app's), each marked is_live and with the newest deployment of it. " +
			"Deploy one with POST …/deployments {\"ref\": \"<its sha>\"}. Any member; waits up to 30 s for the server.",
	}, func(ctx context.Context, in *struct {
		AppPath
		Branch string `query:"branch" maxLength:"200" doc:"A branch, tag or commit to list from; default: the app's branch."`
		Limit  int    `query:"limit" minimum:"1" maximum:"100" default:"30"`
	}) (*commitsOut, error) {
		branch := strings.TrimSpace(in.Branch)
		op, rc, byCommit, err := d.repoQueue(ctx, in.Team, in.App, func(q *db.Queries, a deploy.Actor, m *tenancy.Membership, rc deploy.RepoContext) (db.Operation, error) {
			if branch == "" {
				branch = rc.Repo.Branch
			}
			return d.deploys().GitHubCommits(ctx, q, m.TeamID, a, rc.Server, rc.Repo.Slug(), branch, in.Limit)
		})
		if err != nil {
			return nil, err
		}
		var res agentproto.GitHubCommitsResult
		if _, err := d.githubAwait(ctx, op, 30*time.Second, &res); err != nil {
			return nil, err
		}
		out := &commitsOut{}
		out.Body.Branch, out.Body.LiveSHA, out.Body.Operation = branch, optional(rc.LiveSHA), op.ID
		out.Body.Commits = toRepoCommits(rc, byCommit, res.Commits)
		return out, nil
	})

	type compareOut struct {
		Body struct {
			Base         string                  `json:"base"`
			Head         string                  `json:"head"`
			AheadBy      int                     `json:"ahead_by" doc:"Commits head has that base does not."`
			BehindBy     int                     `json:"behind_by"`
			TotalCommits int                     `json:"total_commits"`
			Commits      []RepoCommitOut         `json:"commits" doc:"Newest first; at most 100."`
			Files        []agentproto.GitHubFile `json:"files" doc:"At most 300; never patch bodies."`
			Truncated    bool                    `json:"truncated" doc:"More commits or files exist than are listed."`
			Operation    string                  `json:"operation"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "compare-app-repository", Method: http.MethodGet, Path: base + "/compare", Tags: tags,
		Summary: "Compare base...head: what deploying head would change. Defaults: base = the live release's commit, head = the app's branch. " +
			"Any member; waits up to 30 s for the server. 422 when base is omitted and nothing is live.",
	}, func(ctx context.Context, in *struct {
		AppPath
		Base string `query:"base" maxLength:"200" doc:"A branch, tag or commit; default: the live release's commit."`
		Head string `query:"head" maxLength:"200" doc:"A branch, tag or commit; default: the app's branch."`
	}) (*compareOut, error) {
		cmpBase, cmpHead := strings.TrimSpace(in.Base), strings.TrimSpace(in.Head)
		op, rc, byCommit, err := d.repoQueue(ctx, in.Team, in.App, func(q *db.Queries, a deploy.Actor, m *tenancy.Membership, rc deploy.RepoContext) (db.Operation, error) {
			if cmpBase == "" {
				cmpBase = rc.LiveSHA
			}
			if cmpBase == "" {
				return db.Operation{}, huma.Error422UnprocessableEntity("nothing is live yet, so there is no default base — give base (a branch, tag or commit)")
			}
			if cmpHead == "" {
				cmpHead = rc.Repo.Branch
			}
			return d.deploys().GitHubCompare(ctx, q, m.TeamID, a, rc.Server, rc.Repo.Slug(), cmpBase, cmpHead)
		})
		if err != nil {
			return nil, err
		}
		var res agentproto.GitHubCompareResult
		if _, err := d.githubAwait(ctx, op, 30*time.Second, &res); err != nil {
			return nil, err
		}
		out := &compareOut{}
		b := &out.Body
		b.Base, b.Head, b.AheadBy, b.BehindBy, b.TotalCommits, b.Operation = cmpBase, cmpHead, res.AheadBy, res.BehindBy, res.TotalCommits, op.ID
		b.Commits = toRepoCommits(rc, byCommit, res.Commits)
		files, cut := deploy.CleanFiles(res.Files)
		b.Files, b.Truncated = files, res.Truncated || cut
		return out, nil
	})

	type pullsOut struct {
		Body struct {
			Pulls     []agentproto.GitHubPull `json:"pulls" doc:"Open pull requests, most recently updated first."`
			Operation string                  `json:"operation"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-app-repository-pulls", Method: http.MethodGet, Path: base + "/pulls", Tags: tags,
		Summary: "The repository's open pull requests. Any member; waits up to 30 s for the server.",
	}, func(ctx context.Context, in *AppPath) (*pullsOut, error) {
		op, rc, _, err := d.repoQueue(ctx, in.Team, in.App, func(q *db.Queries, a deploy.Actor, m *tenancy.Membership, rc deploy.RepoContext) (db.Operation, error) {
			return d.deploys().GitHubPulls(ctx, q, m.TeamID, a, rc.Server, rc.Repo.Slug(), 50)
		})
		if err != nil {
			return nil, err
		}
		var res agentproto.GitHubPullsResult
		if _, err := d.githubAwait(ctx, op, 30*time.Second, &res); err != nil {
			return nil, err
		}
		out := &pullsOut{}
		out.Body.Pulls, out.Body.Operation = deploy.CleanPulls(rc.Repo, res.Pulls), op.ID
		return out, nil
	})
}
