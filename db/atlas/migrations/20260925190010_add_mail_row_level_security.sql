-- mail_servers / mail_domains / mailboxes / mail_aliases row-level security (MH-577 I1/I2/I3).
-- Members of the team read and write their own team's rows (team roles/abilities are enforced in
-- internal/api and internal/mcp; RLS is the backstop). The background installer/DKIM-fetch workers
-- and the agent's own operation results run in system+team scope like every other data-service row.
ALTER TABLE mail_servers ENABLE ROW LEVEL SECURITY;
ALTER TABLE mail_servers FORCE ROW LEVEL SECURITY;
CREATE POLICY mail_servers_team_all ON mail_servers FOR ALL
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY mail_servers_system_all ON mail_servers FOR ALL
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');
-- The agent (app.server_id, its own enrolled server) reads its own mail server row only.
CREATE POLICY mail_servers_agent_select ON mail_servers FOR SELECT
  USING (server_id = nullif(current_setting('app.server_id', true), ''));

ALTER TABLE mail_domains ENABLE ROW LEVEL SECURITY;
ALTER TABLE mail_domains FORCE ROW LEVEL SECURITY;
CREATE POLICY mail_domains_team_all ON mail_domains FOR ALL
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY mail_domains_system_all ON mail_domains FOR ALL
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');

ALTER TABLE mailboxes ENABLE ROW LEVEL SECURITY;
ALTER TABLE mailboxes FORCE ROW LEVEL SECURITY;
CREATE POLICY mailboxes_team_all ON mailboxes FOR ALL
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY mailboxes_system_all ON mailboxes FOR ALL
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');

ALTER TABLE mail_aliases ENABLE ROW LEVEL SECURITY;
ALTER TABLE mail_aliases FORCE ROW LEVEL SECURITY;
CREATE POLICY mail_aliases_team_all ON mail_aliases FOR ALL
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY mail_aliases_system_all ON mail_aliases FOR ALL
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');
