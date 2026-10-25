package deploy

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"github.com/fadymondy/hosbah/internal/app"
	"github.com/fadymondy/hosbah/internal/config"
	"github.com/fadymondy/hosbah/internal/crypto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/migrate"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

var errRollback = errors.New("rollback")

// TestBuildQueueDebounce runs the build queue against a real database: a pushed deploy is not
// claimed before its not_before, a newer push coalesces the waiting one (keeping the first push's
// clock), a person's deploy starts at once, and a push the stored [build] rules ignore is recorded
// as skipped. Needs TEST_DATABASE_URL (a migrated DB); skipped otherwise.
func TestBuildQueueDebounce(t *testing.T) {
	config.LoadDotEnv()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run the build queue DB tests")
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
	kek := make([]byte, 32)
	_, _ = rand.Read(kek)
	keys, err := crypto.NewKeyring(kek)
	if err != nil {
		t.Fatal(err)
	}
	a := &app.App{SQLDB: sqlDB, Keys: keys}
	o, err := ops.New(a, ops.Config{})
	if err != nil {
		t.Fatal(err)
	}
	s := New(a, o)
	s.SetDebounce(Debounce{Wait: time.Minute, MaxWait: 10 * time.Minute})

	n := time.Now().UnixNano()
	teamID, userID, serverID, appID := fmt.Sprintf("deb-team-%d", n), fmt.Sprintf("deb-user-%d", n), fmt.Sprintf("deb-srv-%d", n), fmt.Sprintf("deb-app-%d", n)
	team := tenancy.Scope{UserID: userID, TeamID: teamID}
	var theApp db.App
	if err := tenancy.Tx(ctx, sqlDB, team, func(q *db.Queries, _ *sql.Tx) error {
		if _, err := q.InsertTeam(ctx, db.InsertTeamParams{ID: teamID, Name: teamID, Slug: teamID, OwnerID: userID, DekWrapped: "x"}); err != nil {
			return err
		}
		if _, err := q.InsertTeamMember(ctx, db.InsertTeamMemberParams{TeamID: teamID, UserID: userID, Role: "owner"}); err != nil {
			return err
		}
		if _, err := q.InsertServer(ctx, db.InsertServerParams{ID: serverID, TeamID: teamID, Name: "srv", Provider: "byo", Kind: "vm", Status: "enrolled", CreatedBy: userID}); err != nil {
			return err
		}
		var err error
		theApp, err = q.InsertApp(ctx, db.InsertAppParams{ID: appID, TeamID: teamID, ServerID: serverID, Name: "web", RepoURL: "https://github.com/x/y",
			Branch: "main", Port: 3000, HealthcheckPath: "/", HealthcheckTimeout: 5, CreatedBy: userID})
		return err
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	inTeamTx := func(fn func(q *db.Queries, tx *sql.Tx) error) {
		t.Helper()
		if err := tenancy.Tx(ctx, sqlDB, team, fn); err != nil {
			t.Fatal(err)
		}
	}
	inTeam := func(fn func(q *db.Queries) error) {
		t.Helper()
		inTeamTx(func(q *db.Queries, _ *sql.Tx) error { return fn(q) })
	}
	// claimable reports whether the worker would claim this deployment now (rolled back: nothing runs).
	claimable := func(id string) bool {
		t.Helper()
		got := false
		err := tenancy.Tx(ctx, sqlDB, tenancy.Scope{System: true}, func(q *db.Queries, _ *sql.Tx) error {
			for {
				row, err := q.ClaimQueuedDeployment(ctx)
				if errors.Is(err, sql.ErrNoRows) {
					return errRollback
				}
				if err != nil {
					return err
				}
				if row.ID == id {
					got = true
					return errRollback
				}
				// Another test's leftover: take it out of the way inside this rolled-back tx.
				if _, err := q.StartDeployment(ctx, row.ID); err != nil {
					return err
				}
			}
		})
		if err != nil && !errors.Is(err, errRollback) {
			t.Fatal(err)
		}
		return got
	}

	// A push waits out the debounce.
	var first, second db.Deployment
	inTeam(func(q *db.Queries) error {
		var err error
		first, err = s.QueueDeployment(ctx, q, teamID, Actor{Via: "github"}, theApp, "deploy", "", nil, "")
		return err
	})
	if first.NotBefore == nil || first.QueuedSince == nil || time.Until(*first.NotBefore) < 50*time.Second {
		t.Fatalf("a push must wait: not_before %v queued_since %v", first.NotBefore, first.QueuedSince)
	}
	if claimable(first.ID) {
		t.Fatal("claimed before its not_before")
	}

	// A newer push coalesces it: the first push's clock, one more request.
	time.Sleep(10 * time.Millisecond)
	inTeam(func(q *db.Queries) error {
		var err error
		second, err = s.QueueDeployment(ctx, q, teamID, Actor{Via: "hook"}, theApp, "deploy", "", nil, "")
		return err
	})
	if second.Coalesced != 1 || second.QueuedSince == nil || !second.QueuedSince.Equal(*first.QueuedSince) {
		t.Fatalf("coalesced %d, queued_since %v (first %v)", second.Coalesced, second.QueuedSince, first.QueuedSince)
	}
	inTeam(func(q *db.Queries) error {
		d, err := q.DeploymentInTeam(ctx, db.DeploymentInTeamParams{ID: first.ID, TeamID: teamID})
		if err != nil || d.Status != "cancelled" {
			return fmt.Errorf("the first push is %s (%v), want cancelled (superseded)", d.Status, err)
		}
		return nil
	})

	// Once not_before has passed it is claimable.
	inTeamTx(func(_ *db.Queries, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE deployments SET not_before = now() - interval '1 second' WHERE id = $1", second.ID)
		return err
	})
	if !claimable(second.ID) {
		t.Fatal("not claimable after its not_before")
	}

	// A person's deploy starts at once (and still counts the pushes it replaced).
	var manual db.Deployment
	inTeam(func(q *db.Queries) error {
		var err error
		manual, err = s.QueueDeployment(ctx, q, teamID, Actor{UserID: userID, Via: "web"}, theApp, "deploy", "", nil, "")
		return err
	})
	if manual.NotBefore != nil || manual.Coalesced != 2 || !claimable(manual.ID) {
		t.Fatalf("manual deploy: not_before %v coalesced %d", manual.NotBefore, manual.Coalesced)
	}

	// Stored [build] rules: a push touching only ignored paths is skipped once the app is not behind a
	// failed build.
	rules := BuildRules{Ignore: []string{"docs/**"}, Debounce: 5}
	inTeamTx(func(q *db.Queries, tx *sql.Tx) error {
		if err := q.SetAppBuildRules(ctx, db.SetAppBuildRulesParams{BuildRules: rules.JSON(), ID: appID}); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE deployments SET status = 'succeeded', finished_at = now() WHERE id = $1", manual.ID)
		return err
	})
	push := PushPayload{Before: "a1", After: "b2", Commits: []PushCommit{{ID: "b2", Message: "docs: typo", Modified: []string{"docs/x.md"}}}}
	inTeam(func(q *db.Queries) error {
		a, err := q.AppInTeam(ctx, db.AppInTeamParams{ID: appID, TeamID: teamID})
		if err != nil {
			return err
		}
		if ParseBuildRules(a.BuildRules).Debounce != 5 {
			return fmt.Errorf("stored rules %q", a.BuildRules)
		}
		skip, reason := SkipPush(ctx, q, teamID, a, push, nil)
		if !skip {
			return fmt.Errorf("not skipped: %s", reason)
		}
		d, err := s.RecordSkipped(ctx, q, teamID, Actor{Via: "github"}, a, push, reason)
		if err != nil {
			return err
		}
		if d.Status != "skipped" || d.Error == nil || *d.Error != "skipped: no image changes (docs/x.md)" || d.CommitSha == nil || *d.CommitSha != "b2" {
			return fmt.Errorf("skipped row %+v", d)
		}
		code := PushPayload{Before: "b2", After: "c3", Commits: []PushCommit{{ID: "c3", Modified: []string{"docs/y.md", "web/page.tsx"}}}}
		if skip, _ := SkipPush(ctx, q, teamID, a, code, nil); skip {
			return errors.New("a code change was skipped")
		}
		// The app's debounce comes from its rules now.
		dep, err := s.QueueDeployment(ctx, q, teamID, Actor{Via: "github"}, a, "deploy", "", nil, "")
		if err != nil {
			return err
		}
		if w := time.Until(*dep.NotBefore); w > 6*time.Second || w < 3*time.Second {
			return fmt.Errorf("debounce from hosbah.toml: waits %v", w)
		}
		return nil
	})
	// Behind a failed build, an ignored-only push still builds (the newest commit retries it).
	inTeamTx(func(q *db.Queries, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE deployments SET status = 'failed', finished_at = now() WHERE app_id = $1 AND status = 'queued'", appID); err != nil {
			return err
		}
		a, err := q.AppInTeam(ctx, db.AppInTeamParams{ID: appID, TeamID: teamID})
		if err != nil {
			return err
		}
		if skip, reason := SkipPush(ctx, q, teamID, a, push, nil); skip || reason != "the last deployment failed" {
			return fmt.Errorf("behind a failed build: skip %v (%s)", skip, reason)
		}
		return nil
	})
	// With a host that compares commits, behind a cancelled build only what changed since the live
	// release counts (fadymondy.com #470 after the cancelled #706): ignored-only is skipped, code builds.
	if err := tenancy.Tx(ctx, sqlDB, tenancy.Scope{System: true, TeamID: teamID}, func(_ *db.Queries, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE deployments SET status = 'cancelled' WHERE app_id = $1 AND status = 'failed'", appID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO releases (id, team_id, app_id, deployment_id, version, image, commit_sha, port, config, status, activated_at)
			VALUES ($1, $2, $3, $4, 1, 'img', 'a0', 3000, '{}', 'live', now())`, newUUID(), teamID, appID, manual.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	inTeam(func(q *db.Queries) error {
		a, err := q.AppInTeam(ctx, db.AppInTeamParams{ID: appID, TeamID: teamID})
		if err != nil {
			return err
		}
		host := &fakeComparer{answers: map[string][]string{
			"a1...b2": {"docs/x.md"},
			"a0...b2": {"docs/x.md", "docs/y.md"},
			"b2...c3": {"docs/y.md"},
			"a0...c3": {"docs/y.md", "web/page.tsx"},
		}}
		if skip, reason := SkipPush(ctx, q, teamID, a, push, host.cmp); !skip || reason != "no image changes since the live release (2 files, none watched)" {
			return fmt.Errorf("behind a cancelled build, nothing new for the image: skip %v (%s)", skip, reason)
		}
		docs := PushPayload{Before: "b2", After: "c3", Commits: []PushCommit{{ID: "c3", Modified: []string{"docs/y.md"}}}}
		if skip, reason := SkipPush(ctx, q, teamID, a, docs, host.cmp); skip || reason != "the last deployment cancelled and web/page.tsx changed" {
			return fmt.Errorf("behind a cancelled code build: skip %v (%s)", skip, reason)
		}
		if skip, reason := SkipPush(ctx, q, teamID, a, PushPayload{Before: "x", After: "y", Commits: []PushCommit{{ID: "y", Modified: []string{"docs/z.md"}}}}, host.cmp); skip || reason != "the last deployment cancelled" {
			return fmt.Errorf("no comparison behind a cancelled build: skip %v (%s)", skip, reason)
		}
		return nil
	})
}
