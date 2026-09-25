-- MH-575 H7 row-level security: app_apm_settings, apm_spans, apm_issues, apm_exception_events.
--
-- app_apm_settings: members of the team read and manage their own app's setting; the public OTLP
-- ingest endpoints (internal/apm, POST /api/otlp/v1/traces and /v1/logs) resolve a presented
-- Bearer key in pure tenancy.Scope{TokenHash: sha256(key)} — never a team context — so they can
-- only ever match the explicit key-hash policy below, exactly like app_rum_settings (H5) and
-- monitors' ping-token policies. The retention sweep and deploy env injection (which needs
-- key_sealed back in the clear) run in system scope.
ALTER TABLE app_apm_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE app_apm_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY app_apm_settings_team_select ON app_apm_settings FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_apm_settings_team_insert ON app_apm_settings FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_apm_settings_team_update ON app_apm_settings FOR UPDATE
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_apm_settings_team_delete ON app_apm_settings FOR DELETE
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_apm_settings_key_hash_select ON app_apm_settings FOR SELECT
  USING (key_hash IS NOT NULL AND key_hash = nullif(current_setting('app.token_hash', true), ''));
CREATE POLICY app_apm_settings_system_all ON app_apm_settings FOR ALL
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');

-- apm_spans: members read (endpoints summary / trace list / waterfall); writes are team-scoped —
-- the ingest handler calls tenancy.SetTeam right after resolving the app's team from its key, the
-- same pattern rum_samples and heartbeat pings use. No update or delete policy for the team: a
-- span is write-once and only ever removed by the retention sweep (system scope).
ALTER TABLE apm_spans ENABLE ROW LEVEL SECURITY;
ALTER TABLE apm_spans FORCE ROW LEVEL SECURITY;
CREATE POLICY apm_spans_team_select ON apm_spans FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY apm_spans_team_insert ON apm_spans FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY apm_spans_system_all ON apm_spans FOR ALL
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');

-- apm_issues: members read and update status (open/resolved/ignored); the ingest path upserts
-- issues in team scope right after resolving the app's team, same as apm_spans. The retention
-- sweep deletes aged-out issues in system scope.
ALTER TABLE apm_issues ENABLE ROW LEVEL SECURITY;
ALTER TABLE apm_issues FORCE ROW LEVEL SECURITY;
CREATE POLICY apm_issues_team_select ON apm_issues FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY apm_issues_team_insert ON apm_issues FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY apm_issues_team_update ON apm_issues FOR UPDATE
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY apm_issues_system_all ON apm_issues FOR ALL
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');

-- apm_exception_events: members read (an issue's latest example); writes are team-scoped from the
-- ingest path. The retention sweep deletes old events in system scope.
ALTER TABLE apm_exception_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE apm_exception_events FORCE ROW LEVEL SECURITY;
CREATE POLICY apm_exception_events_team_select ON apm_exception_events FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY apm_exception_events_team_insert ON apm_exception_events FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY apm_exception_events_system_all ON apm_exception_events FOR ALL
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');
