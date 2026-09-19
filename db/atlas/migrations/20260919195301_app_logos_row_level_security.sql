-- app_logos row-level security. Members of the team read their apps' logos; writes are
-- team-scoped (the API additionally requires owner or admin, like the appearance PATCH). No
-- agent and no system access: nothing else touches logos. App deletion cascades through the
-- foreign key, which row security does not apply to.
ALTER TABLE app_logos ENABLE ROW LEVEL SECURITY;
ALTER TABLE app_logos FORCE ROW LEVEL SECURITY;
CREATE POLICY app_logos_team_select ON app_logos FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_logos_team_insert ON app_logos FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_logos_team_update ON app_logos FOR UPDATE
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_logos_team_delete ON app_logos FOR DELETE
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
