package tenancy

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"github.com/fadymondy/cloudy/internal/config"
	db "github.com/fadymondy/cloudy/internal/db/gen"
	"github.com/fadymondy/cloudy/internal/migrate"
)

// P1's tables under row-level security, including the two new contexts: an agent's ServerID
// scope sees its own server only, and nothing at all is visible without a context.
func TestRowLevelSecurityP1(t *testing.T) {
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

	n := time.Now().UnixNano()
	type fixture struct{ user, team, server, op string }
	mk := func(tag string) fixture {
		f := fixture{user: fmt.Sprintf("p1-u%s-%d", tag, n), team: fmt.Sprintf("p1-t%s-%d", tag, n),
			server: fmt.Sprintf("p1-s%s-%d", tag, n), op: fmt.Sprintf("p1-o%s-%d", tag, n)}
		err := Tx(ctx, sqlDB, Scope{UserID: f.user, TeamID: f.team}, func(q *db.Queries, tx *sql.Tx) error {
			if _, err := q.InsertTeam(ctx, db.InsertTeamParams{ID: f.team, Name: f.team, Slug: f.team, OwnerID: f.user, DekWrapped: "x"}); err != nil {
				return err
			}
			if _, err := q.InsertServer(ctx, db.InsertServerParams{ID: f.server, TeamID: f.team, Name: "s", Provider: "byo", Kind: "byo", Status: "enrolled", CreatedBy: f.user}); err != nil {
				return err
			}
			if _, err := q.InsertOperation(ctx, db.InsertOperationParams{ID: f.op, TeamID: f.team, ServerID: f.server, Kind: "system.info",
				Executor: "agent", Params: "{}", ParamsHash: "h", CreatedBy: f.user, TimeoutSeconds: 60, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
				return err
			}
			_, err := q.InsertCredential(ctx, db.InsertCredentialParams{ID: "cred-" + f.team, TeamID: f.team, Provider: "proxmox", Name: "c", Config: "{}", SecretSealed: "x", CreatedBy: f.user})
			return err
		})
		if err != nil {
			t.Fatalf("seed %s: %v", tag, err)
		}
		// A team (a browser session) cannot append log lines; only agents and the control plane can.
		if err := Tx(ctx, sqlDB, Scope{UserID: f.user, TeamID: f.team}, func(q *db.Queries, _ *sql.Tx) error {
			_, err := q.InsertOperationLog(ctx, db.InsertOperationLogParams{TeamID: f.team, ServerID: f.server, OperationID: f.op, Seq: 1, Stream: "info", LineSealed: "x"})
			return err
		}); err == nil {
			t.Fatalf("a team context appended an operation log line")
		}
		// The agent appends its own lines.
		if err := Tx(ctx, sqlDB, Scope{ServerID: f.server}, func(q *db.Queries, _ *sql.Tx) error {
			_, err := q.InsertOperationLog(ctx, db.InsertOperationLogParams{TeamID: f.team, ServerID: f.server, OperationID: f.op, Seq: 1, Stream: "info", LineSealed: "x"})
			return err
		}); err != nil {
			t.Fatalf("agent append %s: %v", tag, err)
		}
		return f
	}
	a, b := mk("a"), mk("b")

	count := func(s Scope, q string, args ...any) int {
		t.Helper()
		var c int
		if err := Tx(ctx, sqlDB, s, func(_ *db.Queries, tx *sql.Tx) error { return tx.QueryRowContext(ctx, q, args...).Scan(&c) }); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return c
	}
	tables := []string{"servers", "agent_enrollments", "operations", "operation_logs", "credentials"}

	for _, tbl := range tables {
		if c := count(Scope{}, "SELECT count(*) FROM "+tbl); c != 0 {
			t.Errorf("%s: %d rows visible with no context", tbl, c)
		}
	}
	// Team A sees none of B's rows, even with an unfiltered query.
	for _, tbl := range []string{"servers", "operations", "operation_logs", "credentials"} {
		if c := count(Scope{UserID: a.user, TeamID: a.team}, "SELECT count(*) FROM "+tbl+" WHERE team_id = $1", b.team); c != 0 {
			t.Errorf("%s: team A sees %d of team B's rows", tbl, c)
		}
	}
	// An agent sees its own server and operations — nothing of another server, not even its credentials.
	agentA := Scope{ServerID: a.server}
	for q, want := range map[string]int{
		"SELECT count(*) FROM servers":        1,
		"SELECT count(*) FROM operations":     1,
		"SELECT count(*) FROM operation_logs": 1,
		"SELECT count(*) FROM credentials":    0,
		"SELECT count(*) FROM teams":          0,
	} {
		if c := count(agentA, q); c != want {
			t.Errorf("agent scope: %s = %d (want %d)", q, c, want)
		}
	}
	// An agent cannot touch another server's operation or write into it.
	var touched int64
	_ = Tx(ctx, sqlDB, agentA, func(_ *db.Queries, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "UPDATE operations SET status = 'ok' WHERE id = $1", b.op)
		if err == nil {
			touched, _ = res.RowsAffected()
		}
		return nil
	})
	if touched != 0 {
		t.Errorf("agent A finished team B's operation")
	}
	err = Tx(ctx, sqlDB, agentA, func(q *db.Queries, _ *sql.Tx) error {
		_, err := q.InsertOperationLog(ctx, db.InsertOperationLogParams{TeamID: b.team, ServerID: b.server, OperationID: b.op, Seq: 9, Stream: "info", LineSealed: "x"})
		return err
	})
	if err == nil {
		t.Errorf("agent A appended a line to server B's operation")
	}
	// The system context (control-plane workers) sees both; logs stay append-only for everyone.
	if c := count(Scope{System: true}, "SELECT count(*) FROM operations WHERE id IN ($1, $2)", a.op, b.op); c != 2 {
		t.Errorf("system scope sees %d of 2 operations", c)
	}
	var rewritten int64
	_ = Tx(ctx, sqlDB, Scope{System: true}, func(_ *db.Queries, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "DELETE FROM operation_logs WHERE operation_id = $1", a.op)
		if err == nil {
			rewritten, _ = res.RowsAffected()
		}
		return nil
	})
	if rewritten != 0 {
		t.Errorf("operation logs are deletable (%d rows)", rewritten)
	}
}
