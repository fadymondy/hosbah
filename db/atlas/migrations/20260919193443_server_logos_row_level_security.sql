-- server_logos row-level security. Members of the team read their servers' logos; writes are
-- team-scoped (the API additionally requires owner or admin, like the appearance PATCH). No
-- agent and no system access: nothing else touches logos. Server deletion cascades through the
-- foreign key, which row security does not apply to.
ALTER TABLE server_logos ENABLE ROW LEVEL SECURITY;
ALTER TABLE server_logos FORCE ROW LEVEL SECURITY;
CREATE POLICY server_logos_team_select ON server_logos FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY server_logos_team_insert ON server_logos FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY server_logos_team_update ON server_logos FOR UPDATE
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY server_logos_team_delete ON server_logos FOR DELETE
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
