-- app_realtime row-level security. Members of the team read; writes are team-scoped (the API
-- additionally requires manage_apps). System scope has full access: the deploy pipeline reads an
-- app's realtime configuration to publish the process's port and route its path. App deletion
-- cascades through the foreign key, which row security does not apply to.
ALTER TABLE app_realtime ENABLE ROW LEVEL SECURITY;
ALTER TABLE app_realtime FORCE ROW LEVEL SECURITY;
CREATE POLICY app_realtime_team_select ON app_realtime FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_realtime_team_insert ON app_realtime FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_realtime_team_update ON app_realtime FOR UPDATE
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_realtime_team_delete ON app_realtime FOR DELETE
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_realtime_system_all ON app_realtime FOR ALL
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');
