package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// App image scans (MH-580 J5): Trivy `image` scans of an app's live release, run by the agent in a
// container on the app's own server (agent op app.scan) and stored as a compact summary. Reading
// scans is any member; running one is owner, admin or deployer (the same ability as deploying) and
// audited. deploy.Service kicks one off automatically, best-effort and non-blocking, after a release
// goes live (triggered_by "deploy"); see scanAfterDeploy in internal/deploy.

// AppScanOut is one scan's summary, as the app page and the MCP tools see it.
type AppScanOut struct {
	ID           string                    `json:"id"`
	Image        string                    `json:"image"`
	Status       string                    `json:"status"`
	TrivyVersion string                    `json:"trivy_version,omitempty"`
	Counts       map[string]int64          `json:"counts"`
	Findings     []agentproto.ScanFinding  `json:"findings"`
	Truncated    bool                      `json:"truncated"`
	Error        string                    `json:"error,omitempty"`
	TriggeredBy  string                    `json:"triggered_by"`
	StartedAt    *time.Time                `json:"started_at,omitempty"`
	FinishedAt   *time.Time                `json:"finished_at,omitempty"`
	CreatedAt    time.Time                 `json:"created_at"`
}

func toAppScanOut(r db.AppScan) AppScanOut {
	out := AppScanOut{
		ID: r.ID, Image: r.Image, Status: r.Status, TrivyVersion: r.TrivyVersion,
		Counts: map[string]int64{
			"CRITICAL": r.CriticalCount, "HIGH": r.HighCount, "MEDIUM": r.MediumCount,
			"LOW": r.LowCount, "UNKNOWN": r.UnknownCount,
		},
		Truncated: r.Truncated, Error: r.Error, TriggeredBy: r.TriggeredBy,
		StartedAt: r.StartedAt, FinishedAt: r.FinishedAt, CreatedAt: r.CreatedAt,
	}
	_ = json.Unmarshal([]byte(r.Findings), &out.Findings)
	if out.Findings == nil {
		out.Findings = []agentproto.ScanFinding{}
	}
	return out
}

func (d *deps) registerAppScans(api huma.API) {
	tags := []string{"App scans"}
	base := "/api/teams/{team}/apps/{app}/scans"

	type scansListOut struct {
		Body struct {
			Scans []AppScanOut `json:"scans"`
		}
	}
	huma.Register(api, huma.Operation{OperationID: "list-app-scans", Method: http.MethodGet, Path: base, Tags: tags,
		Summary: "The app's Trivy image scans, newest first. Any member.",
	}, func(ctx context.Context, in *AppPath) (*scansListOut, error) {
		out := &scansListOut{}
		out.Body.Scans = []AppScanOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityView, func(q *db.Queries, _ *sql.Tx, _ *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			rows, err := q.ListAppScans(ctx, db.ListAppScansParams{TeamID: m.TeamID, AppID: a.ID, Lim: 50, Off: 0})
			if err != nil {
				return huma.Error500InternalServerError("could not list the app's scans")
			}
			for _, r := range rows {
				out.Body.Scans = append(out.Body.Scans, toAppScanOut(r))
			}
			return nil
		})
		return out, err
	})

	type scanRunOut struct{ Body AppScanOut }
	huma.Register(api, huma.Operation{OperationID: "run-app-scan", Method: http.MethodPost, Path: base, DefaultStatus: http.StatusCreated, Tags: tags,
		Summary: "Scan the app's current live release image for known vulnerabilities with Trivy (owner, admin or deployer). Audited.",
	}, func(ctx context.Context, in *AppPath) (*scanRunOut, error) {
		out := &scanRunOut{}
		err := d.inTeam(ctx, in.Team, tenancy.AbilityManageApps, func(q *db.Queries, _ *sql.Tx, id *auth.Identity, m *tenancy.Membership) error {
			a, err := appIn(ctx, q, m, in.App)
			if err != nil {
				return err
			}
			live, err := q.LiveReleaseOfApp(ctx, a.ID)
			if err != nil || live.TeamID != m.TeamID {
				return huma.Error409Conflict("this app has no live release to scan yet")
			}
			row, err := d.runAppScan(ctx, q, m.TeamID, a.ID, a.ServerID, live.ID, live.Image, "member")
			if err != nil {
				return err
			}
			if err := tenancy.Record(ctx, q, m.TeamID, id.ID, "app.scan", "app", a.ID,
				map[string]any{"image": live.Image, "status": row.Status}); err != nil {
				return huma.Error500InternalServerError("audit failed")
			}
			out.Body = toAppScanOut(row)
			return nil
		})
		return out, err
	})
}

// runAppScan runs app.scan on the app's server and records the result (ok or failed — the record
// itself is never skipped, so a failed scan still shows on the app page instead of vanishing).
// q is a team-scoped *db.Queries (from d.inTeam); the operation itself is enqueued in its own
// system+team-scope transaction, the same way runServiceOp does.
func (d *deps) runAppScan(ctx context.Context, q *db.Queries, teamID, appID, serverID, releaseID, image, triggeredBy string) (db.AppScan, error) {
	started := time.Now()
	var res agentproto.AppScanResult
	opErr := d.runServiceOp(ctx, teamID, serverID, agentproto.KindAppScan,
		agentproto.AppScanParams{AppID: appID, Image: image}, &res)
	finished := time.Now()
	status, errText := "ok", ""
	counts := map[string]int64{}
	findings := []byte("[]")
	if opErr != nil {
		status, errText = "failed", opErr.Error()
	} else {
		counts["CRITICAL"] = int64(res.Counts["CRITICAL"])
		counts["HIGH"] = int64(res.Counts["HIGH"])
		counts["MEDIUM"] = int64(res.Counts["MEDIUM"])
		counts["LOW"] = int64(res.Counts["LOW"])
		counts["UNKNOWN"] = int64(res.Counts["UNKNOWN"])
		if b, e := json.Marshal(res.Findings); e == nil {
			findings = b
		}
	}
	row, err := q.InsertAppScan(ctx, db.InsertAppScanParams{
		TeamID: teamID, AppID: appID, ServerID: serverID, ReleaseID: &releaseID, Image: image,
		Status: status, TrivyVersion: res.TrivyVersion,
		CriticalCount: counts["CRITICAL"], HighCount: counts["HIGH"], MediumCount: counts["MEDIUM"],
		LowCount: counts["LOW"], UnknownCount: counts["UNKNOWN"], Findings: string(findings),
		Truncated: res.Truncated, Error: errText, TriggeredBy: triggeredBy,
		StartedAt: &started, FinishedAt: &finished,
	})
	if err != nil {
		return db.AppScan{}, huma.Error500InternalServerError("could not record the scan")
	}
	return row, nil
}
