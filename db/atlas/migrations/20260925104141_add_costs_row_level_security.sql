-- MH-575 H10 row-level security: server_costs and cost_line_items.
--
-- Both tables are plain team data: a member reads their team's prices and line items, and an
-- owner/admin (checked in internal/api/costs.go and internal/mcp/costs.go, not here) writes them.
-- No system policy: nothing outside a team's own request ever touches these rows — there is no
-- sweep or cross-team aggregation for cost data, unlike RUM's retention sweep.
ALTER TABLE server_costs ENABLE ROW LEVEL SECURITY;
ALTER TABLE server_costs FORCE ROW LEVEL SECURITY;
CREATE POLICY server_costs_team_select ON server_costs FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY server_costs_team_insert ON server_costs FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY server_costs_team_update ON server_costs FOR UPDATE
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY server_costs_team_delete ON server_costs FOR DELETE
  USING (team_id = nullif(current_setting('app.team_id', true), ''));

ALTER TABLE cost_line_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE cost_line_items FORCE ROW LEVEL SECURITY;
CREATE POLICY cost_line_items_team_select ON cost_line_items FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY cost_line_items_team_insert ON cost_line_items FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY cost_line_items_team_update ON cost_line_items FOR UPDATE
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY cost_line_items_team_delete ON cost_line_items FOR DELETE
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
