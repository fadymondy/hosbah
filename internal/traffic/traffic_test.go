package traffic

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/fadymondy/hosbah/internal/agentproto"
	"github.com/fadymondy/hosbah/internal/config"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/migrate"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// ---- pure functions, no database ---------------------------------------------------------------

func TestWindowAlignsToStep(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 34, 56, 0, time.UTC)
	since := window(now, time.Hour, time.Minute)
	if want := time.Date(2026, 9, 25, 11, 35, 0, 0, time.UTC); !since.Equal(want) {
		t.Fatalf("since = %v, want %v", since, want)
	}
}

func TestTopOrEmptyNeverReturnsNil(t *testing.T) {
	if got := topOrEmpty(nil); got == nil || len(got) != 0 {
		t.Fatalf("topOrEmpty(nil) = %v, want an empty, non-nil slice", got)
	}
	in := []agentproto.KeyCount{{Key: "/a", Count: 1}}
	if got := topOrEmpty(in); len(got) != 1 || got[0].Key != "/a" {
		t.Fatalf("topOrEmpty(%v) = %v", in, got)
	}
}

func TestMergeCountsSumsAcrossRows(t *testing.T) {
	into := map[string]int64{"/a": 1}
	mergeCounts(into, `[{"key":"/a","count":2},{"key":"/b","count":5}]`)
	if into["/a"] != 3 || into["/b"] != 5 {
		t.Fatalf("merged = %v", into)
	}
	// Garbage JSON is ignored, not fatal.
	mergeCounts(into, "not json")
	mergeCounts(into, "")
	if into["/a"] != 3 {
		t.Fatalf("garbage input must not change the map: %v", into)
	}
}

func TestRankTopOrdersByCountThenKeyAndCaps(t *testing.T) {
	counts := map[string]int64{}
	for i := 0; i < TopN+5; i++ {
		counts[fmt.Sprintf("/p%02d", i)] = int64(i)
	}
	counts["/tie-a"] = 3
	counts["/tie-b"] = 3
	out := rankTop(counts)
	if len(out) != TopN {
		t.Fatalf("len = %d, want %d", len(out), TopN)
	}
	for i := 1; i < len(out); i++ {
		if out[i-1].Count < out[i].Count {
			t.Fatalf("not sorted descending by count: %+v", out)
		}
	}
}

func TestRound1And2(t *testing.T) {
	if v := round1(12.34); v != 12.3 {
		t.Errorf("round1(12.34) = %v, want 12.3", v)
	}
	if v := round1(12.36); v != 12.4 {
		t.Errorf("round1(12.36) = %v, want 12.4", v)
	}
	if v := round2(0.12345); v != 0.12 {
		t.Errorf("round2(0.12345) = %v, want 0.12", v)
	}
	if v := round1(0); v != 0 {
		t.Errorf("round1(0) = %v", v)
	}
}

// ---- database-backed: Record, Compact and AppSeries end to end ---------------------------------

// TestRecordCompactAndSeries exercises the full lifecycle a minute of traffic goes through: the
// agent's Record, the retention job's Compact (folding old minutes into a 15-minute bucket), and
// the API's AppSeries reading it back. Set TEST_DATABASE_URL (a migrated DB) to run it; skipped
// otherwise, the same convention as internal/tenancy/rls_test.go.
func TestRecordCompactAndSeries(t *testing.T) {
	config.LoadDotEnv()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run the traffic DB tests")
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
	teamID := fmt.Sprintf("traffic-team-%d", n)
	userID := fmt.Sprintf("traffic-user-%d", n)
	serverID := fmt.Sprintf("traffic-server-%d", n)
	appID := fmt.Sprintf("traffic-app-%d", n)

	seed := tenancy.Scope{UserID: userID, TeamID: teamID}
	err = tenancy.Tx(ctx, sqlDB, seed, func(q *db.Queries, _ *sql.Tx) error {
		if _, err := q.InsertTeam(ctx, db.InsertTeamParams{ID: teamID, Name: teamID, Slug: teamID, OwnerID: userID, DekWrapped: "x"}); err != nil {
			return fmt.Errorf("team: %w", err)
		}
		if _, err := q.InsertServer(ctx, db.InsertServerParams{
			ID: serverID, TeamID: teamID, Name: "srv", Provider: "byo", Kind: "vm", Status: "enrolled", CreatedBy: userID,
		}); err != nil {
			return fmt.Errorf("server: %w", err)
		}
		if _, err := q.InsertApp(ctx, db.InsertAppParams{
			ID: appID, TeamID: teamID, ServerID: serverID, Name: "app", RepoURL: "https://example.com/repo.git",
			Branch: "main", Port: 3000, HealthcheckPath: "/", HealthcheckTimeout: 5, CreatedBy: userID,
		}); err != nil {
			return fmt.Errorf("app: %w", err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed fixtures: %v", err)
	}

	// Two old minutes (past MinuteRetention) that must compact into one 15-minute bucket, and one
	// recent minute that must stay put.
	oldBase := time.Now().UTC().Add(-2 * MinuteRetention).Truncate(15 * time.Minute)
	recent := time.Now().UTC().Truncate(time.Minute)

	minutes := []agentproto.TrafficMinute{
		{
			AppID: appID, At: oldBase, Requests: 2, Status2xx: 2, BytesOut: 200,
			DurationP50Ms: 10, DurationP95Ms: 20, UniqueVisitors: 2,
			TopPaths:     []agentproto.KeyCount{{Key: "/a", Count: 2}},
			TopReferrers: []agentproto.KeyCount{{Key: "google.com", Count: 1}},
		},
		{
			AppID: appID, At: oldBase.Add(time.Minute), Requests: 1, Status4xx: 1, BytesOut: 100,
			DurationP50Ms: 30, DurationP95Ms: 40, UniqueVisitors: 1,
			TopPaths: []agentproto.KeyCount{{Key: "/b", Count: 1}},
		},
		{
			AppID: appID, At: recent, Requests: 5, Status2xx: 5, BytesOut: 500,
			DurationP50Ms: 5, DurationP95Ms: 8, UniqueVisitors: 3,
			TopPaths: []agentproto.KeyCount{{Key: "/a", Count: 5}},
		},
	}
	for _, m := range minutes {
		err := tenancy.Tx(ctx, sqlDB, tenancy.Scope{System: true, TeamID: teamID}, func(q *db.Queries, _ *sql.Tx) error {
			return Record(ctx, q, appID, serverID, teamID, m)
		})
		if err != nil {
			t.Fatalf("record %v: %v", m.At, err)
		}
	}

	compacted, pruned, err := Compact(ctx, sqlDB, time.Now())
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if compacted != 2 {
		t.Errorf("compacted = %d, want 2 (only the two old minutes)", compacted)
	}
	if pruned != 0 {
		t.Errorf("pruned = %d, want 0 (nothing is older than Retention)", pruned)
	}

	// Read the whole 30-day range back: one folded 15-minute bucket plus the still-fresh minute.
	var series AnalyticsSeries
	err = tenancy.Tx(ctx, sqlDB, tenancy.Scope{UserID: userID, TeamID: teamID}, func(q *db.Queries, _ *sql.Tx) error {
		s, err := AppSeries(ctx, q, teamID, appID, Ranges["30d"], time.Now())
		series = s
		return err
	})
	if err != nil {
		t.Fatalf("app series: %v", err)
	}
	if series.Totals.Requests != 8 {
		t.Errorf("total requests = %d, want 8 (2+1+5)", series.Totals.Requests)
	}
	if series.Totals.Visitors != 6 {
		t.Errorf("total visitors = %d, want 6 (2+1+3, summed not deduped across buckets)", series.Totals.Visitors)
	}
	if series.Totals.ErrorRate <= 0 {
		t.Errorf("error rate = %v, want > 0 (one 4xx out of 8 requests)", series.Totals.ErrorRate)
	}
	found := false
	for _, p := range series.TopPaths {
		if p.Key == "/a" && p.Count == 7 {
			found = true
		}
	}
	if !found {
		t.Errorf("top paths = %+v, want /a with count 7 (2 folded + 5 recent)", series.TopPaths)
	}

	// Compacting again is a no-op: nothing new has crossed MinuteRetention.
	compacted2, _, err := Compact(ctx, sqlDB, time.Now())
	if err != nil {
		t.Fatalf("compact again: %v", err)
	}
	if compacted2 != 0 {
		t.Errorf("second compact moved %d rows, want 0 (idempotent)", compacted2)
	}

	// A far-future "now" prunes everything, old and recent alike.
	_, prunedAll, err := Compact(ctx, sqlDB, time.Now().Add(2*Retention))
	if err != nil {
		t.Fatalf("compact far future: %v", err)
	}
	if prunedAll == 0 {
		t.Errorf("expected the far-future compact to prune rows, got 0")
	}
}
