-- MH-576 D7 row-level security: products, product_instances, product_keys, product_upgrades.
--
-- All four are plain team data reached only through internal/api/products.go (member tier) and
-- the product-key public endpoint (internal/api/product_keys_public.go), which runs in the
-- product's own team scope (Scope{TeamID: product.TeamID}) after resolving the key to its
-- product_id and team_id — never System scope. No system policy is needed: nothing outside a
-- team's own request touches these rows, exactly like server_costs and cost_line_items.
ALTER TABLE products ENABLE ROW LEVEL SECURITY;
ALTER TABLE products FORCE ROW LEVEL SECURITY;
CREATE POLICY products_team_select ON products FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY products_team_insert ON products FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY products_team_update ON products FOR UPDATE
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY products_team_delete ON products FOR DELETE
  USING (team_id = nullif(current_setting('app.team_id', true), ''));

ALTER TABLE product_instances ENABLE ROW LEVEL SECURITY;
ALTER TABLE product_instances FORCE ROW LEVEL SECURITY;
CREATE POLICY product_instances_team_select ON product_instances FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY product_instances_team_insert ON product_instances FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY product_instances_team_update ON product_instances FOR UPDATE
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY product_instances_team_delete ON product_instances FOR DELETE
  USING (team_id = nullif(current_setting('app.team_id', true), ''));

ALTER TABLE product_keys ENABLE ROW LEVEL SECURITY;
ALTER TABLE product_keys FORCE ROW LEVEL SECURITY;
CREATE POLICY product_keys_team_select ON product_keys FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY product_keys_team_insert ON product_keys FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY product_keys_team_update ON product_keys FOR UPDATE
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY product_keys_team_delete ON product_keys FOR DELETE
  USING (team_id = nullif(current_setting('app.team_id', true), ''));

ALTER TABLE product_upgrades ENABLE ROW LEVEL SECURITY;
ALTER TABLE product_upgrades FORCE ROW LEVEL SECURITY;
CREATE POLICY product_upgrades_team_select ON product_upgrades FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY product_upgrades_team_insert ON product_upgrades FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY product_upgrades_team_update ON product_upgrades FOR UPDATE
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY product_upgrades_team_delete ON product_upgrades FOR DELETE
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
