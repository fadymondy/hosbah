-- shared_services / app_service_links row-level security. Members of the team read and write both;
-- no agent and no system access needed: the sync runs inside the same team-scoped tx as
-- AutoLinkFacts (internal/ops/ws.go), and manual scan/edit run through the team API.
ALTER TABLE shared_services ENABLE ROW LEVEL SECURITY;
ALTER TABLE shared_services FORCE ROW LEVEL SECURITY;
CREATE POLICY shared_services_team_select ON shared_services FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY shared_services_team_insert ON shared_services FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY shared_services_team_update ON shared_services FOR UPDATE
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY shared_services_team_delete ON shared_services FOR DELETE
  USING (team_id = nullif(current_setting('app.team_id', true), ''));

ALTER TABLE app_service_links ENABLE ROW LEVEL SECURITY;
ALTER TABLE app_service_links FORCE ROW LEVEL SECURITY;
CREATE POLICY app_service_links_team_select ON app_service_links FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_service_links_team_insert ON app_service_links FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_service_links_team_update ON app_service_links FOR UPDATE
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_service_links_team_delete ON app_service_links FOR DELETE
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
