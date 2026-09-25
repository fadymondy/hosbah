-- app_httprules / proxy_host_httprules row-level security (MH-578 E4-E6 site HTTP rules). Members
-- of the team read and write (the handlers gate writes to owner/admin/deployer, AbilityManageApps);
-- no agent and no system access needed — the agent never queries these tables, it only receives
-- rules.set operations built by the team-scoped tx, same shape as app_edge_caches.
ALTER TABLE app_httprules ENABLE ROW LEVEL SECURITY;
ALTER TABLE app_httprules FORCE ROW LEVEL SECURITY;
CREATE POLICY app_httprules_team_select ON app_httprules FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_httprules_team_insert ON app_httprules FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_httprules_team_update ON app_httprules FOR UPDATE
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_httprules_team_delete ON app_httprules FOR DELETE
  USING (team_id = nullif(current_setting('app.team_id', true), ''));

ALTER TABLE proxy_host_httprules ENABLE ROW LEVEL SECURITY;
ALTER TABLE proxy_host_httprules FORCE ROW LEVEL SECURITY;
CREATE POLICY proxy_host_httprules_team_select ON proxy_host_httprules FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY proxy_host_httprules_team_insert ON proxy_host_httprules FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY proxy_host_httprules_team_update ON proxy_host_httprules FOR UPDATE
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY proxy_host_httprules_team_delete ON proxy_host_httprules FOR DELETE
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
