-- Imported workloads, row-level security. import_ignores (tombstones of imported records the user
-- deleted) are team rows only: the importer syncs in the server's team scope and the delete that
-- writes a tombstone runs in the caller's team scope, so no system or agent policy is needed.
-- apps and data_services keep their existing policies: imported rows are ordinary team rows.
ALTER TABLE import_ignores ENABLE ROW LEVEL SECURITY;
ALTER TABLE import_ignores FORCE ROW LEVEL SECURITY;
CREATE POLICY import_ignores_team ON import_ignores FOR ALL
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
