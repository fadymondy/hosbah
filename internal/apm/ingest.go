package apm

import (
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
	"github.com/fadymondy/hosbah/internal/tokens"
)

// MountPublic adds the two anonymous OTLP/HTTP JSON ingest routes, outside /api/teams:
//
//	POST /api/otlp/v1/traces
//	POST /api/otlp/v1/logs
//
// Both take `Authorization: Bearer hbt_...` (or `x-hosbah-key: hbt_...`), the app's own secret APM
// ingest key — unlike RUM's key this one is a real bearer secret, so unlike rum.Service there is no
// public, anonymous beacon route here at all; only an app's own server-side OTel SDK holds the key.
func (s *Service) MountPublic(r chi.Router) {
	r.Post("/api/otlp/v1/traces", s.handleTraces)
	r.Post("/api/otlp/v1/logs", s.handleLogs)
}

// resolvedKey is the app/team an ingest key resolved to.
type resolvedKey struct {
	appID, teamID string
}

// authenticate reads the bearer key from the request, rate-limits it, and resolves it to an
// enabled app_apm_settings row. Every failure path returns the same shape of response the caller
// already decided on (401/400) without leaking which part failed.
func (s *Service) authenticate(r *http.Request) (resolvedKey, int, error) {
	key := bearerKey(r)
	if key == "" || !tokens.HasPrefix(key, tokens.Apm) || len(key) > 100 {
		return resolvedKey{}, http.StatusUnauthorized, errors.New("missing or invalid key")
	}
	if !s.lim.allow(key) {
		return resolvedKey{}, http.StatusTooManyRequests, errors.New("rate limited")
	}
	hash := tokens.Hash(key)
	ctx := r.Context()
	var out resolvedKey
	err := tenancy.Tx(ctx, s.db, tenancy.Scope{TokenHash: hash}, func(q *db.Queries, _ *sql.Tx) error {
		row, err := q.AppApmSettingByKeyHash(ctx, &hash)
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("unknown key")
		}
		if err != nil {
			return err
		}
		if !row.Enabled {
			return errors.New("apm disabled")
		}
		out = resolvedKey{appID: row.AppID, teamID: row.TeamID}
		return nil
	})
	if err != nil {
		return resolvedKey{}, http.StatusUnauthorized, err
	}
	return out, 0, nil
}

func bearerKey(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	if k := r.Header.Get("x-hosbah-key"); k != "" {
		return strings.TrimSpace(k)
	}
	return ""
}

// readBody applies the size cap and transparent gzip decompression an OTel SDK's OTLP/HTTP JSON
// exporter may use (Content-Encoding: gzip).
func readBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
	var reader io.Reader = r.Body
	if strings.EqualFold(r.Header.Get("Content-Encoding"), "gzip") {
		gz, err := gzip.NewReader(reader)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		reader = http.MaxBytesReader(w, io.NopCloser(gz), MaxBodyBytes)
	}
	return io.ReadAll(reader)
}

func (s *Service) handleTraces(w http.ResponseWriter, r *http.Request) {
	rk, status, err := s.authenticate(r)
	if err != nil {
		http.Error(w, "{}", status)
		return
	}
	raw, err := readBody(w, r)
	if err != nil {
		http.Error(w, "{}", http.StatusBadRequest)
		return
	}
	var payload TracesPayload
	if json.Unmarshal(raw, &payload) != nil {
		http.Error(w, "{}", http.StatusBadRequest)
		return
	}
	spans, exceptions := ReduceTraces(payload)
	if err := s.store(r.Context(), rk, spans, exceptions); err != nil {
		s.log.Warn("apm traces ingest failed", "err", err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("{}"))
}

func (s *Service) handleLogs(w http.ResponseWriter, r *http.Request) {
	rk, status, err := s.authenticate(r)
	if err != nil {
		http.Error(w, "{}", status)
		return
	}
	raw, err := readBody(w, r)
	if err != nil {
		http.Error(w, "{}", http.StatusBadRequest)
		return
	}
	var payload LogsPayload
	if json.Unmarshal(raw, &payload) != nil {
		http.Error(w, "{}", http.StatusBadRequest)
		return
	}
	exceptions := ReduceLogs(payload)
	if err := s.store(r.Context(), rk, nil, exceptions); err != nil {
		s.log.Warn("apm logs ingest failed", "err", err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("{}"))
}

// store writes the reduced spans and groups the exception occurrences into apm_issues, all in the
// resolved app's team scope. Best-effort per item: one bad row never drops the rest of the batch.
func (s *Service) store(ctx context.Context, rk resolvedKey, spans []ReducedSpan, exceptions []ExceptionOccurrence) error {
	if len(spans) == 0 && len(exceptions) == 0 {
		return nil
	}
	return tenancy.Tx(ctx, s.db, tenancy.Scope{TeamID: rk.teamID}, func(q *db.Queries, _ *sql.Tx) error {
		for _, sp := range spans {
			attrsJSON := "{}"
			if sp.Attrs != nil {
				if b, err := json.Marshal(sp.Attrs); err == nil {
					attrsJSON = string(b)
				}
			}
			_ = q.InsertApmSpanInTeam(ctx, db.InsertApmSpanInTeamParams{
				TeamID: rk.teamID, AppID: rk.appID, TraceID: sp.TraceID, SpanID: sp.SpanID,
				ParentSpanID: nullIfEmptyStr(sp.ParentSpanID), Name: sp.Name, Kind: sp.Kind, Service: sp.Service,
				HttpMethod: nullIfEmptyStr(sp.HTTPMethod), HttpRoute: nullIfEmptyStr(sp.HTTPRoute),
				HttpStatus: nullInt32(sp.HTTPStatus), DurationMs: float32(sp.DurationMs), Error: sp.Error,
				StartTime: orNow(sp.StartTime, s.Now()), Attrs: attrsJSON,
			})
		}
		for _, ex := range exceptions {
			if ex.Type == "" {
				continue
			}
			fp := Fingerprint(ex.Type, ex.Message, ex.Stack)
			at := orNow(ex.At, s.Now())
			issue, err := q.UpsertApmIssueSeen(ctx, db.UpsertApmIssueSeenParams{
				TeamID: rk.teamID, AppID: rk.appID, Fingerprint: fp, Type: ex.Type, Message: ex.Message, SeenAt: at,
			})
			if err != nil {
				continue
			}
			_ = q.InsertApmExceptionEventInTeam(ctx, db.InsertApmExceptionEventInTeamParams{
				TeamID: rk.teamID, AppID: rk.appID, IssueID: issue.ID,
				TraceID: nullIfEmptyStr(ex.TraceID), SpanID: nullIfEmptyStr(ex.SpanID),
				Type: ex.Type, Message: ex.Message, Stacktrace: ex.Stack,
			})
		}
		return nil
	})
}

func nullIfEmptyStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nullInt32(v int) sql.NullInt32 {
	if v == 0 {
		return sql.NullInt32{}
	}
	return sql.NullInt32{Int32: int32(v), Valid: true}
}

func orNow(t, now time.Time) time.Time {
	if t.IsZero() {
		return now
	}
	return t
}
