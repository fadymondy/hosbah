-- Platform-admin row-level security (hand-written; Atlas OSS does not manage policies).
--
-- The admin pages and the admin MCP run with app.platform_admin = 'on' (internal/tenancy).
-- P0 gave that context the platform tables (teams, team_members, tokens, reports); the tenant
-- tables added since (servers, operations, apps, deployments, data_services) only answered a
-- team, an agent or the system workers. The admin overview and cross-team lists need to READ
-- them, so each gets a SELECT-only policy — no UPDATE, INSERT or DELETE: platform
-- administration looks at tenant infrastructure, it does not operate it. operations.params
-- stays unreadable in practice because no admin query selects it.

CREATE POLICY servers_platform_admin_select ON servers FOR SELECT
  USING (coalesce(current_setting('app.platform_admin', true), '') = 'on');
CREATE POLICY operations_platform_admin_select ON operations FOR SELECT
  USING (coalesce(current_setting('app.platform_admin', true), '') = 'on');
CREATE POLICY apps_platform_admin_select ON apps FOR SELECT
  USING (coalesce(current_setting('app.platform_admin', true), '') = 'on');
CREATE POLICY deployments_platform_admin_select ON deployments FOR SELECT
  USING (coalesce(current_setting('app.platform_admin', true), '') = 'on');
CREATE POLICY data_services_platform_admin_select ON data_services FOR SELECT
  USING (coalesce(current_setting('app.platform_admin', true), '') = 'on');

-- platform_settings: one row everybody may read (sign-up, team creation and AddServer enforce
-- its limits from any context, and part of it is public); only a platform admin writes it.
-- The one row is seeded before RLS is forced (migrations run as the app role).
INSERT INTO platform_settings (id) VALUES (true) ON CONFLICT (id) DO NOTHING;
ALTER TABLE platform_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE platform_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY platform_settings_select ON platform_settings FOR SELECT USING (true);
CREATE POLICY platform_settings_insert ON platform_settings FOR INSERT
  WITH CHECK (coalesce(current_setting('app.platform_admin', true), '') = 'on');
CREATE POLICY platform_settings_update ON platform_settings FOR UPDATE
  USING (coalesce(current_setting('app.platform_admin', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.platform_admin', true), '') = 'on');

-- platform_audit: platform admins read and append. No UPDATE or DELETE policy: append-only.
ALTER TABLE platform_audit ENABLE ROW LEVEL SECURITY;
ALTER TABLE platform_audit FORCE ROW LEVEL SECURITY;
CREATE POLICY platform_audit_select ON platform_audit FOR SELECT
  USING (coalesce(current_setting('app.platform_admin', true), '') = 'on');
CREATE POLICY platform_audit_insert ON platform_audit FOR INSERT
  WITH CHECK (coalesce(current_setting('app.platform_admin', true), '') = 'on');
