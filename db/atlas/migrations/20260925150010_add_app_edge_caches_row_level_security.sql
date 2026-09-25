-- app_edge_caches row-level security. Members of the team read and write (the handlers gate writes
-- to owner/admin, tenancy.AbilityManageApps); no agent and no system access needed — the agent never
-- queries this table, it only receives cache.set/cache.purge operations built by the team-scoped tx.
ALTER TABLE app_edge_caches ENABLE ROW LEVEL SECURITY;
ALTER TABLE app_edge_caches FORCE ROW LEVEL SECURITY;
CREATE POLICY app_edge_caches_team_select ON app_edge_caches FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_edge_caches_team_insert ON app_edge_caches FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_edge_caches_team_update ON app_edge_caches FOR UPDATE
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_edge_caches_team_delete ON app_edge_caches FOR DELETE
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
