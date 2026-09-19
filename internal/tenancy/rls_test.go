package tenancy

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/fadymondy/hosbah/internal/config"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/migrate"
)

// The row-level-security backstop, tested at the database with no handler in the way: this is
// what still holds when a query forgets its team filter.
func TestRowLevelSecurity(t *testing.T) {
	config.LoadDotEnv()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run the RLS tests")
	}
	sqlDB, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	ctx := context.Background()
	if err := migrate.Run(ctx, sqlDB, migrate.RepoDir(migrate.Dir), false, io.Discard); err != nil {
		t.Fatal(err)
	}

	// RLS is meaningless for a superuser or a BYPASSRLS role — refuse to pass vacuously.
	var super, bypass bool
	if err := sqlDB.QueryRowContext(ctx, `SELECT rolsuper, rolbypassrls FROM pg_roles WHERE rolname = current_user`).Scan(&super, &bypass); err != nil {
		t.Fatal(err)
	}
	if super || bypass {
		t.Fatalf("the app connects as a superuser/BYPASSRLS role — row-level security does not apply to it")
	}

	n := time.Now().UnixNano()
	ua, ub := fmt.Sprintf("rls-ua-%d", n), fmt.Sprintf("rls-ub-%d", n)
	ta, tb := fmt.Sprintf("rls-ta-%d", n), fmt.Sprintf("rls-tb-%d", n)
	mk := func(user, team string) {
		t.Helper()
		err := Tx(ctx, sqlDB, Scope{UserID: user, TeamID: team}, func(q *db.Queries, _ *sql.Tx) error {
			if _, err := q.InsertTeam(ctx, db.InsertTeamParams{ID: team, Name: team, Slug: team, OwnerID: user, DekWrapped: "x"}); err != nil {
				return err
			}
			if _, err := q.InsertTeamMember(ctx, db.InsertTeamMemberParams{TeamID: team, UserID: user, Role: RoleOwner}); err != nil {
				return err
			}
			_, err := q.InsertMcpToken(ctx, db.InsertMcpTokenParams{TeamID: team, UserID: user, Name: "t", TokenHash: "hash-" + team, Scopes: "read"})
			if err != nil {
				return err
			}
			return Record(ctx, q, team, user, "test", "team", team, nil)
		})
		if err != nil {
			t.Fatalf("seed %s: %v", team, err)
		}
	}
	mk(ua, ta)
	mk(ub, tb)

	count := func(s Scope, query string, args ...any) int {
		t.Helper()
		var c int
		err := Tx(ctx, sqlDB, s, func(_ *db.Queries, tx *sql.Tx) error {
			return tx.QueryRowContext(ctx, query, args...).Scan(&c)
		})
		if err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		return c
	}

	t.Run("no context sees no tenant rows", func(t *testing.T) {
		for _, table := range []string{"teams", "team_members", "invitations", "mcp_tokens", "activity_log", "reports", "admin_mcp_tokens"} {
			if c := count(Scope{}, "SELECT count(*) FROM "+table); c != 0 {
				t.Errorf("%s: %d rows visible with no tenant context", table, c)
			}
		}
		// The raw pool — no transaction, no settings — is the "forgot tenancy.Tx" case.
		var c int
		if err := sqlDB.QueryRowContext(ctx, "SELECT count(*) FROM teams").Scan(&c); err != nil || c != 0 {
			t.Errorf("the bare pool sees %d teams (err %v)", c, err)
		}
	})

	t.Run("team A's context cannot read team B", func(t *testing.T) {
		a := Scope{UserID: ua, TeamID: ta}
		for q, want := range map[string]int{
			"SELECT count(*) FROM teams WHERE id = $1":             0,
			"SELECT count(*) FROM team_members WHERE team_id = $1": 0,
			"SELECT count(*) FROM mcp_tokens WHERE team_id = $1":   0,
			"SELECT count(*) FROM activity_log WHERE team_id = $1": 0,
		} {
			if c := count(a, q, tb); c != want {
				t.Errorf("%s [team B] as team A: %d rows", q, c)
			}
		}
		// A query that forgets its WHERE still sees only A.
		if c := count(a, "SELECT count(*) FROM mcp_tokens"); c != 1 {
			t.Errorf("unfiltered mcp_tokens as team A: %d rows (want A's 1)", c)
		}
	})

	t.Run("team A's context cannot write team B", func(t *testing.T) {
		a := Scope{UserID: ua, TeamID: ta}
		var affected int64
		err := Tx(ctx, sqlDB, a, func(_ *db.Queries, tx *sql.Tx) error {
			res, err := tx.ExecContext(ctx, "UPDATE teams SET name = 'pwned' WHERE id = $1", tb)
			if err != nil {
				return err
			}
			affected, _ = res.RowsAffected()
			res, err = tx.ExecContext(ctx, "DELETE FROM mcp_tokens WHERE team_id = $1", tb)
			if err != nil {
				return err
			}
			d, _ := res.RowsAffected()
			affected += d
			return nil
		})
		if err != nil || affected != 0 {
			t.Errorf("updating/deleting team B as team A touched %d rows (err %v)", affected, err)
		}
		for name, stmt := range map[string]string{
			"member into B": "INSERT INTO team_members (team_id, user_id, role) VALUES ($1, 'intruder', 'owner')",
			"token into B":  "INSERT INTO mcp_tokens (team_id, user_id, name, token_hash, scopes) VALUES ($1, 'x', 'x', 'h-" + ta + "', 'read')",
			"audit into B":  "INSERT INTO activity_log (id, action, created_at, team_id) VALUES ('rls-" + ta + "', 'x', 'x', $1)",
		} {
			err := Tx(ctx, sqlDB, a, func(_ *db.Queries, tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, stmt, tb)
				return err
			})
			if err == nil || !strings.Contains(err.Error(), "row-level security") {
				t.Errorf("%s as team A: expected an RLS violation, got %v", name, err)
			}
		}
	})

	t.Run("a team can only be created by its owner, in its own context", func(t *testing.T) {
		err := Tx(ctx, sqlDB, Scope{UserID: ua, TeamID: "other"}, func(q *db.Queries, _ *sql.Tx) error {
			_, err := q.InsertTeam(ctx, db.InsertTeamParams{ID: "forged-" + ta, Name: "x", Slug: "forged-" + ta, OwnerID: ub, DekWrapped: "x"})
			return err
		})
		if err == nil {
			t.Error("inserted a team owned by someone else outside its context")
		}
	})

	t.Run("a token hash finds exactly its own token", func(t *testing.T) {
		if c := count(Scope{TokenHash: "hash-" + ta}, "SELECT count(*) FROM mcp_tokens"); c != 1 {
			t.Errorf("token-hash context sees %d tokens (want exactly 1)", c)
		}
	})

	t.Run("the audit log is append-only", func(t *testing.T) {
		var affected int64
		_ = Tx(ctx, sqlDB, Scope{UserID: ua, TeamID: ta}, func(_ *db.Queries, tx *sql.Tx) error {
			res, err := tx.ExecContext(ctx, "UPDATE activity_log SET action = 'rewritten' WHERE team_id = $1", ta)
			if err == nil {
				affected, _ = res.RowsAffected()
			}
			res, err = tx.ExecContext(ctx, "DELETE FROM activity_log WHERE team_id = $1", ta)
			if err == nil {
				d, _ := res.RowsAffected()
				affected += d
			}
			return nil
		})
		if affected != 0 {
			t.Errorf("rewrote/deleted %d audit rows", affected)
		}
	})
}
