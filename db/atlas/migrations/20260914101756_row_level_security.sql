-- Report numbers come from a sequence: under RLS a reporter sees only their own reports, so
-- max(number)+1 would collide. Here rather than in the schema fragment because Atlas OSS does
-- not manage sequences (a Pro feature).
CREATE SEQUENCE reports_number_seq;

-- Row-level security: the tenancy backstop (hand-written; Atlas OSS does not manage policies).
--
-- Handlers always scope by team_id themselves. These policies are what still holds when one
-- forgets: a query with no tenant context sees no tenant rows, and a query in team A's context
-- cannot read or write team B's. They bind only because the app connects as cloudy_app, which is
-- neither a superuser nor BYPASSRLS, and FORCE makes them apply to the table owner too.
--
-- Context is set per transaction by internal/tenancy (set_config(..., true) = SET LOCAL):
--   app.team_id         the team the request acts in
--   app.user_id         the signed-in user
--   app.token_hash      SHA-256 of a presented bearer/invite token (finds exactly that row)
--   app.platform_admin  'on' for platform administration (admin MCP, admin pages)
-- A pooled connection that has seen a SET LOCAL reads an empty string afterwards, hence nullif.

-- teams: members see their teams; the context team is visible; inserting requires owner = you.
ALTER TABLE teams ENABLE ROW LEVEL SECURITY;
ALTER TABLE teams FORCE ROW LEVEL SECURITY;
CREATE POLICY teams_select ON teams FOR SELECT USING (
  coalesce(current_setting('app.platform_admin', true), '') = 'on'
  OR id = nullif(current_setting('app.team_id', true), '')
  OR id IN (SELECT tm.team_id FROM team_members tm WHERE tm.user_id = nullif(current_setting('app.user_id', true), ''))
);
CREATE POLICY teams_insert ON teams FOR INSERT WITH CHECK (
  coalesce(current_setting('app.platform_admin', true), '') = 'on'
  OR (id = nullif(current_setting('app.team_id', true), '') AND owner_id = nullif(current_setting('app.user_id', true), ''))
);
CREATE POLICY teams_update ON teams FOR UPDATE
  USING (coalesce(current_setting('app.platform_admin', true), '') = 'on' OR id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (coalesce(current_setting('app.platform_admin', true), '') = 'on' OR id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY teams_delete ON teams FOR DELETE
  USING (coalesce(current_setting('app.platform_admin', true), '') = 'on' OR id = nullif(current_setting('app.team_id', true), ''));

-- team_members: the context team's roster, plus your own memberships (to list your teams).
ALTER TABLE team_members ENABLE ROW LEVEL SECURITY;
ALTER TABLE team_members FORCE ROW LEVEL SECURITY;
CREATE POLICY team_members_select ON team_members FOR SELECT USING (
  coalesce(current_setting('app.platform_admin', true), '') = 'on'
  OR team_id = nullif(current_setting('app.team_id', true), '')
  OR user_id = nullif(current_setting('app.user_id', true), '')
);
CREATE POLICY team_members_insert ON team_members FOR INSERT WITH CHECK (
  coalesce(current_setting('app.platform_admin', true), '') = 'on' OR team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY team_members_update ON team_members FOR UPDATE
  USING (coalesce(current_setting('app.platform_admin', true), '') = 'on' OR team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (coalesce(current_setting('app.platform_admin', true), '') = 'on' OR team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY team_members_delete ON team_members FOR DELETE USING (
  coalesce(current_setting('app.platform_admin', true), '') = 'on' OR team_id = nullif(current_setting('app.team_id', true), ''));

-- invitations: the context team's; an invitee can find exactly the invitation whose token they hold.
ALTER TABLE invitations ENABLE ROW LEVEL SECURITY;
ALTER TABLE invitations FORCE ROW LEVEL SECURITY;
CREATE POLICY invitations_select ON invitations FOR SELECT USING (
  coalesce(current_setting('app.platform_admin', true), '') = 'on'
  OR team_id = nullif(current_setting('app.team_id', true), '')
  OR token_hash = nullif(current_setting('app.token_hash', true), '')
);
CREATE POLICY invitations_update ON invitations FOR UPDATE
  USING (coalesce(current_setting('app.platform_admin', true), '') = 'on'
         OR team_id = nullif(current_setting('app.team_id', true), '')
         OR token_hash = nullif(current_setting('app.token_hash', true), ''))
  WITH CHECK (coalesce(current_setting('app.platform_admin', true), '') = 'on'
         OR team_id = nullif(current_setting('app.team_id', true), '')
         OR token_hash = nullif(current_setting('app.token_hash', true), ''));
CREATE POLICY invitations_insert ON invitations FOR INSERT WITH CHECK (
  coalesce(current_setting('app.platform_admin', true), '') = 'on' OR team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY invitations_delete ON invitations FOR DELETE USING (
  coalesce(current_setting('app.platform_admin', true), '') = 'on' OR team_id = nullif(current_setting('app.team_id', true), ''));

-- mcp_tokens: the context team's; the MCP transport finds exactly the token whose hash it holds.
ALTER TABLE mcp_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE mcp_tokens FORCE ROW LEVEL SECURITY;
CREATE POLICY mcp_tokens_select ON mcp_tokens FOR SELECT USING (
  coalesce(current_setting('app.platform_admin', true), '') = 'on'
  OR team_id = nullif(current_setting('app.team_id', true), '')
  OR token_hash = nullif(current_setting('app.token_hash', true), '')
);
CREATE POLICY mcp_tokens_update ON mcp_tokens FOR UPDATE
  USING (coalesce(current_setting('app.platform_admin', true), '') = 'on'
         OR team_id = nullif(current_setting('app.team_id', true), '')
         OR token_hash = nullif(current_setting('app.token_hash', true), ''))
  WITH CHECK (coalesce(current_setting('app.platform_admin', true), '') = 'on'
         OR team_id = nullif(current_setting('app.team_id', true), '')
         OR token_hash = nullif(current_setting('app.token_hash', true), ''));
CREATE POLICY mcp_tokens_insert ON mcp_tokens FOR INSERT WITH CHECK (
  coalesce(current_setting('app.platform_admin', true), '') = 'on' OR team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY mcp_tokens_delete ON mcp_tokens FOR DELETE USING (
  coalesce(current_setting('app.platform_admin', true), '') = 'on' OR team_id = nullif(current_setting('app.team_id', true), ''));

-- admin_mcp_tokens: platform administration only, plus lookup by the presented token's hash.
ALTER TABLE admin_mcp_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE admin_mcp_tokens FORCE ROW LEVEL SECURITY;
CREATE POLICY admin_mcp_tokens_select ON admin_mcp_tokens FOR SELECT USING (
  coalesce(current_setting('app.platform_admin', true), '') = 'on'
  OR token_hash = nullif(current_setting('app.token_hash', true), ''));
CREATE POLICY admin_mcp_tokens_update ON admin_mcp_tokens FOR UPDATE
  USING (coalesce(current_setting('app.platform_admin', true), '') = 'on' OR token_hash = nullif(current_setting('app.token_hash', true), ''))
  WITH CHECK (coalesce(current_setting('app.platform_admin', true), '') = 'on' OR token_hash = nullif(current_setting('app.token_hash', true), ''));
CREATE POLICY admin_mcp_tokens_insert ON admin_mcp_tokens FOR INSERT WITH CHECK (
  coalesce(current_setting('app.platform_admin', true), '') = 'on');
CREATE POLICY admin_mcp_tokens_delete ON admin_mcp_tokens FOR DELETE USING (
  coalesce(current_setting('app.platform_admin', true), '') = 'on');

-- reports: the product's own tracker. A reporter sees and files their own; triage is platform admin.
ALTER TABLE reports ENABLE ROW LEVEL SECURITY;
ALTER TABLE reports FORCE ROW LEVEL SECURITY;
CREATE POLICY reports_select ON reports FOR SELECT USING (
  coalesce(current_setting('app.platform_admin', true), '') = 'on'
  OR reporter_id = nullif(current_setting('app.user_id', true), ''));
CREATE POLICY reports_insert ON reports FOR INSERT WITH CHECK (
  coalesce(current_setting('app.platform_admin', true), '') = 'on'
  OR reporter_id = nullif(current_setting('app.user_id', true), ''));
CREATE POLICY reports_update ON reports FOR UPDATE
  USING (coalesce(current_setting('app.platform_admin', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.platform_admin', true), '') = 'on');
CREATE POLICY reports_delete ON reports FOR DELETE USING (
  coalesce(current_setting('app.platform_admin', true), '') = 'on');

ALTER TABLE report_comments ENABLE ROW LEVEL SECURITY;
ALTER TABLE report_comments FORCE ROW LEVEL SECURITY;
CREATE POLICY report_comments_select ON report_comments FOR SELECT USING (
  coalesce(current_setting('app.platform_admin', true), '') = 'on'
  OR report_id IN (SELECT r.id FROM reports r WHERE r.reporter_id = nullif(current_setting('app.user_id', true), '')));
CREATE POLICY report_comments_insert ON report_comments FOR INSERT WITH CHECK (
  coalesce(current_setting('app.platform_admin', true), '') = 'on'
  OR (author_id = nullif(current_setting('app.user_id', true), '')
      AND report_id IN (SELECT r.id FROM reports r WHERE r.reporter_id = nullif(current_setting('app.user_id', true), ''))));
CREATE POLICY report_comments_update ON report_comments FOR UPDATE
  USING (coalesce(current_setting('app.platform_admin', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.platform_admin', true), '') = 'on');
CREATE POLICY report_comments_delete ON report_comments FOR DELETE USING (
  coalesce(current_setting('app.platform_admin', true), '') = 'on');

-- activity_log: append-only. Reads are per team; a write may not claim another team. There is
-- no UPDATE or DELETE policy, so history cannot be edited through the app role.
ALTER TABLE activity_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE activity_log FORCE ROW LEVEL SECURITY;
CREATE POLICY activity_log_select ON activity_log FOR SELECT USING (
  coalesce(current_setting('app.platform_admin', true), '') = 'on'
  OR team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY activity_log_insert ON activity_log FOR INSERT WITH CHECK (
  team_id IS NULL
  OR team_id = nullif(current_setting('app.team_id', true), '')
  OR coalesce(current_setting('app.platform_admin', true), '') = 'on');
