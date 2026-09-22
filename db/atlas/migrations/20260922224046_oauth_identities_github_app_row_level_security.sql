-- Row-level security for sign-in identities and the GitHub App (hand-written; Atlas OSS does not
-- manage policies).
--
-- user_identities: a signed-in user reads, touches and unlinks only their own (app.user_id, the
-- account settings). The sign-in callback runs before anybody is signed in: it finds an identity by
-- (provider, subject) and links new ones in system scope (internal/account). Platform administration
-- may read them. Deleting a user cascades through the foreign key, which row security does not
-- apply to. oauth_states, like account_challenges and users, has none: it is read before sign-in.
ALTER TABLE user_identities ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_identities FORCE ROW LEVEL SECURITY;
CREATE POLICY user_identities_own_select ON user_identities FOR SELECT
  USING (user_id = nullif(current_setting('app.user_id', true), '')
         OR coalesce(current_setting('app.platform_admin', true), '') = 'on');
CREATE POLICY user_identities_own_insert ON user_identities FOR INSERT
  WITH CHECK (user_id = nullif(current_setting('app.user_id', true), ''));
CREATE POLICY user_identities_own_update ON user_identities FOR UPDATE
  USING (user_id = nullif(current_setting('app.user_id', true), ''))
  WITH CHECK (user_id = nullif(current_setting('app.user_id', true), ''));
CREATE POLICY user_identities_own_delete ON user_identities FOR DELETE
  USING (user_id = nullif(current_setting('app.user_id', true), ''));
CREATE POLICY user_identities_system_all ON user_identities FOR ALL
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');

-- github_installations: members of the team read their installations; writes are the team's (the
-- API additionally requires an owner or admin) or the system's (the setup callback checks that no
-- other team holds the installation, and webhooks suspend, update and delete rows). Team deletion
-- cascades through the foreign key.
ALTER TABLE github_installations ENABLE ROW LEVEL SECURITY;
ALTER TABLE github_installations FORCE ROW LEVEL SECURITY;
CREATE POLICY github_installations_team_select ON github_installations FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY github_installations_team_insert ON github_installations FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY github_installations_team_update ON github_installations FOR UPDATE
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY github_installations_team_delete ON github_installations FOR DELETE
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY github_installations_system_all ON github_installations FOR ALL
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');

-- github_deliveries: the webhook's redelivery ledger, system scope only.
ALTER TABLE github_deliveries ENABLE ROW LEVEL SECURITY;
ALTER TABLE github_deliveries FORCE ROW LEVEL SECURITY;
CREATE POLICY github_deliveries_system_all ON github_deliveries FOR ALL
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');
