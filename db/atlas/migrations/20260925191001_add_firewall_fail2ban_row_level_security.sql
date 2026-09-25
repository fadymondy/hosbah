-- MH-580 J1/J2 row-level security: firewall rules/settings and fail2ban settings (hand-written;
-- Atlas OSS does not manage policies). Team-scoped like every other server-level resource; the
-- control plane also reads them in system scope, so the operation queue (dispatcher, sweeper) can
-- render an nftables ruleset or fail2ban config for a server without a member's session.
ALTER TABLE firewall_rules ENABLE ROW LEVEL SECURITY;
ALTER TABLE firewall_rules FORCE ROW LEVEL SECURITY;
CREATE POLICY firewall_rules_team ON firewall_rules FOR ALL
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY firewall_rules_system_select ON firewall_rules FOR SELECT
  USING (coalesce(current_setting('app.system', true), '') = 'on');

ALTER TABLE firewall_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE firewall_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY firewall_settings_team ON firewall_settings FOR ALL
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY firewall_settings_system_select ON firewall_settings FOR SELECT
  USING (coalesce(current_setting('app.system', true), '') = 'on');

ALTER TABLE fail2ban_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE fail2ban_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY fail2ban_settings_team ON fail2ban_settings FOR ALL
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY fail2ban_settings_system_select ON fail2ban_settings FOR SELECT
  USING (coalesce(current_setting('app.system', true), '') = 'on');
