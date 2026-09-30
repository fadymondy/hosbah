package circlexo

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// All queries here run on the app pool against the circlexo_* platform tables (see
// schema/zz_circlexo.sql), which have no row-level security and hold no tenant data, so they are
// plain SQL rather than sqlc. Anything that touches a tenant table goes through tenancy.Tx.

// OrgLink is a hub org and the team that serves it.
type OrgLink struct {
	OrgID    string
	OrgSlug  string
	TeamID   string
	TeamSlug string
	Locale   string
	Active   bool
}

const orgLinkSQL = `SELECT org_id, org_slug, team_id, team_slug, locale, status = 'active' FROM circlexo_org_links`

func scanOrgLink(row *sql.Row) (*OrgLink, error) {
	var l OrgLink
	if err := row.Scan(&l.OrgID, &l.OrgSlug, &l.TeamID, &l.TeamSlug, &l.Locale, &l.Active); err != nil {
		return nil, err
	}
	return &l, nil
}

// OrgByID returns the link for a hub org (any status); sql.ErrNoRows when there is none.
func (s *Service) OrgByID(ctx context.Context, orgID string) (*OrgLink, error) {
	return scanOrgLink(s.DB.QueryRowContext(ctx, orgLinkSQL+` WHERE org_id = $1`, orgID))
}

// OrgByTeam returns the link of a team (any status); sql.ErrNoRows when it has none.
func (s *Service) OrgByTeam(ctx context.Context, teamID string) (*OrgLink, error) {
	return scanOrgLink(s.DB.QueryRowContext(ctx, orgLinkSQL+` WHERE team_id = $1`, teamID))
}

// UserBySubject returns the Hosbah user linked to a hub subject; sql.ErrNoRows when unlinked.
func (s *Service) UserBySubject(ctx context.Context, subject string) (string, error) {
	var id string
	err := s.DB.QueryRowContext(ctx, `SELECT user_id FROM circlexo_user_links WHERE subject = $1`, subject).Scan(&id)
	return id, err
}

// linkUser records subject -> user. It reports the user the subject ends up linked to, which is
// not userID when another request linked it first (or, for an already-linked user, when they have
// a different hub account): the caller must compare, never assume.
func (s *Service) linkUser(ctx context.Context, subject, userID, email, via string) (string, error) {
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO circlexo_user_links (subject, user_id, email, linked_via) VALUES ($1, $2, $3, $4)
		ON CONFLICT DO NOTHING`, subject, userID, email, via)
	if err != nil {
		return "", err
	}
	got, err := s.UserBySubject(ctx, subject)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil // the user is linked to a different subject: the caller sees "" != userID
	}
	return got, err
}

func isUnique(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

func norm(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

// dedupe is the webhooks.Deduper over circlexo_webhook_events, so a retry is recognised by any
// API replica. Done runs only after the handler succeeded, so a failed event is retried.
type dedupe struct{ db *sql.DB }

func (d dedupe) Seen(ctx context.Context, id string) (bool, error) {
	if id == "" {
		return false, nil
	}
	var ok bool
	err := d.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM circlexo_webhook_events WHERE webhook_id = $1)`, id).Scan(&ok)
	return ok, err
}

func (d dedupe) Done(ctx context.Context, id string) error {
	if id == "" {
		return nil
	}
	_, err := d.db.ExecContext(ctx, `INSERT INTO circlexo_webhook_events (webhook_id) VALUES ($1) ON CONFLICT DO NOTHING`, id)
	if err == nil {
		// Old ids only matter inside the hub's retry window (days at most).
		_, _ = d.db.ExecContext(ctx, `DELETE FROM circlexo_webhook_events WHERE received_at < now() - interval '30 days'`)
	}
	return err
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
