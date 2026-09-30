package circlexo

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

/*
Ending sessions from the hub.

Hosbah's own session is a stateless JWT, so "revoke session <sid>" needs something to look up. At
hub sign-in we remember (hub sid, user, the JWT's iat); session.revoked marks those rows revoked;
and the account gate (server/accounts.go) refuses a request whose session's (user, iat) matches a
revoked row. Only sessions that came in through the hub are ever in this table, so a password
session of the same user is unaffected. Known limit: two sessions of one user minted in the same
second share an iat, so revoking one ends both, which is harmless for a sign-out.
*/

// jwtIssuedAt reads the iat of a JWT the auth plugin just minted (no verification: we made it).
func jwtIssuedAt(token string) (time.Time, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, false
	}
	var c struct {
		Iat float64 `json:"iat"`
	}
	if json.Unmarshal(raw, &c) != nil || c.Iat == 0 {
		return time.Time{}, false
	}
	return time.Unix(int64(c.Iat), 0), true
}

// recordSession remembers a hub-born Hosbah session so a later session.revoked can find it.
func (s *Service) recordSession(ctx context.Context, sid, userID, jwt string) {
	iat, ok := jwtIssuedAt(jwt)
	if !ok || sid == "" {
		return
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO circlexo_sessions (sid, user_id, issued_at) VALUES ($1, $2, $3)`, sid, userID, iat); err != nil {
		s.Log.Error("circlexo: recording the session", "err", err)
		return
	}
	// A session cannot outlive the JWT lifetime, which is far under this.
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM circlexo_sessions WHERE created_at < now() - interval '60 days'`)
}

// revokeSession ends the hub-born sessions with this sid; with no sid, all of the subject's.
func (s *Service) revokeSession(ctx context.Context, subject, sid string) error {
	if sid != "" {
		_, err := s.DB.ExecContext(ctx, `UPDATE circlexo_sessions SET revoked_at = now() WHERE sid = $1 AND revoked_at IS NULL`, sid)
		return err
	}
	if subject == "" {
		return nil
	}
	_, err := s.DB.ExecContext(ctx, `
		UPDATE circlexo_sessions SET revoked_at = now() WHERE revoked_at IS NULL
		AND user_id = (SELECT user_id FROM circlexo_user_links WHERE subject = $1)`, subject)
	return err
}

// SessionEnded reports whether a Hosbah session (user, JWT issue time) was born from a hub session
// that the hub has since revoked. Cheap and index-backed; the account gate calls it only when the
// integration is on.
func SessionEnded(ctx context.Context, s *Service, userID string, issuedAt time.Time) bool {
	if s == nil {
		return false
	}
	var hit bool
	err := s.DB.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM circlexo_sessions WHERE user_id = $1 AND issued_at = $2 AND revoked_at IS NOT NULL)`,
		userID, issuedAt.UTC().Truncate(time.Second)).Scan(&hit)
	return err == nil && hit
}
