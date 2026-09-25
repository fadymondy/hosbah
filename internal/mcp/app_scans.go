package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/scopes"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// AppScanTools: Trivy image CVE scans (MH-580 J5). app_scan runs one synchronously (it waits for
// the agent, like run_server_step's cousins that return a result rather than just an operation id)
// and records it; app_scan_results reads what is already on file.
func AppScanTools() []Tool {
	appArg := str("App name or id")
	return []Tool{
		{
			Name: "app_scan", Scope: scopes.Write,
			Description: "Scan the app's current live release image for known CVEs with Trivy, and record the result. Waits for the agent (can take a minute or two on a first scan).",
			InputSchema: schema([]string{"app"}, map[string]any{"app": appArg}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				if err := needs(ctx, env, tenancy.AbilityManageApps); err != nil {
					return "", err
				}
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				live, err := env.Q.LiveReleaseOfApp(ctx, a.ID)
				if err != nil || live.TeamID != env.Caller.TeamID {
					return "", errors.New("this app has no live release to scan yet")
				}
				row, err := mcpRunAppScan(ctx, env, a, live, "mcp")
				if err != nil {
					return "", err
				}
				if err := tenancy.Record(ctx, env.Q, env.Caller.TeamID, actor(env), "app.scan", "app", a.ID,
					auditChanges(env, map[string]any{"image": live.Image, "status": row.Status})); err != nil {
					return "", err
				}
				return formatScanLine(row), nil
			},
		},
		{
			Name: "app_scan_results", Scope: scopes.Read,
			Description: "The app's Trivy image scans, newest first: status, severity counts and top findings.",
			InputSchema: schema([]string{"app"}, map[string]any{"app": appArg,
				"limit": map[string]any{"type": "integer", "default": 5, "maximum": 50}}),
			Run: func(ctx context.Context, env *Env, args Args) (string, error) {
				a, err := findApp(ctx, env, args)
				if err != nil {
					return "", err
				}
				rows, err := env.Q.ListAppScans(ctx, db.ListAppScansParams{
					TeamID: env.Caller.TeamID, AppID: a.ID, Lim: int32(args.Int("limit", 5, 1, 50)), Off: 0,
				})
				if err != nil {
					return "", errors.New("could not list the app's scans")
				}
				if len(rows) == 0 {
					return "No scans yet. Run app_scan.", nil
				}
				var b strings.Builder
				for _, r := range rows {
					fmt.Fprintln(&b, formatScanLine(r))
				}
				return b.String(), nil
			},
		},
	}
}

func formatScanLine(r db.AppScan) string {
	if r.Status != "ok" {
		return fmt.Sprintf("%s  %-8s %s  error: %s  (%s)", r.CreatedAt.Format(time.RFC3339), r.Status, r.Image, r.Error, r.ID)
	}
	return fmt.Sprintf("%s  ok  %s  critical %d, high %d, medium %d, low %d, unknown %d  (%s)",
		r.CreatedAt.Format(time.RFC3339), r.Image, r.CriticalCount, r.HighCount, r.MediumCount, r.LowCount, r.UnknownCount, r.ID)
}

// mcpRunAppScan runs app.scan on the app's server and records the result, ok or failed — mirroring
// internal/api's runAppScan (kept separate: the MCP env carries its own *ops.Service accessor and
// *db.Queries, and duplicating ~20 lines here avoids a cross-package dependency for one helper).
func mcpRunAppScan(ctx context.Context, env *Env, a db.App, live db.Release, triggeredBy string) (db.AppScan, error) {
	svc, err := opsOf(env)
	if err != nil {
		return db.AppScan{}, err
	}
	params, _ := json.Marshal(agentproto.AppScanParams{AppID: a.ID, Image: live.Image})
	op, _, err := svc.Enqueue(ctx, env.Q, ops.EnqueueSpec{TeamID: env.Caller.TeamID, ServerID: a.ServerID, Kind: agentproto.KindAppScan, Params: params, CreatedBy: actor(env)})
	if err != nil {
		return db.AppScan{}, errors.New("could not queue the scan")
	}
	svc.Wake()
	started := time.Now()
	done, err := svc.Await(ctx, op.ID, a.ServerID, nil)
	finished := time.Now()
	status, errText := "ok", ""
	var res agentproto.AppScanResult
	if err != nil {
		status, errText = "failed", "the scan did not finish: "+err.Error()
	} else if done.Status != "ok" {
		status = "failed"
		if done.Error != nil {
			errText = *done.Error
		} else {
			errText = "the operation did not succeed: " + done.Status
		}
	} else if done.Result != nil {
		_ = json.Unmarshal([]byte(*done.Result), &res)
	}
	findings, _ := json.Marshal(res.Findings)
	if len(findings) == 0 {
		findings = []byte("[]")
	}
	row, ierr := env.Q.InsertAppScan(ctx, db.InsertAppScanParams{
		TeamID: env.Caller.TeamID, AppID: a.ID, ServerID: a.ServerID, ReleaseID: &live.ID, Image: live.Image,
		Status: status, TrivyVersion: res.TrivyVersion,
		CriticalCount: int64(res.Counts["CRITICAL"]), HighCount: int64(res.Counts["HIGH"]), MediumCount: int64(res.Counts["MEDIUM"]),
		LowCount: int64(res.Counts["LOW"]), UnknownCount: int64(res.Counts["UNKNOWN"]), Findings: string(findings),
		Truncated: res.Truncated, Error: errText, TriggeredBy: triggeredBy, StartedAt: &started, FinishedAt: &finished,
	})
	if ierr != nil {
		return db.AppScan{}, errors.New("could not record the scan")
	}
	return row, nil
}
