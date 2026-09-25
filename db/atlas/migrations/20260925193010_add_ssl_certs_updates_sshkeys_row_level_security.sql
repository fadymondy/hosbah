-- ssl_certificates / server_update_statuses / ssh_keys / ssh_key_installs row-level security
-- (MH-580 J6, MH-578 C8/C10).
--
-- ssl_certificates: members of the team read their own certificates; only the system-scope checker
-- (internal/certcheck) ever writes them, always in Scope{System: true, TeamID: ...} so it can read
-- its own upsert back (see CLAUDE.md's note on system scope + RETURNING).
ALTER TABLE ssl_certificates ENABLE ROW LEVEL SECURITY;
ALTER TABLE ssl_certificates FORCE ROW LEVEL SECURITY;
CREATE POLICY ssl_certificates_team_select ON ssl_certificates FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY ssl_certificates_system_all ON ssl_certificates FOR ALL
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');

-- server_update_statuses: members read and write in plain team scope — the API queues the
-- pkg.list/pkg.upgrade/pkg.autoupdate.set operation and persists its result itself, like a
-- deployment; nothing here is written in system scope.
ALTER TABLE server_update_statuses ENABLE ROW LEVEL SECURITY;
ALTER TABLE server_update_statuses FORCE ROW LEVEL SECURITY;
CREATE POLICY server_update_statuses_team_all ON server_update_statuses FOR ALL
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));

-- ssh_keys / ssh_key_installs: members read and write in plain team scope.
ALTER TABLE ssh_keys ENABLE ROW LEVEL SECURITY;
ALTER TABLE ssh_keys FORCE ROW LEVEL SECURITY;
CREATE POLICY ssh_keys_team_all ON ssh_keys FOR ALL
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));

ALTER TABLE ssh_key_installs ENABLE ROW LEVEL SECURITY;
ALTER TABLE ssh_key_installs FORCE ROW LEVEL SECURITY;
CREATE POLICY ssh_key_installs_team_all ON ssh_key_installs FOR ALL
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
