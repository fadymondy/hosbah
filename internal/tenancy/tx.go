// Package tenancy is how Cloudy touches tenant data.
//
// Every read or write of a tenant row goes through Tx: one database transaction
// whose first statement pins the request's identity with set_config(..., true)
// — the function form of SET LOCAL — so the row-level-security policies in
// db/atlas/migrations/*_row_level_security.sql can see it. The setting dies with
// the transaction, so a pooled connection never carries one request's team into
// the next.
//
// Why a transaction per operation rather than a per-request connection hook:
// the togo kernel (v0.21.0) opens a plain database/sql pool (sql.Open in
// Kernel.SQL) and exposes no acquire/release hook, and the togo ORM takes a
// concrete *sql.DB, so neither can carry a session variable safely. sqlc's
// Queries accept a *sql.Tx, which is what the handlers use.
//
// Handlers still filter by team_id explicitly. RLS is the backstop for the query
// that forgets to — with no team in context, a tenant table returns nothing.
package tenancy

import (
	"context"
	"database/sql"
	"errors"

	db "github.com/fadymondy/cloudy/internal/db/gen"
)

// Scope is the identity a transaction runs as.
type Scope struct {
	UserID        string
	TeamID        string
	TokenHash     string // SHA-256 of a presented token (MCP bearer, invitation link)
	PlatformAdmin bool
}

// Tx runs fn in a transaction scoped to s. fn's error rolls it back.
func Tx(ctx context.Context, sqlDB *sql.DB, s Scope, fn func(q *db.Queries, tx *sql.Tx) error) error {
	if sqlDB == nil {
		return errors.New("tenancy: database unavailable")
	}
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit

	if err := apply(ctx, tx, s); err != nil {
		return err
	}
	if err := fn(db.New(tx), tx); err != nil {
		return err
	}
	return tx.Commit()
}

// SetTeam re-points an open transaction at another team — used when creating a
// team (its id must be in context before the row can be inserted and returned)
// and when accepting an invitation (the invitee joins the invitation's team).
func SetTeam(ctx context.Context, tx *sql.Tx, teamID string) error {
	_, err := tx.ExecContext(ctx, `SELECT set_config('app.team_id', $1, true)`, teamID)
	return err
}

func apply(ctx context.Context, tx *sql.Tx, s Scope) error {
	admin := ""
	if s.PlatformAdmin {
		admin = "on"
	}
	_, err := tx.ExecContext(ctx, `SELECT
		set_config('app.user_id', $1, true),
		set_config('app.team_id', $2, true),
		set_config('app.token_hash', $3, true),
		set_config('app.platform_admin', $4, true)`,
		s.UserID, s.TeamID, s.TokenHash, admin)
	return err
}
