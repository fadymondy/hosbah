-- browser_auth_tokens row-level security. Members of the team mint a token for their own browser
-- ("Open browser"). The forward_auth exchange at <browser host>/.hosbah/auth does not yet know the
-- team when it consumes a token (it is deriving the team from the token itself), so it reads and
-- consumes tokens in system scope — the same shape as app_browsers and proxy_hosts.
ALTER TABLE browser_auth_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE browser_auth_tokens FORCE ROW LEVEL SECURITY;
CREATE POLICY browser_auth_tokens_team_select ON browser_auth_tokens FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY browser_auth_tokens_team_insert ON browser_auth_tokens FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY browser_auth_tokens_team_delete ON browser_auth_tokens FOR DELETE
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY browser_auth_tokens_system_all ON browser_auth_tokens FOR ALL
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');
