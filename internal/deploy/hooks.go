package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"strings"

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
func (s *Service) GitHub() GitHubApp { return s.github }

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
	if !strings.HasPrefix(token, HookTokenPrefix) || len(token) > 100 {
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
		status = http.StatusNotFound
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
		d, err := s.QueueDeployment(r.Context(), q, a.TeamID, Actor{Via: "hook"}, a, "deploy", ref, nil)
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

// handleGitHubWebhook: push events of installed repositories deploy every app on that repo and branch.
func (s *Service) handleGitHubWebhook(w http.ResponseWriter, r *http.Request) {
	if !s.github.Configured() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": ErrGitHubNotConfigured.Error()})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 25<<20))
	if err != nil || !s.github.VerifyWebhook(body, r.Header.Get("X-Hub-Signature-256")) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "bad signature"})
		return
	}
	if r.Header.Get("X-GitHub-Event") != "push" {
		writeJSON(w, http.StatusAccepted, map[string]string{"skipped": "not a push"})
		return
	}
	var push struct {
		Ref        string `json:"ref"`
		Deleted    bool   `json:"deleted"`
		Repository struct {
			HTMLURL string `json:"html_url"`
		} `json:"repository"`
	}
	if json.Unmarshal(body, &push) != nil || push.Deleted || !strings.HasPrefix(push.Ref, "refs/heads/") {
		writeJSON(w, http.StatusAccepted, map[string]string{"skipped": "not a branch push"})
		return
	}
	queued := 0
	ctx := r.Context()
	_ = tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{System: true}, func(q *db.Queries, _ *sql.Tx) error {
		apps, err := q.AppsForRepo(ctx, db.AppsForRepoParams{RepoURL: agentproto.NormalizeRepoURL(push.Repository.HTMLURL), Branch: strings.TrimPrefix(push.Ref, "refs/heads/")})
		if err != nil {
			return err
		}
		for _, a := range apps {
			if err := s.queueForTeam(ctx, a); err == nil {
				queued++
			}
		}
		return nil
	})
	writeJSON(w, http.StatusAccepted, map[string]int{"queued": queued})
}

func (s *Service) queueForTeam(ctx context.Context, a db.App) error {
	return tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{TeamID: a.TeamID}, func(q *db.Queries, _ *sql.Tx) error {
		_, err := s.QueueDeployment(ctx, q, a.TeamID, Actor{Via: "github"}, a, "deploy", BranchOf(a), nil)
		return err
	})
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
