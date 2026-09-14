-- Cloudy: togo audit's activity_log, declared here so it is part of the desired schema.
--
-- The togo audit plugin creates this table itself at boot (CREATE TABLE IF NOT EXISTS, same
-- columns). Declaring it here means (1) the migration ledger owns it, so it exists — with
-- row-level security — before the plugin ever runs, and (2) a later `atlas migrate diff`
-- does not see a table missing from the desired state and emit DROP TABLE for it.
--
-- team_id is Cloudy's addition. The plugin has no tenant column, and its Log() writes through
-- the pool outside any transaction, so Cloudy writes entries in the same shape itself
-- (internal/tenancy/audit.go) inside the tenant transaction — the audit row commits or rolls
-- back with the change it describes. Append-only is enforced by RLS: there is no UPDATE or
-- DELETE policy.
CREATE TABLE activity_log (
    id text PRIMARY KEY,
    action text NOT NULL,
    subject_type text NOT NULL DEFAULT '',
    subject_id text NOT NULL DEFAULT '',
    causer_id text NOT NULL DEFAULT '',
    changes text NOT NULL DEFAULT '{}',
    ip text NOT NULL DEFAULT '',
    user_agent text NOT NULL DEFAULT '',
    created_at text NOT NULL,
    team_id text
);
CREATE INDEX idx_activity_subject ON activity_log (subject_type, subject_id, created_at);
CREATE INDEX idx_activity_causer ON activity_log (causer_id, created_at);
CREATE INDEX idx_activity_action ON activity_log (action, created_at);
CREATE INDEX activity_log_team_idx ON activity_log (team_id, created_at);
