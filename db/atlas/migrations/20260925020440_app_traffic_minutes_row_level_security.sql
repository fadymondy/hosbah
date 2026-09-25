-- app_traffic_minutes row-level security. Members of the team read their apps' traffic history (no
-- team writes: only agents and the retention job record buckets). An authenticated agent folds its
-- own aggregates into its own server's rows (INSERT … ON CONFLICT DO UPDATE needs SELECT and
-- UPDATE too). The retention job runs in system scope: it deletes minute rows, folds them into
-- 15-minute rows, and prunes old ones.
ALTER TABLE app_traffic_minutes ENABLE ROW LEVEL SECURITY;
ALTER TABLE app_traffic_minutes FORCE ROW LEVEL SECURITY;
CREATE POLICY app_traffic_minutes_team_select ON app_traffic_minutes FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_traffic_minutes_agent_select ON app_traffic_minutes FOR SELECT
  USING (server_id = nullif(current_setting('app.server_id', true), ''));
CREATE POLICY app_traffic_minutes_agent_insert ON app_traffic_minutes FOR INSERT
  WITH CHECK (server_id = nullif(current_setting('app.server_id', true), ''));
CREATE POLICY app_traffic_minutes_agent_update ON app_traffic_minutes FOR UPDATE
  USING (server_id = nullif(current_setting('app.server_id', true), ''))
  WITH CHECK (server_id = nullif(current_setting('app.server_id', true), ''));
CREATE POLICY app_traffic_minutes_system_all ON app_traffic_minutes FOR ALL
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');
