package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	"github.com/fadymondy/hosbah/internal/crypto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/importer"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

/*
Adopting an imported app: turning something Hosbah found running on a server (imported.go,
workloads.go) into a normal, Hosbah-built git app IN PLACE — no rebuild is required to take it over.

  - A container's actual configuration (image, env, ports, labels, mounts, restart policy) is read
    once with container.inspect (agentproto.ContainerInspectResult) and never logged; its env values
    are sealed with the team DEK exactly like SetEnv, and release #0 is recorded pointing at the
    image already running, so there is something to roll back to even though Hosbah never built it.
  - A guest or systemd unit has no image to point a release at: no release #0. The old workload keeps
    running until the team explicitly confirms it stopped (ConfirmAdoptedWorkloadStopped) — Hosbah
    never stops it on its own.
  - Either way the app's source becomes "git" immediately: the workload keeps serving under whatever
    routed it there until the first real deploy goes through the normal pipeline (build → web on an
    ephemeral port → health gate → Caddy switch → drain) and, for a container only, automatically
    retires the old one (retireAdoptedContainer, hooked into service.go's deploy()). Domains are
    untouched throughout.

Missing information never blocks adoption: AdoptProposal guesses what it can (a source label, a
connected GitHub repository whose name matches, a published port) and reports a confidence and a
source for each field, so the caller can present a one-click, editable confirmation instead of a form
to fill in from scratch.
*/

// AdoptInput is what the confirm dialog (or an MCP call informed by AdoptProposal) sends.
type AdoptInput struct {
	RepoURL string
	Branch  string
	Build   string // auto | dockerfile | railpack — advisory: build detection still runs at deploy time
	Port    int    // 0 = keep the app's current port
}

func validBuildKind(b string) bool {
	switch b {
	case "", "auto", "dockerfile", "railpack":
		return true
	}
	return false
}

// Adopt turns an imported app into a git app in place. rel is non-nil only for a container (release
// #0, pointing at the image already running).
func (s *Service) Adopt(ctx context.Context, q *db.Queries, teamID string, actor Actor, a db.App, in AdoptInput) (db.App, *db.Release, error) {
	if !IsImported(a) {
		return db.App{}, nil, coded(http.StatusConflict, CodeNotImported, a.Name+" is already built by Hosbah")
	}
	in.RepoURL = strings.TrimSpace(in.RepoURL)
	if err := agentproto.CheckRepoURL(in.RepoURL); err != nil {
		return db.App{}, nil, invalid(err.Error())
	}
	in.Branch = strings.TrimSpace(in.Branch)
	if in.Branch == "" {
		in.Branch = "main"
	}
	if !validBuildKind(in.Build) {
		return db.App{}, nil, invalid("build is auto, dockerfile or railpack")
	}
	if in.Port != 0 && (in.Port < 1 || in.Port > 65535) {
		return db.App{}, nil, invalid("port must be between 1 and 65535")
	}

	w := WorkloadOfApp(a)
	var inspected *agentproto.ContainerInspectResult
	var env map[string]string
	if w.IsContainer() {
		if _, err := s.check(ctx, q, teamID, w); err != nil {
			return db.App{}, nil, err
		}
		// Its own transaction, committed before AwaitResult polls for it — see enqueueCommitted.
		op, err := s.enqueueCommitted(ctx, teamID, actor, w, agentproto.KindContainerInspect,
			agentproto.ContainerInspectParams{Name: w.OriginKey, Label: w.Name}, "", nil)
		if err != nil {
			return db.App{}, nil, err
		}
		var res agentproto.ContainerInspectResult
		if _, err := s.AwaitResult(ctx, op, 20*time.Second, &res); err != nil {
			return db.App{}, nil, err
		}
		inspected = &res
		// The agent sealed the container's env to this control plane's own op-signing key rather
		// than returning it in the clear — operations.result is persisted, so an Env map here would
		// put secrets in the database. Opened once, in memory, then re-sealed with the team DEK below.
		env, err = s.ops.OpenEnvBox(agentproto.AdoptEnvContext(op.ID, w.OriginKey), res.EnvBox)
		if err != nil {
			return db.App{}, nil, err
		}
	}

	var port *int64
	if in.Port != 0 {
		p := int64(in.Port)
		port = &p
	}
	updated, err := q.AdoptApp(ctx, db.AdoptAppParams{ID: a.ID, TeamID: teamID, RepoURL: in.RepoURL, Branch: in.Branch, Port: port})
	if err != nil {
		return db.App{}, nil, err
	}

	if len(env) > 0 {
		dek, err := s.ops.TeamDEK(ctx, q, teamID)
		if err != nil {
			return db.App{}, nil, err
		}
		for name, value := range env {
			if !validEnvName(name) || name == "PORT" {
				continue // PORT is Hosbah's own; other odd names are silently skipped rather than blocking adoption
			}
			sealed, err := crypto.Seal(dek, teamID, EnvSealContext(a.ID, name), []byte(value))
			if err != nil {
				return db.App{}, nil, err
			}
			if _, err := q.UpsertEnvVar(ctx, db.UpsertEnvVarParams{TeamID: teamID, AppID: a.ID, Name: name, ValueSealed: sealed, UpdatedBy: actor.UserID}); err != nil {
				return db.App{}, nil, err
			}
		}
	}

	var rel *db.Release
	if inspected != nil {
		eff := Effective{Port: int(updated.Port), HealthPath: updated.HealthcheckPath, HealthTimeout: int(updated.HealthcheckTimeout),
			Processes: map[string]string{"web": ""}}
		config, _ := json.Marshal(eff)
		// releases has no team-scope INSERT policy ("written only by the worker") — only System may
		// insert. The deployment is created in the same System transaction: releases_deployment_fk
		// needs to see it, and a row inserted by the still-open, uncommitted outer (team-scoped)
		// transaction is invisible to this separate connection — see systemTx. Committed together,
		// immediately, so the outer transaction's later statements (read committed) see both rows.
		var dep db.Deployment
		var created db.Release
		if err := s.systemTx(ctx, teamID, func(txq *db.Queries) error {
			var err error
			dep, err = txq.InsertAdoptDeployment(ctx, db.InsertAdoptDeploymentParams{ID: newUUID(), TeamID: teamID, AppID: a.ID,
				ServerID: a.ServerID, Ref: in.Branch, Trigger: actor.trigger(), CreatedBy: actor.UserID})
			if err != nil {
				return err
			}
			created, err = txq.InsertAdoptRelease(ctx, db.InsertAdoptReleaseParams{ID: newUUID(), TeamID: teamID, AppID: a.ID, DeploymentID: dep.ID,
				Image: inspected.Image, Port: int64(updated.Port), Config: string(config)})
			return err
		}); err != nil {
			return db.App{}, nil, err
		}
		if err := q.SetDeploymentRelease(ctx, db.SetDeploymentReleaseParams{ID: dep.ID, ReleaseID: &created.ID}); err != nil {
			return db.App{}, nil, err
		}
		if err := q.SetAppState(ctx, db.SetAppStateParams{Status: "running", CurrentReleaseID: &created.ID, ID: a.ID}); err != nil {
			return db.App{}, nil, err
		}
		rel = &created
	}

	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "app.adopted", "app", a.ID, actor.audit(map[string]any{
		"name": a.Name, "repo_url": in.RepoURL, "branch": in.Branch, "build": in.Build, "origin_kind": w.OriginKind})); err != nil {
		return db.App{}, nil, err
	}
	final, err := q.AppInTeam(ctx, db.AppInTeamParams{ID: a.ID, TeamID: teamID})
	if err != nil {
		return db.App{}, nil, err
	}
	s.ops.Broker().Publish(fmt.Sprintf("app:%s", a.ID))
	return final, rel, nil
}

// retireAdoptedContainer runs after the first real deploy of an adopted app goes live: the old
// imported container (still running under its original name) is stopped and removed, and the app's
// origin is cleared so this never runs again. A guest or systemd unit is never touched here — only a
// container, and only automatically; see ConfirmAdoptedWorkloadStopped for the other two.
func (r *runner) retireAdoptedContainer(ctx context.Context) {
	if r.app.OriginKind == nil || *r.app.OriginKind != "container" || r.app.OriginKey == nil || *r.app.OriginKey == "" {
		return
	}
	key, name := *r.app.OriginKey, r.app.Name
	if err := r.op(ctx, "adopt-cleanup", agentproto.KindWorkloadDestroy,
		agentproto.DestroyParams{WorkloadRef: agentproto.Container(key, name), Purge: false}, nil); err != nil {
		r.logf("error", "the new release is live, but stopping the imported container %s failed: %v — stop it by hand when convenient", key, err)
		return
	}
	if err := r.tx(ctx, func(q *db.Queries) error {
		return q.ClearAppOrigin(ctx, db.ClearAppOriginParams{ID: r.app.ID, TeamID: r.dep.TeamID})
	}); err != nil {
		r.logf("error", "the imported container %s was retired, but its record could not be cleared: %v", key, err)
		return
	}
	r.logf("info", "the imported container %s has been retired — %s is now fully a Hosbah build", key, name)
}

// ConfirmAdoptedWorkloadStopped is the explicit, one-click action for an adopted guest or systemd
// unit's old workload: Hosbah never stops one on its own (a container is the only origin retired
// automatically, by the first real deploy). Requires the first real deploy to have already gone live
// (a release beyond #0 exists) so nothing is stopped before the app can serve traffic another way. A
// guest is actually powered off; a unit cannot be controlled at all, so this only acknowledges a
// manual stop and clears the record.
func (s *Service) ConfirmAdoptedWorkloadStopped(ctx context.Context, q *db.Queries, teamID string, actor Actor, a db.App) (db.App, error) {
	if IsImported(a) {
		return db.App{}, coded(http.StatusConflict, CodeNotImported, a.Name+" has not been adopted")
	}
	if a.OriginKind == nil || *a.OriginKind == "" {
		return db.App{}, conflict(a.Name + " has no old workload left to confirm — it was already cleared")
	}
	if *a.OriginKind == "container" {
		return db.App{}, conflict(a.Name + "'s old container is retired automatically by its first deploy; there is nothing to confirm")
	}
	live, err := q.LiveReleaseOfApp(ctx, a.ID)
	if err != nil || live.TeamID != teamID || live.Version < 1 {
		return db.App{}, conflict("deploy " + a.Name + " from git first — the old workload stays up until the new one is live")
	}

	w := WorkloadOfApp(a)
	w.Source = SourceImported // the gates in check() beyond "is this imported" (ownership, server, state) still apply
	if *a.OriginKind == "guest" {
		if _, err := s.check(ctx, q, teamID, w); err != nil {
			return db.App{}, err
		}
		// Its own transaction, committed before AwaitResult polls for it — see enqueueCommitted.
		op, err := s.enqueueCommitted(ctx, teamID, actor, w, agentproto.KindWorkloadPower,
			agentproto.PowerParams{WorkloadRef: w.Ref(), Action: agentproto.PowerShutdown}, "workload.power", map[string]any{"action": "shutdown"})
		if err != nil {
			return db.App{}, err
		}
		if _, err := s.AwaitResult(ctx, op, 30*time.Second, nil); err != nil && err != ErrStillRunning {
			return db.App{}, err
		}
	}
	if err := q.ClearAppOrigin(ctx, db.ClearAppOriginParams{ID: a.ID, TeamID: teamID}); err != nil {
		return db.App{}, err
	}
	if err := tenancy.Record(ctx, q, teamID, actor.UserID, "app.adopted_workload_stopped", "app", a.ID, actor.audit(map[string]any{
		"name": a.Name, "origin_kind": *a.OriginKind})); err != nil {
		return db.App{}, err
	}
	return q.AppInTeam(ctx, db.AppInTeamParams{ID: a.ID, TeamID: teamID})
}

// ---- adopt proposal -----------------------------------------------------------------------------

// AdoptField is one guessed field of an adopt proposal: a value with how sure the guess is and where
// it came from, or a value-less entry naming what is missing (the caller lets the user fill it in).
type AdoptField struct {
	Value      string `json:"value,omitempty"`
	Confidence string `json:"confidence,omitempty"` // high | medium | low | "" (missing)
	Source     string `json:"source,omitempty"`     // e.g. "label:org.opencontainers.image.source", "github:name match"
}

// AdoptProposal is what the confirm dialog opens pre-filled with.
type AdoptProposal struct {
	RepoURL     AdoptField `json:"repo_url"`
	Branch      AdoptField `json:"branch"`
	Build       AdoptField `json:"build"`
	Port        AdoptField `json:"port"`
	Missing     []string   `json:"missing"`
	ContainerOK bool       `json:"container_ok"` // false: container.inspect could not be reached; guesses degrade to name-matching only
}

// Propose guesses the fields Adopt needs, so nothing about missing information ever blocks the
// action: everything here is a starting point the caller may accept as-is or edit before posting.
func (s *Service) Propose(ctx context.Context, q *db.Queries, teamID string, a db.App) (AdoptProposal, error) {
	if !IsImported(a) {
		return AdoptProposal{}, coded(http.StatusConflict, CodeNotImported, a.Name+" is already built by Hosbah")
	}
	w := WorkloadOfApp(a)
	var out AdoptProposal

	var labels map[string]string
	var inspectedPort int
	if w.IsContainer() {
		if srv, err := s.check(ctx, q, teamID, w); err == nil {
			// Its own transaction, committed before AwaitResult polls for it — see enqueueCommitted.
			op, err := s.enqueueCommitted(ctx, teamID, Actor{Via: "system"}, w, agentproto.KindContainerInspect,
				agentproto.ContainerInspectParams{Name: w.OriginKey, Label: w.Name}, "", nil)
			if err == nil {
				var res agentproto.ContainerInspectResult
				if _, err := s.AwaitResult(ctx, op, 8*time.Second, &res); err == nil {
					out.ContainerOK = true
					labels = res.Labels
					if len(res.Ports) > 0 {
						inspectedPort = firstContainerPort(res.Ports)
					}
				}
			}
			_ = srv
		}
	}

	// Repository: an OCI source label first, then a connected GitHub repo whose name matches the app.
	if src := labels["org.opencontainers.image.source"]; src != "" {
		out.RepoURL = AdoptField{Value: src, Confidence: "high", Source: "label:org.opencontainers.image.source"}
	} else if repo, ok := matchingGitHubRepo(ctx, s, q, teamID, a.Name); ok {
		out.RepoURL = AdoptField{Value: repo.URL, Confidence: "medium", Source: "github:name match"}
	} else {
		out.Missing = append(out.Missing, "repo_url")
	}

	// Branch: an OCI revision label if it looks like a branch (not a full SHA), else the matched
	// repo's default branch, else Hosbah's own default.
	if rev := labels["org.opencontainers.image.revision"]; rev != "" && !looksLikeCommitSHA(rev) {
		out.Branch = AdoptField{Value: rev, Confidence: "high", Source: "label:org.opencontainers.image.revision"}
	} else if repo, ok := matchingGitHubRepo(ctx, s, q, teamID, a.Name); ok && repo.DefaultBranch != "" {
		out.Branch = AdoptField{Value: repo.DefaultBranch, Confidence: "medium", Source: "github:default branch"}
	} else {
		out.Branch = AdoptField{Value: "main", Confidence: "low", Source: "default"}
	}

	// Build: Hosbah always detects Dockerfile vs Railpack at build time (app.detect); "auto" is a
	// correct, low-risk guess whenever nothing more specific is known.
	if _, present := labels["org.opencontainers.image.source"]; present {
		out.Build = AdoptField{Value: "auto", Confidence: "medium", Source: "detected at deploy time"}
	} else {
		out.Build = AdoptField{Value: "auto", Confidence: "low", Source: "default"}
	}

	// Port: the container's published port, or the origin's own record of one, else the app's own
	// current port (never zero).
	switch {
	case inspectedPort != 0:
		out.Port = AdoptField{Value: fmt.Sprint(inspectedPort), Confidence: "high", Source: "container.inspect"}
	case w.IsContainer() && len(importer.PublishedPorts(w.Origin.Ports)) > 0:
		out.Port = AdoptField{Value: fmt.Sprint(importer.PublishedPorts(w.Origin.Ports)[0].Port), Confidence: "medium", Source: "published port"}
	default:
		out.Port = AdoptField{Value: fmt.Sprint(a.Port), Confidence: "low", Source: "app's current port"}
	}

	return out, nil
}

func looksLikeCommitSHA(s string) bool {
	if len(s) < 7 {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}

func firstContainerPort(ports []string) int {
	for _, p := range ports {
		_, rhs, ok := strings.Cut(p, "-> ")
		if !ok {
			continue
		}
		_, portStr, ok := strings.Cut(rhs, ":")
		if !ok {
			continue
		}
		var n int
		if _, err := fmt.Sscanf(portStr, "%d", &n); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

// matchingGitHubRepo is a team's connected repo whose name matches an app's, best-effort (a missing
// or unconfigured GitHub App integration is not an error here — it just leaves the field unguessed).
func matchingGitHubRepo(ctx context.Context, s *Service, q *db.Queries, teamID, appName string) (GitHubAppRepo, bool) {
	insts, err := q.GitHubInstallationsOfTeam(ctx, teamID)
	if err != nil || len(insts) == 0 {
		return GitHubAppRepo{}, false
	}
	repos, err := s.AppRepos(ctx, teamID, insts)
	if err != nil {
		return GitHubAppRepo{}, false
	}
	for _, r := range repos {
		if strings.EqualFold(r.Name, appName) {
			return r, true
		}
	}
	return GitHubAppRepo{}, false
}
