-- P4 row-level security: approval requests for destructive MCP calls (hand-written; Atlas OSS does
-- not manage policies). Same rules as P0–P3: handlers always filter by team_id themselves; these
-- policies are what still holds when one forgets, and they bind because the app connects as
-- cloudy_app (not superuser, not BYPASSRLS) with FORCE on every table.
--
-- Contexts:
--   app.team_id   a member's request or a team MCP token — both sides of this feature. The MCP
--                 token files the request; a member of the same team decides it. One policy covers
--                 both, because an approval is only ever meaningful inside its own team.
--   app.system    the expiry sweeper, which runs with no team and must age out every team's rows.
--                 It gets UPDATE only: nothing here justifies a system-wide INSERT, and a SELECT
--                 would let a bug read one team's pending asks while serving another.
--
-- There is deliberately no DELETE policy for anyone. An approval request is the audit trail for
-- "a machine credential asked to destroy something", and used and rejected rows are exactly the
-- ones worth keeping — so they age through status, never by disappearing.

ALTER TABLE approval_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE approval_requests FORCE ROW LEVEL SECURITY;

CREATE POLICY approval_requests_team_select ON approval_requests FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY approval_requests_team_insert ON approval_requests FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY approval_requests_team_update ON approval_requests FOR UPDATE
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));

CREATE POLICY approval_requests_system_update ON approval_requests FOR UPDATE
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');
