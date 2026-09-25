-- Hosbah: an app_service_link records that one of an app's env vars points at a shared service or a
-- data service — discovered by parsing the app's (decrypted, never stored here) env values for a
-- connection URL and matching its host:port, or set by hand. service_kind picks which table
-- service_id names; there is no DB foreign key across the two (kept simple, matched in Go), but
-- reads always re-check the service still belongs to the team.
CREATE TABLE app_service_links (
    id text PRIMARY KEY DEFAULT gen_random_uuid()::text,
    team_id text NOT NULL,
    app_id text NOT NULL,
    service_kind text NOT NULL,
    service_id text NOT NULL,
    variable text NOT NULL,
    source text NOT NULL DEFAULT 'auto',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE app_service_links ADD CONSTRAINT app_service_links_service_kind_check CHECK (service_kind IN ('shared', 'data'));
ALTER TABLE app_service_links ADD CONSTRAINT app_service_links_source_check CHECK (source IN ('auto', 'manual'));
CREATE UNIQUE INDEX app_service_links_app_variable_key ON app_service_links (app_id, variable);
CREATE INDEX app_service_links_team_idx ON app_service_links (team_id);
CREATE INDEX app_service_links_app_idx ON app_service_links (app_id);
