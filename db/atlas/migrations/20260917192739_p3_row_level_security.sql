-- P3 row-level security: managed data services, their databases and users, and backups
-- (hand-written; Atlas OSS does not manage policies). Same rules as P0/P1/P2: handlers always
-- filter by team_id themselves; these policies are what still holds when one forgets, and they bind
-- because the app connects as cloudy_app (not superuser, not BYPASSRLS) with FORCE on every table.
--
-- Contexts, as before:
--   app.team_id   a member's request or a team MCP token
--   app.system    the provisioning/backup worker and the backup scheduler — never derived from
--                 anything a request carries. It reacts to signed agent operation results (sets a
--                 service's status/endpoint, records a backup's snapshot and restore-test) and reads
--                 the sealed root/user credentials to re-seal them to the target agent's own key.
-- Permissive policies on one table are OR'ed.

-- data_services
ALTER TABLE data_services ENABLE ROW LEVEL SECURITY;
ALTER TABLE data_services FORCE ROW LEVEL SECURITY;
CREATE POLICY data_services_team ON data_services FOR ALL
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY data_services_system_select ON data_services FOR SELECT
  USING (coalesce(current_setting('app.system', true), '') = 'on');
CREATE POLICY data_services_system_update ON data_services FOR UPDATE
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');

-- databases
ALTER TABLE databases ENABLE ROW LEVEL SECURITY;
ALTER TABLE databases FORCE ROW LEVEL SECURITY;
CREATE POLICY databases_team ON databases FOR ALL
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY databases_system_select ON databases FOR SELECT
  USING (coalesce(current_setting('app.system', true), '') = 'on');
CREATE POLICY databases_system_update ON databases FOR UPDATE
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');

-- database_users: the worker reads the sealed password to build a connection string it seals again
-- for the target agent (attach) or for an audited reveal.
ALTER TABLE database_users ENABLE ROW LEVEL SECURITY;
ALTER TABLE database_users FORCE ROW LEVEL SECURITY;
CREATE POLICY database_users_team ON database_users FOR ALL
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY database_users_system_select ON database_users FOR SELECT
  USING (coalesce(current_setting('app.system', true), '') = 'on');
CREATE POLICY database_users_system_update ON database_users FOR UPDATE
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');

-- backup_schedules: the scheduler reads due rows and marks them run.
ALTER TABLE backup_schedules ENABLE ROW LEVEL SECURITY;
ALTER TABLE backup_schedules FORCE ROW LEVEL SECURITY;
CREATE POLICY backup_schedules_team ON backup_schedules FOR ALL
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY backup_schedules_system_select ON backup_schedules FOR SELECT
  USING (coalesce(current_setting('app.system', true), '') = 'on');
CREATE POLICY backup_schedules_system_update ON backup_schedules FOR UPDATE
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');

-- backups: the team queues manual backups and reads history; the scheduler inserts scheduled runs
-- and both the worker and scheduler update a run's result and its restore-test status.
ALTER TABLE backups ENABLE ROW LEVEL SECURITY;
ALTER TABLE backups FORCE ROW LEVEL SECURITY;
CREATE POLICY backups_team ON backups FOR ALL
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY backups_system_select ON backups FOR SELECT
  USING (coalesce(current_setting('app.system', true), '') = 'on');
CREATE POLICY backups_system_insert ON backups FOR INSERT
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');
CREATE POLICY backups_system_update ON backups FOR UPDATE
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');
