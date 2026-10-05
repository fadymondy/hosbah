package circlexo

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	circlexo "github.com/circlexo/circlexo-go"
)

/*
Profile sync (MH-1149). The hub owns who a person is. Hosbah shows people by their address, so it
copies the hub's address onto the account on every CircleXO sign-in (from the ID token) and
whenever the hub sends user.updated. A new address is taken only when the hub says it is verified
and no other Hosbah account holds it.
*/

// SyncProfile overwrites userID's copy of their hub profile. Failures are logged: a sign-in never
// fails because of it.
func (s *Service) SyncProfile(ctx context.Context, userID string, p circlexo.Profile) {
	email := strings.ToLower(strings.TrimSpace(p.Email))
	if email == "" || !p.EmailVerified {
		return
	}
	var cur string
	if err := s.DB.QueryRowContext(ctx, `SELECT email FROM users WHERE id = $1`, userID).Scan(&cur); err != nil || strings.EqualFold(cur, email) {
		return
	}
	var other string
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM users WHERE lower(email) = $1 AND id <> $2`, email, userID).Scan(&other)
	if err == nil {
		s.Log.Warn("circlexo: the hub's address belongs to another Hosbah account; keeping the old one", "user", userID)
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE users SET email = $2 WHERE id = $1`, userID, email); err != nil {
		s.Log.Warn("circlexo: syncing the address", "user", userID, "err", err)
		return
	}
	_, _ = s.DB.ExecContext(ctx, `UPDATE circlexo_user_links SET email = $2 WHERE user_id = $1`, userID, email)
}
