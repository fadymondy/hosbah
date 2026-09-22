-- app_installs row-level security. Members of the team read; writes are team-scoped (the API
-- additionally requires manage_apps). System scope has full access: the install worker records how
-- recipe.install ended. secrets is sealed with the team DEK; no route returns it. App deletion
-- cascades through the foreign key, which row security does not apply to.
ALTER TABLE app_installs ENABLE ROW LEVEL SECURITY;
ALTER TABLE app_installs FORCE ROW LEVEL SECURITY;
CREATE POLICY app_installs_team_select ON app_installs FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_installs_team_insert ON app_installs FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_installs_team_update ON app_installs FOR UPDATE
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_installs_team_delete ON app_installs FOR DELETE
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_installs_system_all ON app_installs FOR ALL
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');
