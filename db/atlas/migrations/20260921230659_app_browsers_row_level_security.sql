-- app_browsers row-level security. Members of the team read; writes are team-scoped (the API
-- additionally requires manage_apps). System scope has full access: the browser worker records how
-- browser.create ended. token_sealed is sealed with the team DEK; no route returns it. App deletion
-- cascades through the foreign key, which row security does not apply to.
ALTER TABLE app_browsers ENABLE ROW LEVEL SECURITY;
ALTER TABLE app_browsers FORCE ROW LEVEL SECURITY;
CREATE POLICY app_browsers_team_select ON app_browsers FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_browsers_team_insert ON app_browsers FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_browsers_team_update ON app_browsers FOR UPDATE
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_browsers_team_delete ON app_browsers FOR DELETE
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_browsers_system_all ON app_browsers FOR ALL
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');
