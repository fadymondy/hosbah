-- MH-576 D7 row-level security: products, product_instances, product_keys, product_upgrades.
--
-- All four are plain team data reached only through internal/api/products.go (member tier) and
-- the product-key public endpoint (internal/api/product_keys_public.go). The public endpoint
-- doesn't know its team until it has resolved the bearer key, so it opens its transaction scoped
-- to the token's hash instead (Scope{TokenHash: hash}, the same pattern as app_rum_settings and
-- app_apm_settings in 20260914101756_row_level_security.sql), and switches to the key's own team
-- (tenancy.SetTeam) as soon as the key row is found — every other read and write in that request
-- then runs under the normal team policies below. No system policy is needed: nothing outside a
-- team's own request or its own product key touches these rows, exactly like server_costs and
-- cost_line_items.
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
-- The public product-key API resolves the bearer key before it knows the team (see the header
-- comment above): it needs to find exactly the one row whose hash matches, by token hash alone.
CREATE POLICY product_keys_token_select ON product_keys FOR SELECT
  USING (key_hash = nullif(current_setting('app.token_hash', true), ''));
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
