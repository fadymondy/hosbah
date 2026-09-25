-- MH-576 D5/D4 row-level security: scripts, script_runs, script_run_servers.
--
-- scripts: a team's own script library — full team CRUD, plus a system policy for the built-in
-- catalogue lookups and anything the provisioning worker or a sweep needs across teams.
ALTER TABLE scripts ENABLE ROW LEVEL SECURITY;
ALTER TABLE scripts FORCE ROW LEVEL SECURITY;
CREATE POLICY scripts_team_select ON scripts FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY scripts_team_insert ON scripts FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY scripts_team_update ON scripts FOR UPDATE
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY scripts_team_delete ON scripts FOR DELETE
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY scripts_system_all ON scripts FOR ALL
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');

-- script_runs: a run's history is team-readable; only the control plane (system scope, inside the
-- same tenancy.Tx as the team's own insert — see internal/schedules/service.go's StartRun pattern)
-- creates and settles a run, so team INSERT/UPDATE are still allowed for the request that starts a
-- run to also see its own freshly-created row, but only the row it just made (checked by the
-- handler, not RLS, exactly like operations rows a team INSERTs). No team DELETE: history is kept.
ALTER TABLE script_runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE script_runs FORCE ROW LEVEL SECURITY;
CREATE POLICY script_runs_team_select ON script_runs FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY script_runs_team_insert ON script_runs FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY script_runs_team_update ON script_runs FOR UPDATE
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY script_runs_system_all ON script_runs FOR ALL
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');

-- script_run_servers: one row per targeted server; team reads for the run view's per-server tabs
-- and history, system scope owns every write (enqueuing the operation, then settling it as the
-- agent's events arrive) so a run's status can never be forged by a team-scoped request.
ALTER TABLE script_run_servers ENABLE ROW LEVEL SECURITY;
ALTER TABLE script_run_servers FORCE ROW LEVEL SECURITY;
CREATE POLICY script_run_servers_team_select ON script_run_servers FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY script_run_servers_system_all ON script_run_servers FOR ALL
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');
