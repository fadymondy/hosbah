-- mail_relay_credentials row-level security (MH-577 I6). Members of the team read and write their
-- own team's rows (team roles/abilities enforced in internal/api and internal/mcp; RLS is the
-- backstop). The background upsert/status worker and the agent's own operation results run in
-- system+team scope like every other mail row.
ALTER TABLE mail_relay_credentials ENABLE ROW LEVEL SECURITY;
ALTER TABLE mail_relay_credentials FORCE ROW LEVEL SECURITY;
CREATE POLICY mail_relay_credentials_team_all ON mail_relay_credentials FOR ALL
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY mail_relay_credentials_system_all ON mail_relay_credentials FOR ALL
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');
