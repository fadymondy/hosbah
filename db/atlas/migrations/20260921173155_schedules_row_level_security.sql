-- schedules and schedule_runs row-level security. Members of the team read; writes are team-scoped
-- (the API additionally requires the role: manage_apps to change a schedule, deploy to run one or
-- read a run's output). The scheduler (internal/schedules) runs in system scope with full access:
-- it claims due schedules of every team, creates runs and copies their outcome. App deletion
-- cascades through the foreign keys, which row security does not apply to.
ALTER TABLE schedules ENABLE ROW LEVEL SECURITY;
ALTER TABLE schedules FORCE ROW LEVEL SECURITY;
CREATE POLICY schedules_team_select ON schedules FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY schedules_team_insert ON schedules FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY schedules_team_update ON schedules FOR UPDATE
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY schedules_team_delete ON schedules FOR DELETE
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY schedules_system_all ON schedules FOR ALL
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');
ALTER TABLE schedule_runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE schedule_runs FORCE ROW LEVEL SECURITY;
CREATE POLICY schedule_runs_team_select ON schedule_runs FOR SELECT
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY schedule_runs_team_insert ON schedule_runs FOR INSERT
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY schedule_runs_team_update ON schedule_runs FOR UPDATE
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY schedule_runs_team_delete ON schedule_runs FOR DELETE
  USING (team_id = nullif(current_setting('app.team_id', true), ''));
CREATE POLICY schedule_runs_system_all ON schedule_runs FOR ALL
  USING (coalesce(current_setting('app.system', true), '') = 'on')
  WITH CHECK (coalesce(current_setting('app.system', true), '') = 'on');
