package deploy

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
	"github.com/fadymondy/hosbah/internal/tokens"
)

// MountPublic adds the routes outside /api, each authenticated by its own means (never a session):
//
//	POST /hooks/deploy/{token}  an app's deploy hook — the token (only its hash is stored)
//	POST /hooks/github          the GitHub App's webhook — X-Hub-Signature-256; 503 until configured
//	GET  /internal/tls/allow    Caddy's on-demand TLS permission check — answers only yes/no
func (s *Service) MountPublic(r chi.Router) {
	r.Post("/hooks/deploy/{token}", s.handleDeployHook)
	r.Post("/hooks/github", s.handleGitHubWebhook)
	r.Get("/internal/tls/allow", s.handleTLSAllow)
}

// GitHub is the GitHub App integration (unconfigured until the owner supplies its credentials).
func (s *Service) GitHub() GitHubApp { return s.gh() }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// handleDeployHook queues a deploy of the app whose token this is. A git host's push payload is
// understood: a push to another branch, or a tag, is acknowledged and skipped. A plain
// {"ref": "<branch|tag|sha>"} deploys that ref; no body deploys the app's branch.
func (s *Service) handleDeployHook(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if !tokens.HasPrefix(token, tokens.Hook) || len(token) > 100 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	var payload struct {
		Ref string `json:"ref"`
	}
	_ = json.Unmarshal(body, &payload)

	hash := tokens.Hash(token)
	var (
		status     = http.StatusNotFound
		out    any = map[string]string{"error": "not found"}
	)
	err := tenancy.Tx(r.Context(), s.a.SQLDB, tenancy.Scope{TokenHash: hash}, func(q *db.Queries, tx *sql.Tx) error {
		a, err := q.AppByDeployHook(r.Context(), &hash)
		if err != nil {
			return nil
		}
		if a.AutodeployPaused {
			status, out = http.StatusConflict, map[string]string{"error": "deploys from the hook are paused for this app"}
			return nil
		}
		ref := BranchOf(a)
		switch p := payload.Ref; {
		case strings.HasPrefix(p, "refs/heads/"):
			if b := strings.TrimPrefix(p, "refs/heads/"); b != BranchOf(a) {
				status, out = http.StatusAccepted, map[string]string{"skipped": "a push to " + b + ", not " + BranchOf(a)}
				return nil
			}
		case strings.HasPrefix(p, "refs/"):
			status, out = http.StatusAccepted, map[string]string{"skipped": "not a branch push"}
			return nil
		case p != "":
			ref = p
		}
		if err := tenancy.SetTeam(r.Context(), tx, a.TeamID); err != nil {
			return err
		}
		d, err := s.QueueDeployment(r.Context(), q, a.TeamID, Actor{Via: "hook"}, a, "deploy", ref, nil, "")
		if err != nil {
			if ae, ok := err.(*ActionError); ok {
				status, out = ae.Status, map[string]string{"error": ae.Msg}
				return nil
			}
			return err
		}
		status, out = http.StatusAccepted, map[string]any{"deployment_id": d.ID, "number": d.Number, "ref": d.Ref, "status": d.Status}
		return nil
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not queue the deploy"})
		return
	}
	writeJSON(w, status, out)
}

// handleGitHubWebhook is the GitHub App's webhook. Every delivery must carry a valid
// X-Hub-Signature-256; then:
//
//	push                        the installation's team deploys its apps on that repository and
//	                            branch (never another team's, whatever their repository URL says),
//	                            except those with automatic deploys paused — once per X-GitHub-Delivery
//	installation                deleted → the row goes; suspend / unsuspend → suspended_at
//	installation_repositories   the repository selection follows; the team's listing is refreshed
//
// Installation events reach every GitHub App whatever it subscribes to. A new installation is never
// stored from a webhook: only the setup callback knows which team it is for.
func (s *Service) handleGitHubWebhook(w http.ResponseWriter, r *http.Request) {
	g := s.gh()
	if !g.Configured() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": ErrGitHubNotConfigured.Error()})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 25<<20))
	if err != nil || !g.VerifyWebhook(body, r.Header.Get("X-Hub-Signature-256")) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "bad signature"})
		return
	}
	var ev struct {
		Action       string `json:"action"`
		Installation *struct {
			ID                  int64      `json:"id"`
			RepositorySelection string     `json:"repository_selection"`
			SuspendedAt         *time.Time `json:"suspended_at"`
		} `json:"installation"`
		RepositorySelection string `json:"repository_selection"`
	}
	_ = json.Unmarshal(body, &ev)
	ctx := r.Context()
	switch r.Header.Get("X-GitHub-Event") {
	case "push":
		s.githubPush(w, r, body)
	case "installation":
		if ev.Installation == nil || ev.Installation.ID <= 0 {
			writeJSON(w, http.StatusAccepted, map[string]string{"skipped": "no installation"})
			return
		}
		id := ev.Installation.ID
		team := ""
		err := s.sys(ctx, func(q *db.Queries) error {
			var err error
			switch ev.Action {
			case "deleted":
				team, err = q.DeleteGitHubInstallationByInstallationID(ctx, id)
			case "suspend":
				at := time.Now().UTC()
				if ev.Installation.SuspendedAt != nil {
					at = *ev.Installation.SuspendedAt
				}
				team, err = q.SetGitHubInstallationSuspended(ctx, db.SetGitHubInstallationSuspendedParams{SuspendedAt: &at, InstallationID: id})
			case "unsuspend":
				team, err = q.SetGitHubInstallationSuspended(ctx, db.SetGitHubInstallationSuspendedParams{InstallationID: id})
			default:
				return nil
			}
			if errors.Is(err, sql.ErrNoRows) {
				return nil // not connected to any team
			}
			return err
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not record the change"})
			return
		}
		if team != "" {
			s.ForgetAppRepos(team)
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"installation": ev.Action, "applied": team != ""})
	case "installation_repositories":
		if ev.Installation == nil || ev.Installation.ID <= 0 {
			writeJSON(w, http.StatusAccepted, map[string]string{"skipped": "no installation"})
			return
		}
		sel := ev.RepositorySelection
		if sel == "" {
			sel = ev.Installation.RepositorySelection
		}
		if sel != "all" && sel != "selected" {
			sel = "selected"
		}
		team := ""
		err := s.sys(ctx, func(q *db.Queries) error {
			var err error
			team, err = q.SetGitHubInstallationSelection(ctx, db.SetGitHubInstallationSelectionParams{RepositorySelection: sel, InstallationID: ev.Installation.ID})
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not record the change"})
			return
		}
		if team != "" {
			s.ForgetAppRepos(team)
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"repositories": ev.Action, "applied": team != ""})
	default:
		writeJSON(w, http.StatusAccepted, map[string]string{"skipped": "not handled"})
	}
}

// githubPush deploys the installation's team's apps on the pushed repository and branch, once per
// delivery: the delivery id is recorded in the same transaction as the deploys it queued, so a
// redelivery of a push that queued finds it and does nothing, and one that failed is retried.
func (s *Service) githubPush(w http.ResponseWriter, r *http.Request, body []byte) {
	var push struct {
		Ref          string `json:"ref"`
		Deleted      bool   `json:"deleted"`
		Installation *struct {
			ID int64 `json:"id"`
		} `json:"installation"`
		Repository struct {
			HTMLURL string `json:"html_url"`
		} `json:"repository"`
	}
	if json.Unmarshal(body, &push) != nil || push.Deleted || !strings.HasPrefix(push.Ref, "refs/heads/") {
		writeJSON(w, http.StatusAccepted, map[string]string{"skipped": "not a branch push"})
		return
	}
	if push.Installation == nil || push.Installation.ID <= 0 {
		writeJSON(w, http.StatusAccepted, map[string]string{"skipped": "not from an installation"})
		return
	}
	delivery := strings.TrimSpace(r.Header.Get("X-GitHub-Delivery"))
	if delivery == "" || len(delivery) > 100 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no X-GitHub-Delivery"})
		return
	}
	ctx := r.Context()
	var inst db.GithubInstallation
	found := false
	if err := s.sys(ctx, func(q *db.Queries) error {
		row, err := q.GitHubInstallationByInstallationID(ctx, push.Installation.ID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		inst, found = row, err == nil
		return err
	}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "lookup failed"})
		return
	}
	if !found {
		writeJSON(w, http.StatusAccepted, map[string]string{"skipped": "installation not connected to a team"})
		return
	}
	if inst.SuspendedAt != nil {
		writeJSON(w, http.StatusAccepted, map[string]string{"skipped": "installation suspended"})
		return
	}
	repoURL := agentproto.NormalizeRepoURL(push.Repository.HTMLURL)
	branch := strings.TrimPrefix(push.Ref, "refs/heads/")
	queued, duplicate := 0, false
	err := tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{System: true, TeamID: inst.TeamID}, func(q *db.Queries, _ *sql.Tx) error {
		n, err := q.RecordGitHubDelivery(ctx, db.RecordGitHubDeliveryParams{DeliveryID: delivery, Event: "push"})
		if err != nil {
			return err
		}
		if n == 0 {
			duplicate = true
			return nil
		}
		_ = q.PruneGitHubDeliveries(ctx)
		apps, err := q.AppsForRepoInTeam(ctx, db.AppsForRepoInTeamParams{TeamID: inst.TeamID, RepoURL: repoURL, Branch: branch})
		if err != nil {
			return err
		}
		for _, a := range apps {
			if _, err := s.QueueDeployment(ctx, q, inst.TeamID, Actor{Via: "github"}, a, "deploy", BranchOf(a), nil, ""); err != nil {
				var ae *ActionError
				if errors.As(err, &ae) {
					continue // this app cannot deploy now (its page says why); the others still do
				}
				return err
			}
			queued++
		}
		return nil
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not queue the deploys"})
		return
	}
	if duplicate {
		writeJSON(w, http.StatusAccepted, map[string]any{"queued": 0, "duplicate": true})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]int{"queued": queued})
}

// handleTLSAllow is Caddy's `ask`: 200 only for a hostname that is routed to an app, set to auto
// TLS, and verified — so nobody can make a Hosbah server request certificates for names they point
// at it without proving they control them.
func (s *Service) handleTLSAllow(w http.ResponseWriter, r *http.Request) {
	host := NormalizeHostname(r.URL.Query().Get("domain"))
	allowed := false
	if agentproto.ValidHostname(host) {
		_ = tenancy.Tx(r.Context(), s.a.SQLDB, tenancy.Scope{System: true}, func(q *db.Queries, _ *sql.Tx) error {
			n, err := q.TLSAllowed(r.Context(), host)
			allowed = err == nil && n > 0
			return err
		})
	}
	w.Header().Set("Cache-Control", "no-store")
	if !allowed {
		http.Error(w, "not allowed", http.StatusNotFound)
		return
	}
	_, _ = w.Write([]byte("ok"))
}
