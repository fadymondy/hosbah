-- MH-576 D4 fix: script_run_servers was missing the team INSERT/UPDATE policies StartRun's own
-- comment (internal/scripts/service.go) already promised — the caller's team transaction (member
-- scope: tenancy.Tx{UserID: ...} plus app.team_id, not app.system) creates one row per target and
-- then sets its operation_id, exactly like script_runs already allows. Settling a run to its final
-- status (ok/failed/timeout, exit_code, output) stays system-scope only, via script_run_servers_system_all.
CREATE POLICY script_run_servers_team_insert ON script_run_servers FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY script_run_servers_team_update ON script_run_servers FOR UPDATE
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
