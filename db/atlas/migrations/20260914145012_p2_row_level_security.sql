-- P2 row-level security: apps and everything they own (hand-written; Atlas OSS does not manage
-- policies). Same rules as P0/P1: handlers always filter by team_id themselves; these policies are
-- what still holds when one forgets, and they bind because the app connects as cloudy_app (not
-- superuser, not BYPASSRLS) with FORCE on every table.
--
-- Contexts, as in P1:
--   app.team_id     a member's request or a team MCP token
--   app.token_hash  a presented deploy-hook token: it finds exactly the app whose hash it is
--   app.server_id   an authenticated agent: it may only append log lines of deployments on its own
--                   server (and take the next seq for them)
--   app.system      the deploy worker, the TLS `ask` endpoint and the GitHub webhook — never
--                   derived from anything a request carries
-- Permissive policies on one table are OR'ed.

-- apps
ALTER TABLE apps ENABLE ROW LEVEL SECURITY;
ALTER TABLE apps FORCE ROW LEVEL SECURITY;
CREATE POLICY apps_team ON apps FOR ALL
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY apps_hook_select ON apps FOR SELECT
  USING (deploy_hook_hash = nullif(current_setting('app.token_hash', true), ''));
CREATE POLICY apps_system_select ON apps FOR SELECT
  USING (coalesce(current_setting('app.system', true), '') = 'on');
CREATE POLICY apps_system_update ON apps FOR UPDATE
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');

-- process_types: rewritten by the worker from cloudy.toml; read by the team.
ALTER TABLE process_types ENABLE ROW LEVEL SECURITY;
ALTER TABLE process_types FORCE ROW LEVEL SECURITY;
CREATE POLICY process_types_team ON process_types FOR ALL
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY process_types_system ON process_types FOR ALL
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');

-- env_vars: the team's; the worker reads them (sealed) to seal them again for the target agent.
ALTER TABLE env_vars ENABLE ROW LEVEL SECURITY;
ALTER TABLE env_vars FORCE ROW LEVEL SECURITY;
CREATE POLICY env_vars_team ON env_vars FOR ALL
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY env_vars_system_select ON env_vars FOR SELECT
  USING (coalesce(current_setting('app.system', true), '') = 'on');

-- domains: the team's; the worker (routes) and the TLS ask endpoint read them.
ALTER TABLE domains ENABLE ROW LEVEL SECURITY;
ALTER TABLE domains FORCE ROW LEVEL SECURITY;
CREATE POLICY domains_team ON domains FOR ALL
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY domains_system_select ON domains FOR SELECT
  USING (coalesce(current_setting('app.system', true), '') = 'on');

-- deployments: the team's (queue, cancel, read); the worker's; an agent only takes the next log
-- seq of a deployment on its own server.
ALTER TABLE deployments ENABLE ROW LEVEL SECURITY;
ALTER TABLE deployments FORCE ROW LEVEL SECURITY;
CREATE POLICY deployments_team ON deployments FOR ALL
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY deployments_agent_select ON deployments FOR SELECT
  USING (server_id = nullif(current_setting('app.server_id', true), ''));
CREATE POLICY deployments_agent_update ON deployments FOR UPDATE
  USING (server_id = nullif(current_setting('app.server_id', true), ''))
  WITH CHECK (server_id = nullif(current_setting('app.server_id', true), ''));
CREATE POLICY deployments_system_select ON deployments FOR SELECT
  USING (coalesce(current_setting('app.system', true), '') = 'on');
CREATE POLICY deployments_system_insert ON deployments FOR INSERT
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');
CREATE POLICY deployments_system_update ON deployments FOR UPDATE
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');

-- deployment_logs: append-only (no UPDATE or DELETE policy; rows go only with their deployment).
ALTER TABLE deployment_logs ENABLE ROW LEVEL SECURITY;
ALTER TABLE deployment_logs FORCE ROW LEVEL SECURITY;
CREATE POLICY deployment_logs_team_select ON deployment_logs FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY deployment_logs_agent_select ON deployment_logs FOR SELECT
  USING (server_id = nullif(current_setting('app.server_id', true), ''));
CREATE POLICY deployment_logs_agent_insert ON deployment_logs FOR INSERT
  WITH CHECK (server_id = nullif(current_setting('app.server_id', true), ''));
CREATE POLICY deployment_logs_system_select ON deployment_logs FOR SELECT
  USING (coalesce(current_setting('app.system', true), '') = 'on');
CREATE POLICY deployment_logs_system_insert ON deployment_logs FOR INSERT
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');

-- releases: written only by the worker; the team reads them.
ALTER TABLE releases ENABLE ROW LEVEL SECURITY;
ALTER TABLE releases FORCE ROW LEVEL SECURITY;
CREATE POLICY releases_team_select ON releases FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY releases_system ON releases FOR ALL
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');

-- notifications: the worker writes; the team reads and marks read.
ALTER TABLE notifications ENABLE ROW LEVEL SECURITY;
ALTER TABLE notifications FORCE ROW LEVEL SECURITY;
CREATE POLICY notifications_team_select ON notifications FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY notifications_team_update ON notifications FOR UPDATE
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY notifications_system_insert ON notifications FOR INSERT
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');
