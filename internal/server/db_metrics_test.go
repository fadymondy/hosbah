package server

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/dbsvc"
	"github.com/fadymondy/hosbah/internal/importer"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// seedDataServiceMetric inserts one minute (step 60) reading directly, the way the sampler would,
// without needing a real agent round trip.
func seedDataServiceMetric(t *testing.T, teamID, serviceID string, at time.Time, connections, maxConnections, sizeBytes, txCounter int64, cacheHit, rate float64) {
	t.Helper()
	err := tenancy.Tx(context.Background(), testDB(t), tenancy.Scope{System: true, TeamID: teamID}, func(_ *db.Queries, tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO data_service_metrics (service_id, team_id, at, step, connections, max_connections, cache_hit_ratio, size_bytes, slow_query_count, tx_counter, rate_per_sec)
			VALUES ($1, $2, $3, 60, $4, $5, $6, $7, 0, $8, $9)
			ON CONFLICT (service_id, step, at) DO UPDATE SET connections = EXCLUDED.connections`,
			serviceID, teamID, at, connections, maxConnections, cacheHit, sizeBytes, txCounter, rate)
		return err
	})
	if err != nil {
		t.Fatalf("seed data_service_metric: %v", err)
	}
}

// MH-575 F6: DB metrics history charts. Seeds minute rows directly (the sampler's own write, without
// a real agent), then checks the REST history endpoint, the MCP tool, RLS isolation and the retention
// job — the storage/aggregation layer this feature adds on top of the already-tested on-demand Insights.
func TestDBMetricsHistory(t *testing.T) {
	ts, cleanup := bootTestServer(t)
	defer cleanup()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	ua := newClient(t, ts.URL)
	ua.register("owner-dbm-" + suffix + "@example.test")

	var team struct{ ID string }
	ua.mustJSON("POST", "/api/teams", map[string]any{"name": "DBM " + suffix}, 201, &team)

	var created struct {
		Server  struct{ ID string }
		Install struct{ URL string }
	}
	ua.mustJSON("POST", "/api/teams/"+team.ID+"/servers", map[string]any{"name": "dbm-" + suffix, "provider": "byo"}, 201, &created)
	serverID := created.Server.ID

	// An imported running Postgres container: a real db.insights round trip needs a live agent, which
	// this test does not stand up — the sampler's write path (RecordDataServiceMetric) and read path
	// (the REST/MCP history) are what F6 adds, and are exercised directly here.
	if _, err := importer.Sync(context.Background(), testDB(t), team.ID, serverID, []agentproto.Service{
		{Kind: "container", Name: "dbm-pg-" + suffix, Image: "postgres:16", Status: "running"},
	}); err != nil {
		t.Fatalf("import: %v", err)
	}
	var services struct {
		Services []struct{ ID, Name, Kind string } `json:"services"`
	}
	ua.mustJSON("GET", "/api/teams/"+team.ID+"/data-services", nil, 200, &services)
	svcID := ""
	for _, s := range services.Services {
		if s.Name == "dbm-pg-"+suffix {
			svcID = s.ID
		}
	}
	if svcID == "" {
		t.Fatalf("imported data service not found: %+v", services)
	}

	now := time.Now().UTC().Truncate(time.Minute)
	// Three minute rows a minute apart, ~2 minutes back so they land in the 1h window.
	seedDataServiceMetric(t, team.ID, svcID, now.Add(-2*time.Minute), 10, 100, 1_000_000, 1000, 98.0, 5)
	seedDataServiceMetric(t, team.ID, svcID, now.Add(-1*time.Minute), 20, 100, 1_100_000, 1120, 99.0, 6)
	seedDataServiceMetric(t, team.ID, svcID, now, 30, 100, 1_200_000, 1300, 97.0, 7)

	// ---- REST: GET history ----
	var series struct {
		Range       string `json:"range"`
		StepSeconds int    `json:"step_seconds"`
		Points      []struct {
			At             time.Time `json:"at"`
			Connections    float64   `json:"connections"`
			MaxConnections int64     `json:"max_connections"`
			CacheHitRatio  float64   `json:"cache_hit_ratio"`
			SizeBytes      int64     `json:"size_bytes"`
			RatePerSec     float64   `json:"rate_per_sec"`
		} `json:"points"`
	}
	ua.mustJSON("GET", "/api/teams/"+team.ID+"/data-services/"+svcID+"/metrics?range=1h", nil, 200, &series)
	if series.Range != "1h" || series.StepSeconds != 60 {
		t.Fatalf("unexpected series header: %+v", series)
	}
	if len(series.Points) != 3 {
		t.Fatalf("expected 3 one-minute buckets, got %d: %+v", len(series.Points), series.Points)
	}
	last := series.Points[len(series.Points)-1]
	if last.Connections != 30 || last.MaxConnections != 100 || last.SizeBytes != 1_200_000 {
		t.Errorf("last bucket wrong: %+v", last)
	}

	// A bad range is 422.
	if code, _ := ua.do("GET", "/api/teams/"+team.ID+"/data-services/"+svcID+"/metrics?range=nope", nil); code != 422 {
		t.Errorf("range=nope -> %d, want 422", code)
	}

	// ---- MCP: db_metrics_history ----
	var tok struct{ ID, Token string }
	ua.mustJSON("POST", "/api/teams/"+team.ID+"/mcp-tokens", map[string]any{"name": "reader", "scopes": []string{"read"}, "expires_in_days": 30}, 201, &tok)
	res := mcpCall(t, ts.URL, "/api/mcp", tok.Token, "db_metrics_history", map[string]any{"database": svcID, "range": "1h"})
	if res.IsError {
		t.Fatalf("db_metrics_history errored: %s", res.Text)
	}
	if !strings.Contains(res.Text, "dbm-pg-"+suffix) || !strings.Contains(res.Text, "3 point") {
		t.Errorf("db_metrics_history text missing expected content: %s", res.Text)
	}

	// ---- RLS: an unrelated team never sees these rows ----
	ub := newClient(t, ts.URL)
	ub.register("owner-dbm-b-" + suffix + "@example.test")
	var teamB struct{ ID string }
	ub.mustJSON("POST", "/api/teams", map[string]any{"name": "DBM-B " + suffix}, 201, &teamB)
	if code, _ := ub.do("GET", "/api/teams/"+teamB.ID+"/data-services/"+svcID+"/metrics?range=1h", nil); code != 404 {
		t.Errorf("team B reading team A's service metrics -> %d, want 404", code)
	}

	// ---- retention: a minute row far in the past folds into a 15-minute row and an ancient
	// 15-minute row is pruned ----
	old := now.Add(-30 * time.Hour)
	seedDataServiceMetric(t, team.ID, svcID, old, 5, 100, 500_000, 100, 90.0, 1)
	ancient := now.Add(-40 * 24 * time.Hour)
	err := tenancy.Tx(context.Background(), testDB(t), tenancy.Scope{System: true, TeamID: team.ID}, func(_ *db.Queries, tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO data_service_metrics (service_id, team_id, at, step, connections, max_connections, cache_hit_ratio, size_bytes, slow_query_count, tx_counter, rate_per_sec)
			VALUES ($1, $2, $3, 900, 1, 100, 90, 100, 0, 1, 0)`, svcID, team.ID, ancient)
		return err
	})
	if err != nil {
		t.Fatalf("seed ancient 15-minute row: %v", err)
	}
	compacted, pruned, err := dbsvc.CompactMetricsHistory(context.Background(), testDB(t), now)
	if err != nil {
		t.Fatalf("CompactMetricsHistory: %v", err)
	}
	if compacted == 0 {
		t.Errorf("expected at least one row compacted, got %d", compacted)
	}
	if pruned == 0 {
		t.Errorf("expected the ancient 15-minute row to be pruned, got %d", pruned)
	}
	var oldGone bool
	_ = tenancy.Tx(context.Background(), testDB(t), tenancy.Scope{System: true, TeamID: team.ID}, func(_ *db.Queries, tx *sql.Tx) error {
		var n int
		if err := tx.QueryRow(`SELECT count(*) FROM data_service_metrics WHERE service_id = $1 AND step = 60 AND at = $2`, svcID, old).Scan(&n); err != nil {
			return err
		}
		oldGone = n == 0
		return nil
	})
	if !oldGone {
		t.Errorf("the old minute row should have been folded away, not left as step=60")
	}
}
