-- monitors and monitor_incidents row-level security. Members of the team read; writes are
-- team-scoped (the API additionally requires owner or admin). The evaluator (internal/monitors)
-- runs in system scope with full access: it claims due monitors of every team, records results
-- and opens / resolves incidents. App deletion cascades through the foreign keys, which row
-- security does not apply to.
ALTER TABLE monitors ENABLE ROW LEVEL SECURITY;
ALTER TABLE monitors FORCE ROW LEVEL SECURITY;
CREATE POLICY monitors_team_select ON monitors FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY monitors_team_insert ON monitors FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY monitors_team_update ON monitors FOR UPDATE
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY monitors_team_delete ON monitors FOR DELETE
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY monitors_system_all ON monitors FOR ALL
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');
ALTER TABLE monitor_incidents ENABLE ROW LEVEL SECURITY;
ALTER TABLE monitor_incidents FORCE ROW LEVEL SECURITY;
CREATE POLICY monitor_incidents_team_select ON monitor_incidents FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY monitor_incidents_team_insert ON monitor_incidents FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY monitor_incidents_team_update ON monitor_incidents FOR UPDATE
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY monitor_incidents_team_delete ON monitor_incidents FOR DELETE
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY monitor_incidents_system_all ON monitor_incidents FOR ALL
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');
