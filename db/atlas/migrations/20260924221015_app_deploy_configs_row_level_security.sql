-- app_deploy_configs / app_deploy_config_versions row-level security. Members of the team read an
-- app's deploy configuration and its version history; writes are team-scoped (the API additionally
-- requires deploy: deployer, admin or owner). No agent and no system access: nothing else touches a
-- deploy configuration. App deletion cascades through the foreign key, which row security does not
-- apply to.
ALTER TABLE app_deploy_configs ENABLE ROW LEVEL SECURITY;
ALTER TABLE app_deploy_configs FORCE ROW LEVEL SECURITY;
CREATE POLICY app_deploy_configs_team_select ON app_deploy_configs FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_deploy_configs_team_insert ON app_deploy_configs FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_deploy_configs_team_update ON app_deploy_configs FOR UPDATE
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_deploy_configs_team_delete ON app_deploy_configs FOR DELETE
  USING (team_id = nullif(current_setting('app.team_id', true), ''));

ALTER TABLE app_deploy_config_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE app_deploy_config_versions FORCE ROW LEVEL SECURITY;
CREATE POLICY app_deploy_config_versions_team_select ON app_deploy_config_versions FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_deploy_config_versions_team_insert ON app_deploy_config_versions FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_deploy_config_versions_team_update ON app_deploy_config_versions FOR UPDATE
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_deploy_config_versions_team_delete ON app_deploy_config_versions FOR DELETE
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
