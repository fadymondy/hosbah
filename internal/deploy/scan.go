package deploy

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
	"github.com/fadymondy/hosbah/internal/ops"
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// scanAfterDeploy (MH-580 J5): a best-effort, non-blocking Trivy image scan of a release right after
// it goes live. Runs in its own goroutine, on its own background context and its own transactions —
// a deploy must never wait on, or fail because of, a vulnerability scan, and the scan must still be
// on record (status "failed") if the agent op itself fails, the same as internal/api's runAppScan.
func (s *Service) scanAfterDeploy(teamID, appID, serverID, releaseID, image string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()

		started := time.Now()
		params, err := json.Marshal(agentproto.AppScanParams{AppID: appID, Image: image})
		if err != nil {
			return
		}
		var opID string
		if err := tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{System: true, TeamID: teamID}, func(q *db.Queries, _ *sql.Tx) error {
			op, _, e := s.ops.Enqueue(ctx, q, ops.EnqueueSpec{TeamID: teamID, ServerID: serverID, Kind: agentproto.KindAppScan, Params: params, CreatedBy: "deploy"})
			if e != nil {
				return e
			}
			opID = op.ID
			return nil
		}); err != nil {
			return // could not even queue it — nothing to record, no live deployment is waiting on this
		}
		s.ops.Wake()

		status, errText := "ok", ""
		var res agentproto.AppScanResult
		done, err := s.ops.Await(ctx, opID, serverID, nil)
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
		findings, e := json.Marshal(res.Findings)
		if e != nil || findings == nil {
			findings = []byte("[]")
		}
		finished := time.Now()
		_ = tenancy.Tx(ctx, s.a.SQLDB, tenancy.Scope{System: true, TeamID: teamID}, func(q *db.Queries, _ *sql.Tx) error {
			_, e := q.InsertAppScan(ctx, db.InsertAppScanParams{
				TeamID: teamID, AppID: appID, ServerID: serverID, ReleaseID: &releaseID, Image: image,
				Status: status, TrivyVersion: res.TrivyVersion,
				CriticalCount: int64(res.Counts["CRITICAL"]), HighCount: int64(res.Counts["HIGH"]), MediumCount: int64(res.Counts["MEDIUM"]),
				LowCount: int64(res.Counts["LOW"]), UnknownCount: int64(res.Counts["UNKNOWN"]), Findings: string(findings),
				Truncated: res.Truncated, Error: errText, TriggeredBy: "deploy", StartedAt: &started, FinishedAt: &finished,
			})
			return e
		})
	}()
}
