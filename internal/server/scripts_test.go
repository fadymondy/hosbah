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
	"github.com/fadymondy/hosbah/internal/tenancy"
)

// seedScriptRun inserts a finished run of a built-in script targeting one server, with unsealed
// output text (a stand-in, not a real seal) — mirrors seedScheduleRun in schedules_test.go.
func seedScriptRun(t *testing.T, teamID, serverID, status, output string) string {
	t.Helper()
	var runID string
	err := tenancy.Tx(context.Background(), testDB(t), tenancy.Scope{System: true}, func(_ *db.Queries, tx *sql.Tx) error {
		if err := tx.QueryRow(`INSERT INTO script_runs (team_id, builtin_name, status, created_by)
			VALUES ($1, 'caddy', $2, 'test') RETURNING id`, teamID, status).Scan(&runID); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO script_run_servers (team_id, script_run_id, server_id, status, exit_code, output, started_at, finished_at)
			VALUES ($1, $2, $3, $4, 0, $5, now() - interval '1 day', now() - interval '1 day')`,
			teamID, runID, serverID, status, output)
		return err
	})
	if err != nil {
		t.Fatalf("seed a script run: %v", err)
	}
	return runID
}

type scriptJSON struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Description    string            `json:"description"`
	Body           string            `json:"body"`
	DefaultArgs    map[string]string `json:"default_args"`
	EnvNames       []string          `json:"env_names"`
	RunAs          string            `json:"run_as"`
	TimeoutSeconds int64             `json:"timeout_seconds"`
	CreatedBy      string            `json:"created_by"`
}

type scriptRunServerJSON struct {
	db.ScriptRunServer
	Output string `json:"output"`
}

type runStartJSON struct {
	Run     db.ScriptRun          `json:"run"`
	Servers []scriptRunServerJSON `json:"servers"`
}

type runDetailJSON struct {
	Run     db.ScriptRun          `json:"run"`
	Servers []scriptRunServerJSON `json:"servers"`
}

/*
Scripts and services (MH-576 D4/D5/D3), end to end against a real fake agent: a team script created,
run on two servers with a per-server output tail, run history, a built-in's param allow list
rejecting a value outside its enum, role enforcement (a viewer cannot save or run), cross-team
isolation, and a server's systemd services — list, an action, and the agent's own unit always
refused, on both sides.
*/
func TestScriptsAndServices(t *testing.T) {
	ts, stop := bootTestServer(t)
	defer stop()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	short := suffix[len(suffix)-6:]

	owner := newClient(t, ts.URL)
	owner.register("scripts-owner-" + suffix + "@example.test")
	var team struct{ ID string }
	owner.mustJSON("POST", "/api/teams", map[string]any{"name": "Scripts " + suffix}, 201, &team)
	base := "/api/teams/" + team.ID

	viewerEmail := "scripts-viewer-" + suffix + "@example.test"
	viewer := newClient(t, ts.URL)
	viewer.register(viewerEmail)
	var inv struct {
		AcceptURL string `json:"accept_url"`
	}
	owner.mustJSON("POST", base+"/invitations", map[string]any{"email": viewerEmail, "role": "viewer"}, 201, &inv)
	viewer.mustJSON("POST", "/api/invitations/"+inv.AcceptURL[strings.LastIndex(inv.AcceptURL, "/")+1:]+"/accept", nil, 200, nil)

	stranger := newClient(t, ts.URL)
	stranger.register("scripts-stranger-" + suffix + "@example.test")
	var otherTeam struct{ ID string }
	stranger.mustJSON("POST", "/api/teams", map[string]any{"name": "Elsewhere " + suffix}, 201, &otherTeam)

	srvA, privA := enrolWithServicesKey(t, owner, ts.URL, team.ID, "scripts-a-"+short, "203.0.113.40", nil)
	srvB, privB := enrolWithServicesKey(t, owner, ts.URL, team.ID, "scripts-b-"+short, "203.0.113.41", nil)

	// Both fake agents share one script.run/service.* responder: script.run succeeds with a marker
	// in its log line; service.list/service.action answer with a scripted systemd state.
	const outputMarker = "script-output-marker-" + "s3cr3t"
	respond := func(op agentproto.SignedOp) (any, string) {
		switch op.Kind {
		case agentproto.KindScriptRun:
			return agentproto.ScriptRunResult{ExitCode: 0, Bytes: len(outputMarker) + 1}, ""
		case agentproto.KindServiceList:
			return agentproto.ServiceListResult{Units: []agentproto.ServiceUnit{
				{Name: "caddy.service", LoadState: "loaded", ActiveState: "active", SubState: "running", Enabled: true},
			}}, ""
		case agentproto.KindServiceAction:
			return agentproto.ServiceActionResult{Unit: "caddy.service", ActiveState: "active", SubState: "running"}, ""
		default:
			return nil, "this fake agent does not run " + op.Kind
		}
	}
	agentA := startFakeAgent(t, ts.URL, srvA, privA, respond)
	agentA.mu.Lock()
	agentA.events = func(op agentproto.SignedOp) []agentproto.Event {
		if op.Kind != agentproto.KindScriptRun {
			return nil
		}
		return []agentproto.Event{{Kind: agentproto.EventLog, Stream: "stdout", Line: outputMarker}}
	}
	agentA.mu.Unlock()
	agentB := startFakeAgent(t, ts.URL, srvB, privB, respond)
	agentByServer := map[string]*fakeAgent{srvA: agentA, srvB: agentB}

	var script scriptJSON
	t.Run("create a script: roles and other teams", func(t *testing.T) {
		body := map[string]any{"name": "deploy-key-" + short, "body": "#!/bin/bash\necho hi\n", "timeout_seconds": 120}
		viewer.mustJSON("POST", base+"/scripts", body, 403, nil)
		stranger.mustJSON("POST", base+"/scripts", body, 404, nil)
		owner.mustJSON("POST", base+"/scripts", body, 201, &script)
		if script.ID == "" || script.Name != "deploy-key-"+short || script.RunAs != "root" || script.TimeoutSeconds != 120 {
			t.Fatalf("created script: %+v", script)
		}
		owner.mustJSON("POST", base+"/scripts", body, 409, nil) // the name is taken

		var list struct {
			Scripts []scriptJSON `json:"scripts"`
		}
		viewer.mustJSON("GET", base+"/scripts", nil, 200, &list) // any member reads
		found := false
		for _, s := range list.Scripts {
			found = found || s.ID == script.ID
		}
		if !found {
			t.Fatalf("the viewer's list did not include the new script: %+v", list.Scripts)
		}
		stranger.mustJSON("GET", base+"/scripts/"+script.ID, nil, 404, nil)
	})

	var run runStartJSON
	t.Run("run a script on two servers, and its history", func(t *testing.T) {
		viewer.mustJSON("POST", base+"/scripts/run", map[string]any{"script_id": script.ID, "servers": []string{srvA, srvB}}, 403, nil)

		owner.mustJSON("POST", base+"/scripts/run", map[string]any{"script_id": script.ID, "servers": []string{srvA, srvB}}, 202, &run)
		if run.Run.ID == "" || len(run.Servers) != 2 {
			t.Fatalf("run: %+v", run)
		}
		for _, rs := range run.Servers {
			if rs.OperationID == nil {
				t.Fatalf("every target should already carry an operation id: %+v", rs)
			}
			agentByServer[rs.ServerID].waitOp(t, *rs.OperationID)
		}

		var detail runDetailJSON
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			owner.mustJSON("GET", base+"/scripts/runs/"+run.Run.ID, nil, 200, &detail)
			if detail.Run.Status == "ok" || detail.Run.Status == "failed" {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		if detail.Run.Status != "ok" {
			t.Fatalf("run never finished ok: %+v", detail.Run)
		}
		if len(detail.Servers) != 2 {
			t.Fatalf("detail servers: %+v", detail.Servers)
		}
		for _, rs := range detail.Servers {
			if rs.ServerID == srvA && !strings.Contains(rs.Output, outputMarker) {
				t.Fatalf("server A's output should carry the marker: %q", rs.Output)
			}
		}
		// A run's detail carries output (like a schedule's), so a viewer is refused (owner/admin/
		// deployer only — tenancy.AbilityDeploy); a stranger gets the team-isolation 404.
		viewer.mustJSON("GET", base+"/scripts/runs/"+run.Run.ID, nil, 403, nil)
		stranger.mustJSON("GET", base+"/scripts/runs/"+run.Run.ID, nil, 404, nil)

		var list struct {
			Runs []db.ScriptRun `json:"runs"`
		}
		owner.mustJSON("GET", base+"/scripts/runs", nil, 200, &list)
		found := false
		for _, r := range list.Runs {
			found = found || r.ID == run.Run.ID
		}
		if !found {
			t.Fatalf("run history did not include the run: %+v", list.Runs)
		}
	})

	t.Run("a built-in's param allow list rejects a value outside its enum", func(t *testing.T) {
		builtinName := "php"
		if status, text := owner.do("POST", base+"/scripts/run", map[string]any{
			"builtin_name": builtinName, "servers": []string{srvA}, "params": map[string]any{"version": "9.9"},
		}); status != 422 {
			t.Fatalf("a bad enum value: %d (want 422): %s", status, text)
		}
		if status, text := owner.do("POST", base+"/scripts/run", map[string]any{
			"builtin_name": builtinName, "servers": []string{srvA}, "params": map[string]any{"no_such_param": "x"},
		}); status != 422 {
			t.Fatalf("an undeclared param: %d (want 422): %s", status, text)
		}
	})

	t.Run("cross-team isolation", func(t *testing.T) {
		stranger.mustJSON("DELETE", base+"/scripts/"+script.ID, map[string]any{"confirm": script.Name}, 404, nil)
		stranger.mustJSON("POST", base+"/scripts/run", map[string]any{"script_id": script.ID, "servers": []string{srvA}}, 404, nil)
	})

	t.Run("services: list, an action, and the agent's own unit is always refused", func(t *testing.T) {
		var list agentproto.ServiceListResult
		owner.mustJSON("GET", base+"/servers/"+srvA+"/services", nil, 200, &list)
		if len(list.Units) != 1 || list.Units[0].Name != "caddy.service" || list.Units[0].ActiveState != "active" {
			t.Fatalf("service list: %+v", list.Units)
		}
		viewer.mustJSON("GET", base+"/servers/"+srvA+"/services", nil, 200, nil) // any member reads

		var action agentproto.ServiceActionResult
		viewer.mustJSON("POST", base+"/servers/"+srvA+"/services/action", map[string]any{"unit": "caddy.service", "action": "restart"}, 403, nil)
		owner.mustJSON("POST", base+"/servers/"+srvA+"/services/action", map[string]any{"unit": "caddy.service", "action": "restart"}, 200, &action)
		if action.Unit != "caddy.service" || action.ActiveState != "active" {
			t.Fatalf("service action: %+v", action)
		}

		for _, unit := range agentproto.ProtectedUnits {
			if status, text := owner.do("POST", base+"/servers/"+srvA+"/services/action", map[string]any{"unit": unit, "action": "restart"}); status != 422 {
				t.Fatalf("%s must always be refused: %d: %s", unit, status, text)
			}
		}
		stranger.mustJSON("GET", base+"/servers/"+srvA+"/services", nil, 404, nil)
	})
}
