package rum

import (
	_ "embed"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
	"github.com/fadymondy/hosbah/internal/tokens"
)

//go:embed beacon.js
var beaconJS []byte

// MountPublic adds the two anonymous RUM routes, outside /api/teams:
//
//	GET  /rum.js          the beacon script (internal/server/authorization.go: public, outside /api/)
//	POST /api/rum/{key}   the ingest endpoint (internal/server/authorization.go: isPublic)
//
// The OPTIONS route registered here for the ingest endpoint's preflight is never actually reached:
// togo's auth plugin installs a credential-aware CORS middleware with chi's Router.Use (so it runs
// inside every kernel-level middleware, ahead of route dispatch) that answers every OPTIONS request
// itself, with Access-Control-Allow-Origin only for origins on the CORS_ORIGINS allowlist — which an
// arbitrary hosted app's own domain never is. Left registered anyway as the documented shape and a
// harmless fallback; CorsPreflight below is what a real cross-origin beacon actually hits, wired in
// ahead of chi's router (internal/server/server.go, ahead of requireAuthorization).
func (s *Service) MountPublic(r chi.Router) {
	r.Get("/rum.js", s.handleBeacon)
	r.Options("/api/rum/{key}", s.handleIngestPreflight)
	r.Post("/api/rum/{key}", s.handleIngest)
}

// CorsPreflight answers the ingest endpoint's own CORS preflight before it reaches chi's router (and
// so before togo's auth-plugin CORS middleware, mounted with chi's Router.Use, which would otherwise
// answer every OPTIONS request first — see the comment on MountPublic). Register with
// Kernel.UseMiddleware, not chi's Router.Use.
func (s *Service) CorsPreflight(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions && strings.HasPrefix(r.URL.Path, "/api/rum/") {
			s.handleIngestPreflight(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Service) handleBeacon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	_, _ = w.Write(beaconJS)
}

func (s *Service) handleIngestPreflight(w http.ResponseWriter, r *http.Request) {
	corsHeaders(w)
	w.WriteHeader(http.StatusNoContent)
}

func corsHeaders(w http.ResponseWriter) {
	// Wide open on purpose: the ingest key is not a secret (it travels in the page source of every
	// visitor's browser), and the app's origin is whatever domain the site owner put the snippet on —
	// unknown to the control plane ahead of time.
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	h.Set("Access-Control-Allow-Headers", "Content-Type")
	h.Set("Access-Control-Max-Age", "600")
}

// ingestIn is the beacon's POST body. Every field is optional except path and device: a browser
// that supports none of the PerformanceObserver entry types still reports those two.
type ingestIn struct {
	Path          string   `json:"path"`
	Device        string   `json:"device"`
	EffectiveType string   `json:"effective_type"`
	LCPMs         *float64 `json:"lcp_ms"`
	CLS           *float64 `json:"cls"`
	INPMs         *float64 `json:"inp_ms"`
	FCPMs         *float64 `json:"fcp_ms"`
	TTFBMs        *float64 `json:"ttfb_ms"`
}

// handleIngest never distinguishes "no such key" from "RUM disabled" from "bad body" in its
// response — all of them are 204 with no body, so a probing script learns nothing, and a legitimate
// beacon (fire-and-forget, sendBeacon ignores the response anyway) never retries. Never logs or
// stores the request's IP or User-Agent.
func (s *Service) handleIngest(w http.ResponseWriter, r *http.Request) {
	corsHeaders(w)
	key := chi.URLParam(r, "key")
	if !tokens.HasPrefix(key, tokens.Rum) || len(key) > 100 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !s.lim.allow(key) {
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil { // over the cap, or the connection dropped
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var in ingestIn
	if json.Unmarshal(raw, &in) != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !validIngest(in) {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	hash := tokens.Hash(key)
	ctx := r.Context()
	err = tenancy.Tx(ctx, s.db, tenancy.Scope{TokenHash: hash}, func(q *db.Queries, tx *sql.Tx) error {
		setting, err := q.AppRumSettingByKeyHash(ctx, &hash)
		if errors.Is(err, sql.ErrNoRows) {
			return nil // unknown key: same 204 as everything else
		}
		if err != nil {
			return err
		}
		if !setting.Enabled {
			return nil
		}
		if err := tenancy.SetTeam(ctx, tx, setting.TeamID); err != nil {
			return err
		}
		return q.InsertRumSampleInTeam(ctx, db.InsertRumSampleInTeamParams{
			AppID: setting.AppID, TeamID: setting.TeamID, Path: in.Path, Device: in.Device,
			EffectiveType: nullIfEmpty(in.EffectiveType),
			LcpMs:         nullFloat(in.LCPMs), Cls: nullFloat(in.CLS), InpMs: nullFloat(in.INPMs),
			FcpMs: nullFloat(in.FCPMs), TtfbMs: nullFloat(in.TTFBMs),
		})
	})
	if err != nil {
		s.log.Warn("rum ingest failed", "err", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

const maxIngestValue = 300000 // ms; generously above any real paint/interaction timing

func validIngest(in ingestIn) bool {
	if in.Device != "mobile" && in.Device != "desktop" {
		return false
	}
	p := in.Path
	if p == "" || len(p) > 300 || strings.ContainsAny(p, "?#") || !strings.HasPrefix(p, "/") {
		return false
	}
	if len(in.EffectiveType) > 20 {
		return false
	}
	for _, v := range []*float64{in.LCPMs, in.INPMs, in.FCPMs, in.TTFBMs} {
		if v != nil && (*v < 0 || *v > maxIngestValue) {
			return false
		}
	}
	if in.CLS != nil && (*in.CLS < 0 || *in.CLS > 100) {
		return false
	}
	return true
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nullFloat(v *float64) sql.NullFloat64 {
	if v == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *v, Valid: true}
}
