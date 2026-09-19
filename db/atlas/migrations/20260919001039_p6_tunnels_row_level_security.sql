-- P6 row-level security: tunnels (frp / tailscale) are team rows only. Every write, including the
-- settle after the agent reports, runs in team scope, so no system policy is needed.
ALTER TABLE tunnels ENABLE ROW LEVEL SECURITY;
ALTER TABLE tunnels FORCE ROW LEVEL SECURITY;
CREATE POLICY tunnels_team ON tunnels FOR ALL
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
