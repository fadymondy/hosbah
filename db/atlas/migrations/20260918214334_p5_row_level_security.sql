-- P5 row-level security: the Caddy proxy manager's proxy hosts (hand-written; Atlas OSS does not
-- manage policies). Same rules as domains: the team's rows for members and team MCP tokens; the
-- control plane's system scope reads them (hostname uniqueness across teams, and the on-demand TLS
-- ask, which must see every team's verified hostnames).
ALTER TABLE proxy_hosts ENABLE ROW LEVEL SECURITY;
ALTER TABLE proxy_hosts FORCE ROW LEVEL SECURITY;
CREATE POLICY proxy_hosts_team ON proxy_hosts FOR ALL
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY proxy_hosts_system_select ON proxy_hosts FOR SELECT
  USING (coalesce(current_setting('app.system', true), '') = 'on');
