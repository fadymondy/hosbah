-- Hand-written schema fragment (MH-574 A4) — desired state for atlas migrate diff.
--
-- import_runs: the outcome of the last import sync of one server (created fresh on every sync;
-- import_runs_server_key keeps one row per server — the latest run only). status is 'ok' or
-- 'error'; error carries the message when the sync itself failed (a query error, not a per-item
-- one — those live on the item).
CREATE TABLE import_runs (
    id text PRIMARY KEY DEFAULT gen_random_uuid()::text,
    team_id text NOT NULL,
    server_id text NOT NULL,
    status text NOT NULL DEFAULT 'ok',
    error text,
    discovered integer NOT NULL DEFAULT 0,
    created integer NOT NULL DEFAULT 0,
    updated integer NOT NULL DEFAULT 0,
    missing integer NOT NULL DEFAULT 0,
    removed integer NOT NULL DEFAULT 0,
    ignored integer NOT NULL DEFAULT 0,
    started_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE import_runs ADD CONSTRAINT import_runs_status_check CHECK (status IN ('ok', 'error'));
CREATE UNIQUE INDEX import_runs_server_key ON import_runs (server_id);
CREATE INDEX import_runs_team_idx ON import_runs (team_id);

-- import_items: every workload the last run of a server discovered, and what became of it. Kept in
-- step with import_runs (upserted per origin_key on every sync, replaced when no longer discovered
-- — see internal/importer.Sync). outcome is one of imported_app, imported_data_service,
-- imported_shared_service, linked_guest, updated, missing, removed, skipped, ignored, error.
CREATE TABLE import_items (
    id text PRIMARY KEY DEFAULT gen_random_uuid()::text,
    team_id text NOT NULL,
    server_id text NOT NULL,
    run_id text NOT NULL,
    origin_kind text NOT NULL,
    origin_key text NOT NULL,
    name text NOT NULL,
    detected_kind text,
    target text NOT NULL,
    outcome text NOT NULL,
    reason text,
    error text,
    record_id text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE import_items ADD CONSTRAINT import_items_target_check CHECK (target IN ('app', 'data_service'));
ALTER TABLE import_items ADD CONSTRAINT import_items_outcome_check CHECK (outcome IN
    ('imported_app', 'imported_data_service', 'imported_shared_service', 'linked_guest', 'updated', 'missing', 'removed', 'skipped', 'ignored', 'error'));
CREATE UNIQUE INDEX import_items_server_key ON import_items (server_id, origin_key);
CREATE INDEX import_items_team_idx ON import_items (team_id);
CREATE INDEX import_items_run_idx ON import_items (run_id);
