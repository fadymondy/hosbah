-- MH-576 B11 row-level security: site_templates.
--
-- A team's own saved site templates — full team CRUD, plus a system policy for anything the
-- provisioning worker or a sweep needs across teams. Built-in templates are not rows in this
-- table (internal/sitetemplate.Builtins, embedded in the binary) — only a team's own saved ones
-- live here.
ALTER TABLE site_templates ENABLE ROW LEVEL SECURITY;
ALTER TABLE site_templates FORCE ROW LEVEL SECURITY;
CREATE POLICY site_templates_team_select ON site_templates FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY site_templates_team_insert ON site_templates FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY site_templates_team_update ON site_templates FOR UPDATE
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY site_templates_team_delete ON site_templates FOR DELETE
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY site_templates_system_all ON site_templates FOR ALL
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');
