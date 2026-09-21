-- app_volumes and app_buckets row-level security. Members of the team read; writes are team-scoped
-- (the API additionally requires manage_apps). System scope has full access: the deploy pipeline
-- reads an app's volumes to mount them, and the bucket worker records how bucket.create ended.
-- app_buckets.secret_sealed is sealed with the team DEK; no route returns it. App deletion cascades
-- through the foreign keys, which row security does not apply to.
ALTER TABLE app_volumes ENABLE ROW LEVEL SECURITY;
ALTER TABLE app_volumes FORCE ROW LEVEL SECURITY;
CREATE POLICY app_volumes_team_select ON app_volumes FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_volumes_team_insert ON app_volumes FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_volumes_team_update ON app_volumes FOR UPDATE
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_volumes_team_delete ON app_volumes FOR DELETE
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_volumes_system_all ON app_volumes FOR ALL
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');
ALTER TABLE app_buckets ENABLE ROW LEVEL SECURITY;
ALTER TABLE app_buckets FORCE ROW LEVEL SECURITY;
CREATE POLICY app_buckets_team_select ON app_buckets FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_buckets_team_insert ON app_buckets FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_buckets_team_update ON app_buckets FOR UPDATE
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_buckets_team_delete ON app_buckets FOR DELETE
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY app_buckets_system_all ON app_buckets FOR ALL
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');
