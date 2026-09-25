-- log_drains row-level security. Members of the team read and write (the handlers gate writes to
-- owner or admin, tenancy.AbilityManageServers-equivalent). System scope is needed because status
-- updates (shipped/dropped counters, last_success_at, last_error) arrive asynchronously off the
-- agent's WebSocket connection, outside any one team's tenancy.Tx.
ALTER TABLE log_drains ENABLE ROW LEVEL SECURITY;
ALTER TABLE log_drains FORCE ROW LEVEL SECURITY;
CREATE POLICY log_drains_team_select ON log_drains FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY log_drains_team_insert ON log_drains FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY log_drains_team_update ON log_drains FOR UPDATE
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY log_drains_team_delete ON log_drains FOR DELETE
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY log_drains_system_all ON log_drains FOR ALL
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');
