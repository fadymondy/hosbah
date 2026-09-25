-- Import runs, items and decisions (MH-574 A4), row-level security. Team rows only: the importer
-- runs in the server's team scope and the REST/MCP decision endpoints run in the caller's team
-- scope, so no system or agent policy is needed.
ALTER TABLE import_runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE import_runs FORCE ROW LEVEL SECURITY;
CREATE POLICY import_runs_team ON import_runs FOR ALL
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));

ALTER TABLE import_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE import_items FORCE ROW LEVEL SECURITY;
CREATE POLICY import_items_team ON import_items FOR ALL
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
