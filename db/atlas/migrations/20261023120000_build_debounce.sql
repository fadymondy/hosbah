-- Build debounce, path filters and skipped pushes (internal/deploy/buildrules.go). Desired state:
-- internal/db/schema/deployment.sql and app.sql. Hand-written, like the migrations before it.
-- Additive only: every new column is nullable or has a default, so the running control plane keeps
-- working against it until the new one starts (its deploys simply never wait).

-- An automatic deploy is not claimed before not_before (NULL = at once, as before); queued_since is
-- when the first push it coalesces arrived, coalesced how many waiting requests it replaced.
ALTER TABLE "deployments" ADD COLUMN "not_before" timestamptz;
ALTER TABLE "deployments" ADD COLUMN "queued_since" timestamptz;
ALTER TABLE "deployments" ADD COLUMN "coalesced" integer NOT NULL DEFAULT 0;

-- A push that touched only paths the app's [build] rules ignore is recorded 'skipped'.
ALTER TABLE "deployments" DROP CONSTRAINT "deployments_status_check";
ALTER TABLE "deployments" ADD CONSTRAINT "deployments_status_check" CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'cancelled', 'skipped'));

-- hosbah.toml's [build] as the last build read it.
ALTER TABLE "apps" ADD COLUMN "build_rules" jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE "apps" ADD CONSTRAINT "apps_build_rules_check" CHECK (jsonb_typeof(build_rules) = 'object' AND octet_length(build_rules::text) <= 65536);
