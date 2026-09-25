-- sftp_users row-level security. Members of the team read and write (the handlers gate writes to
-- owner/admin, tenancy.AbilityManageServers); no agent and no system access needed — the agent
-- never queries this table, it only receives sftp.user operations built by the team-scoped tx.
ALTER TABLE sftp_users ENABLE ROW LEVEL SECURITY;
ALTER TABLE sftp_users FORCE ROW LEVEL SECURITY;
CREATE POLICY sftp_users_team_select ON sftp_users FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY sftp_users_team_insert ON sftp_users FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY sftp_users_team_update ON sftp_users FOR UPDATE
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY sftp_users_team_delete ON sftp_users FOR DELETE
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
