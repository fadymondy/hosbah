-- data_service_metrics row-level security (MH-575 F6). Members of the team read their services'
-- history. Nothing is ever written in team scope: the sampler (a background loop in the API
-- process, reading db.insights over the agent op queue) and the retention job both run in system
-- scope, the same as server_metrics.
ALTER TABLE data_service_metrics ENABLE ROW LEVEL SECURITY;
ALTER TABLE data_service_metrics FORCE ROW LEVEL SECURITY;
CREATE POLICY data_service_metrics_team_select ON data_service_metrics FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY data_service_metrics_system_all ON data_service_metrics FOR ALL
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');
