-- server_metrics row-level security. Members of the team read their servers' history (no team
-- writes: only agents record samples). An authenticated agent folds its own heartbeats into its
-- own server's rows (INSERT … ON CONFLICT DO UPDATE needs SELECT and UPDATE too). The retention
-- job runs in system scope: it moves minute rows into 15-minute rows and prunes old ones
-- (DELETE … RETURNING and the upsert need every command). Server deletion cascades through the
-- foreign key, which row security does not apply to.
ALTER TABLE server_metrics ENABLE ROW LEVEL SECURITY;
ALTER TABLE server_metrics FORCE ROW LEVEL SECURITY;
CREATE POLICY server_metrics_team_select ON server_metrics FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY server_metrics_agent_select ON server_metrics FOR SELECT
  USING (server_id = nullif(current_setting('app.server_id', true), ''));
CREATE POLICY server_metrics_agent_insert ON server_metrics FOR INSERT
  WITH CHECK (server_id = nullif(current_setting('app.server_id', true), ''));
CREATE POLICY server_metrics_agent_update ON server_metrics FOR UPDATE
  USING (server_id = nullif(current_setting('app.server_id', true), ''))
  WITH CHECK (server_id = nullif(current_setting('app.server_id', true), ''));
CREATE POLICY server_metrics_system_all ON server_metrics FOR ALL
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');
