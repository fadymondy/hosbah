-- MH-575 H5 row-level security: app_rum_settings and rum_samples.
--
-- app_rum_settings: members of the team read and manage their own app's setting; the public ingest
-- endpoint (internal/rum, POST /api/rum/{key}) resolves a presented key in pure
-- tenancy.Scope{TokenHash: sha256(key)} — never a team context — so it can only ever match the
-- explicit key-hash policy below, exactly like monitors' ping-token policies
-- (20260925061853_monitor_channels_heartbeat_status_pages_row_level_security.sql). The retention
-- sweep and settings-management helpers that need every row across teams run in system scope.
ALTER TABLE app_rum_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE app_rum_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY app_rum_settings_team_select ON app_rum_settings FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_rum_settings_team_insert ON app_rum_settings FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_rum_settings_team_update ON app_rum_settings FOR UPDATE
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_rum_settings_team_delete ON app_rum_settings FOR DELETE
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_rum_settings_key_hash_select ON app_rum_settings FOR SELECT
  USING (key_hash IS NOT NULL AND key_hash = nullif(current_setting('app.token_hash', true), ''));
CREATE POLICY app_rum_settings_system_all ON app_rum_settings FOR ALL
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');

-- rum_samples: members of the team read (summary / top paths / timeseries); writes are team-scoped
-- — the ingest handler calls tenancy.SetTeam right after resolving the app's team from its key, the
-- same pattern the heartbeat ping handler uses before writing the monitor's own team. No update or
-- delete policy for the team: a sample is write-once and only ever removed by the retention sweep
-- (system scope).
ALTER TABLE rum_samples ENABLE ROW LEVEL SECURITY;
ALTER TABLE rum_samples FORCE ROW LEVEL SECURITY;
CREATE POLICY rum_samples_team_select ON rum_samples FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY rum_samples_team_insert ON rum_samples FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY rum_samples_system_all ON rum_samples FOR ALL
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');
