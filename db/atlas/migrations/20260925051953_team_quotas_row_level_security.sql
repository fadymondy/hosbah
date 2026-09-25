-- team_quotas row-level security (MH-574 E13). Members of the team read their own quota (to show
-- usage vs limit); only a platform admin ever writes one, from the admin API/MCP, in
-- {PlatformAdmin: true} scope (no app.team_id set) — writes and admin reads use the platform-admin
-- clause, member reads use the team clause. No agent and no system access.
ALTER TABLE team_quotas ENABLE ROW LEVEL SECURITY;
ALTER TABLE team_quotas FORCE ROW LEVEL SECURITY;
CREATE POLICY team_quotas_select ON team_quotas FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), '') OR coalesce(current_setting('app.platform_admin', true), '') = 'on');
CREATE POLICY team_quotas_admin_insert ON team_quotas FOR INSERT
  WITH CHECK (coalesce(current_setting('app.platform_admin', true), '') = 'on');
CREATE POLICY team_quotas_admin_update ON team_quotas FOR UPDATE
  USING (coalesce(current_setting('app.platform_admin', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.platform_admin', true), '') = 'on');
