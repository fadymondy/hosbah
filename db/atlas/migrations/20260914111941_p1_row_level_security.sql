-- P1 row-level security: servers, agent enrolment, operations, operation logs, credentials
-- (hand-written; Atlas OSS does not manage policies). Same rules as P0's: handlers always filter
-- by team_id themselves, these policies are what still holds when one forgets, and they bind only
-- because the app connects as cloudy_app (not superuser, not BYPASSRLS) with FORCE on every table.
--
-- Two contexts are new in P1, next to P0's app.team_id / app.user_id / app.token_hash:
--   app.server_id  the agent's own server. Set only after the agent's challenge signature has
--                  verified against the public key it enrolled (internal/ops), or from a valid
--                  enrolment token. An agent connection sees its own server's rows — not its
--                  team's other servers, not their operations.
--   app.system     'on' for the control plane's own background workers (dispatcher, timeout
--                  sweeper, provisioning). Never derived from anything in a request.
-- Several permissive policies on one table are OR'ed, so P0's policies are untouched.

-- teams: the system context reads a team row to unwrap its DEK (sealing agent log lines).
CREATE POLICY teams_select_system ON teams FOR SELECT USING (
  coalesce(current_setting('app.system', true), '') = 'on');

-- servers
ALTER TABLE servers ENABLE ROW LEVEL SECURITY;
ALTER TABLE servers FORCE ROW LEVEL SECURITY;
CREATE POLICY servers_team ON servers FOR ALL
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY servers_agent_select ON servers FOR SELECT
  USING (id = nullif(current_setting('app.server_id', true), ''));
CREATE POLICY servers_agent_update ON servers FOR UPDATE
  USING (id = nullif(current_setting('app.server_id', true), ''))
  WITH CHECK (id = nullif(current_setting('app.server_id', true), ''));
CREATE POLICY servers_system_select ON servers FOR SELECT
  USING (coalesce(current_setting('app.system', true), '') = 'on');
CREATE POLICY servers_system_update ON servers FOR UPDATE
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');

-- agent_enrollments: the team's; an installer/agent finds exactly the enrolment whose token it holds.
ALTER TABLE agent_enrollments ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_enrollments FORCE ROW LEVEL SECURITY;
CREATE POLICY agent_enrollments_team ON agent_enrollments FOR ALL
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY agent_enrollments_token_select ON agent_enrollments FOR SELECT
  USING (token_hash = nullif(current_setting('app.token_hash', true), ''));
CREATE POLICY agent_enrollments_token_update ON agent_enrollments FOR UPDATE
  USING (token_hash = nullif(current_setting('app.token_hash', true), ''))
  WITH CHECK (token_hash = nullif(current_setting('app.token_hash', true), ''));

-- operations: the team's; the agent's own server's; the system workers'.
ALTER TABLE operations ENABLE ROW LEVEL SECURITY;
ALTER TABLE operations FORCE ROW LEVEL SECURITY;
CREATE POLICY operations_team ON operations FOR ALL
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY operations_agent_select ON operations FOR SELECT
  USING (server_id = nullif(current_setting('app.server_id', true), ''));
CREATE POLICY operations_agent_update ON operations FOR UPDATE
  USING (server_id = nullif(current_setting('app.server_id', true), ''))
  WITH CHECK (server_id = nullif(current_setting('app.server_id', true), ''));
CREATE POLICY operations_system_select ON operations FOR SELECT
  USING (coalesce(current_setting('app.system', true), '') = 'on');
CREATE POLICY operations_system_update ON operations FOR UPDATE
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');

-- operation_logs: append-only (no UPDATE or DELETE policy; rows go only with their operation).
-- Teams read; agents and system workers append for their own server / as the control plane.
ALTER TABLE operation_logs ENABLE ROW LEVEL SECURITY;
ALTER TABLE operation_logs FORCE ROW LEVEL SECURITY;
CREATE POLICY operation_logs_team_select ON operation_logs FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY operation_logs_agent_select ON operation_logs FOR SELECT
  USING (server_id = nullif(current_setting('app.server_id', true), ''));
CREATE POLICY operation_logs_agent_insert ON operation_logs FOR INSERT
  WITH CHECK (server_id = nullif(current_setting('app.server_id', true), ''));
CREATE POLICY operation_logs_system_select ON operation_logs FOR SELECT
  USING (coalesce(current_setting('app.system', true), '') = 'on');
CREATE POLICY operation_logs_system_insert ON operation_logs FOR INSERT
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');

-- credentials: the team's; the provisioning worker (system) reads the one a server was created with.
ALTER TABLE credentials ENABLE ROW LEVEL SECURITY;
ALTER TABLE credentials FORCE ROW LEVEL SECURITY;
CREATE POLICY credentials_team ON credentials FOR ALL
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY credentials_system_select ON credentials FOR SELECT
  USING (coalesce(current_setting('app.system', true), '') = 'on');

-- The provisioning worker (system) mints the one-time enrolment token for a server it is
-- creating on a provider — the token then travels in the guest's user-data or SSH bootstrap.
CREATE POLICY agent_enrollments_system_insert ON agent_enrollments FOR INSERT
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');
