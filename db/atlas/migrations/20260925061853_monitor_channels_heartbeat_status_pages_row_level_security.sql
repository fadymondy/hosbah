-- MH-575 H2/H3 row-level security: notification_channels, status_pages, and the heartbeat ping
-- token policies added to monitors (unchanged otherwise — see 20260921165842).
--
-- notification_channels: members of the team read; writes are team-scoped (the API additionally
-- requires owner or admin). The delivery worker (internal/notifications) and the monitor evaluator
-- run in system scope.
ALTER TABLE notification_channels ENABLE ROW LEVEL SECURITY;
ALTER TABLE notification_channels FORCE ROW LEVEL SECURITY;
CREATE POLICY notification_channels_team_select ON notification_channels FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY notification_channels_team_insert ON notification_channels FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY notification_channels_team_update ON notification_channels FOR UPDATE
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY notification_channels_team_delete ON notification_channels FOR DELETE
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY notification_channels_system_all ON notification_channels FOR ALL
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');

-- status_pages: members of the team read and manage their own team's rows; the public status page
-- endpoint (internal/api/public.go) reads a *published* page in pure system scope, which only ever
-- gets the explicit system policy below — never a team's private, unpublished pages, because the
-- public handler is the one that filters on slug + published (PublicStatusPageBySlug) before it
-- ever reaches this policy.
ALTER TABLE status_pages ENABLE ROW LEVEL SECURITY;
ALTER TABLE status_pages FORCE ROW LEVEL SECURITY;
CREATE POLICY status_pages_team_select ON status_pages FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY status_pages_team_insert ON status_pages FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY status_pages_team_update ON status_pages FOR UPDATE
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY status_pages_team_delete ON status_pages FOR DELETE
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY status_pages_system_all ON status_pages FOR ALL
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');

-- monitors: a heartbeat's ping token (app.token_hash, set by tenancy.Scope{TokenHash: ...}, exactly
-- like the app deploy-hook token in 20260914145012_p2_row_level_security.sql) finds and updates
-- exactly the one monitor it hashes to, whatever team it is in — the public ping handler then calls
-- tenancy.SetTeam to point the rest of its transaction at that monitor's team, same as the deploy
-- hook does with the app's team.
CREATE POLICY monitors_ping_token_select ON monitors FOR SELECT
  USING (ping_token_hash IS NOT NULL AND ping_token_hash = nullif(current_setting('app.token_hash', true), ''));
CREATE POLICY monitors_ping_token_update ON monitors FOR UPDATE
  USING (ping_token_hash IS NOT NULL AND ping_token_hash = nullif(current_setting('app.token_hash', true), ''))
  WITH CHECK (ping_token_hash IS NOT NULL AND ping_token_hash = nullif(current_setting('app.token_hash', true), ''));
