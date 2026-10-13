package account

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/devices"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// POST /api/auth/device/refresh {refresh_token}: the phone and desktop apps' way to stay signed in
// (docs/apps-plan.md). The app registered itself with POST /api/me/devices right after an ordinary
// sign-in and kept the hbr_… token; here it trades that token for a fresh 24-hour session and a
// new refresh token. The old one stops working (compare-and-swap on its hash), so a copied token
// is good for at most one refresh, and the real app's next refresh signs the copy out.
//
// Refused, and the device forgotten, when the account was disabled, its sessions were revoked
// after the device signed in (same rule as web sessions: disabling and re-enabling an account
// never revives an app), or the device sat unused past devices.Window. Public route (the caller
// has no session yet), rate-limited per client like the other credential endpoints.
func (s *Service) handleDeviceRefresh(w http.ResponseWriter, r *http.Request) {
	if !s.tries.allow("refresh:" + s.clientIP(r)) {
		tooMany(w)
		return
	}
	var in struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, CodeInvalidRequest, "refresh_token is required")
		return
	}
	token := strings.TrimSpace(in.RefreshToken)
	if !devices.Valid(token) {
		fail(w, http.StatusUnauthorized, CodeUnauthorized, "sign in again")
		return
	}
	ctx := r.Context()
	oldHash := devices.Hash(token)
	newToken, newHash, err := devices.NewRefresh()
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "could not refresh")
		return
	}
	var (
		row     db.DeviceByRefreshHashRow
		refused string
	)
	err = tenancy.Tx(ctx, s.DB, tenancy.Scope{System: true}, func(q *db.Queries, _ *sql.Tx) error {
		var err error
		row, err = q.DeviceByRefreshHash(ctx, oldHash)
		if err != nil {
			return err
		}
		switch {
		case row.DisabledAt != nil:
			refused = CodeAccountDisabled
		case row.PanelServerID != nil || row.EmailUnverifiedAt != nil:
			refused = CodeUnauthorized
		case row.SessionsRevokedAt != nil && !row.CreatedAt.After(*row.SessionsRevokedAt):
			refused = "session_revoked"
		case s.Now().Sub(row.RefreshedAt) > devices.Window:
			refused = "session_expired"
		}
		if refused != "" {
			return q.DeleteDevice(ctx, row.ID)
		}
		n, err := q.RotateDeviceRefresh(ctx, db.RotateDeviceRefreshParams{NewHash: newHash, ID: row.ID, OldHash: oldHash})
		if err == nil && n == 0 {
			return sql.ErrNoRows // a concurrent refresh won with the same token
		}
		return err
	})
	switch {
	case isNoRows(err):
		fail(w, http.StatusUnauthorized, CodeUnauthorized, "sign in again")
		return
	case err != nil:
		s.Log.Error("device refresh failed", "err", err)
		fail(w, http.StatusInternalServerError, CodeInternal, "could not refresh")
		return
	case refused == CodeAccountDisabled:
		fail(w, http.StatusForbidden, refused, "this account is disabled")
		return
	case refused != "":
		fail(w, http.StatusUnauthorized, refused, "sign in again")
		return
	}
	// The plugin's IssueSession also sets the web's session cookie; an app has no use for it, so
	// it goes to a throwaway recorder.
	session, _, err := s.startSession(newRecorder(), &user{ID: row.UserID, Email: row.Email, Roles: row.Roles, Permissions: row.Permissions})
	if err != nil {
		fail(w, http.StatusInternalServerError, CodeInternal, "could not refresh")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token": session, "refresh_token": newToken, "device_id": row.ID,
		"expires_at": s.Now().Add(24 * time.Hour).UTC(),
	})
}
