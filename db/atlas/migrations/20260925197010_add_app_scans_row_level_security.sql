-- app_scans row-level security (MH-580 J5).
--
-- Members read and write in plain team scope: both the REST handler (runAppScan) and the MCP tool
-- (mcpRunAppScan) enqueue the agent op and record the result themselves, inside tenancy.Tx with
-- app.team_id set — never in system scope, so no *_system_* policy is needed here.
ALTER TABLE app_scans ENABLE ROW LEVEL SECURITY;
ALTER TABLE app_scans FORCE ROW LEVEL SECURITY;
CREATE POLICY app_scans_team_all ON app_scans FOR ALL
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
