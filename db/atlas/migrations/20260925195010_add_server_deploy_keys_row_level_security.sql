-- server_deploy_keys row-level security (MH-578 C11).
--
-- Members read and write in plain team scope — the API generates the key through
-- deploykey.generate and persists the result itself, like ssh_keys.
ALTER TABLE server_deploy_keys ENABLE ROW LEVEL SECURITY;
ALTER TABLE server_deploy_keys FORCE ROW LEVEL SECURITY;
CREATE POLICY server_deploy_keys_team_all ON server_deploy_keys FOR ALL
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
