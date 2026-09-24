-- app_canvas row-level security. Members of the team read where their apps' canvas boxes sit;
-- writes are team-scoped (the API additionally requires manage_apps: owner or admin). No agent
-- and no system access: nothing else touches a canvas. App deletion cascades through the foreign
-- key, which row security does not apply to.
ALTER TABLE app_canvas ENABLE ROW LEVEL SECURITY;
ALTER TABLE app_canvas FORCE ROW LEVEL SECURITY;
CREATE POLICY app_canvas_team_select ON app_canvas FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_canvas_team_insert ON app_canvas FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_canvas_team_update ON app_canvas FOR UPDATE
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_canvas_team_delete ON app_canvas FOR DELETE
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
