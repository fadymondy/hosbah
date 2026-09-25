-- Hosbah (MH-574 E13): per-team quotas, a platform-admin soft guardrail on top of the platform-wide
-- max_servers_per_team (internal/platform/settings.go). NULL = unlimited, the default. One row per
-- team, created lazily the first time a platform admin sets one; a team with no row reads as every
-- field NULL (internal/quota).
CREATE TABLE team_quotas (
    team_id text PRIMARY KEY,
    max_servers bigint,
    max_apps bigint,
    max_data_services bigint,
    updated_by text,
    updated_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE team_quotas ADD CONSTRAINT team_quotas_max_servers_check CHECK (max_servers IS NULL OR max_servers BETWEEN 0 AND 100000);
ALTER TABLE team_quotas ADD CONSTRAINT team_quotas_max_apps_check CHECK (max_apps IS NULL OR max_apps BETWEEN 0 AND 100000);
ALTER TABLE team_quotas ADD CONSTRAINT team_quotas_max_data_services_check CHECK (max_data_services IS NULL OR max_data_services BETWEEN 0 AND 100000);
