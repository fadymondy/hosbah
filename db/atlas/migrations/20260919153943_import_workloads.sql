-- Modify "apps" table
ALTER TABLE "public"."apps" ADD CONSTRAINT "apps_git_repo_check" CHECK ((source <> 'git'::text) OR ((repo_url IS NOT NULL) AND (branch IS NOT NULL))), ADD CONSTRAINT "apps_imported_origin_check" CHECK ((source <> 'imported'::text) OR ((origin_kind IS NOT NULL) AND (origin_key IS NOT NULL))), ADD CONSTRAINT "apps_imported_state_check" CHECK ((imported_state IS NULL) OR (imported_state = ANY (ARRAY['running'::text, 'stopped'::text, 'missing'::text]))), ADD CONSTRAINT "apps_origin_kind_check" CHECK ((origin_kind IS NULL) OR (origin_kind = ANY (ARRAY['container'::text, 'systemd'::text, 'guest'::text]))), ADD CONSTRAINT "apps_source_check" CHECK (source = ANY (ARRAY['git'::text, 'imported'::text])), ALTER COLUMN "repo_url" DROP NOT NULL, ALTER COLUMN "branch" DROP NOT NULL, ADD COLUMN "source" text NOT NULL DEFAULT 'git', ADD COLUMN "origin_kind" text NULL, ADD COLUMN "origin_key" text NULL, ADD COLUMN "origin" jsonb NULL, ADD COLUMN "imported_state" text NULL, ADD COLUMN "last_seen_at" timestamptz NULL;
-- Create index "apps_server_origin_key" to table: "apps"
CREATE UNIQUE INDEX "apps_server_origin_key" ON "public"."apps" ("server_id", "origin_key") WHERE (source = 'imported'::text);
-- Modify "data_services" table
ALTER TABLE "public"."data_services" DROP CONSTRAINT "data_services_kind_check", ADD CONSTRAINT "data_services_kind_check" CHECK (kind = ANY (ARRAY['postgres'::text, 'mysql'::text, 'redis'::text, 'minio'::text, 'mongodb'::text])), DROP CONSTRAINT "data_services_sealed_check", ADD CONSTRAINT "data_services_sealed_check" CHECK ((root_sealed IS NULL) OR (root_sealed ~~ 'csv1:%'::text)), ADD CONSTRAINT "data_services_imported_origin_check" CHECK ((source <> 'imported'::text) OR ((origin_kind IS NOT NULL) AND (origin_key IS NOT NULL))), ADD CONSTRAINT "data_services_imported_state_check" CHECK ((imported_state IS NULL) OR (imported_state = ANY (ARRAY['running'::text, 'stopped'::text, 'missing'::text]))), ADD CONSTRAINT "data_services_managed_check" CHECK ((source <> 'managed'::text) OR ((root_sealed IS NOT NULL) AND (kind <> 'mongodb'::text))), ADD CONSTRAINT "data_services_origin_kind_check" CHECK ((origin_kind IS NULL) OR (origin_kind = ANY (ARRAY['container'::text, 'systemd'::text, 'guest'::text]))), ADD CONSTRAINT "data_services_source_check" CHECK (source = ANY (ARRAY['managed'::text, 'imported'::text])), ALTER COLUMN "root_sealed" DROP NOT NULL, ADD COLUMN "source" text NOT NULL DEFAULT 'managed', ADD COLUMN "origin_kind" text NULL, ADD COLUMN "origin_key" text NULL, ADD COLUMN "origin" jsonb NULL, ADD COLUMN "imported_state" text NULL, ADD COLUMN "last_seen_at" timestamptz NULL;
-- Create index "data_services_server_origin_key" to table: "data_services"
CREATE UNIQUE INDEX "data_services_server_origin_key" ON "public"."data_services" ("server_id", "origin_key") WHERE (source = 'imported'::text);
-- Create "import_ignores" table
CREATE TABLE "public"."import_ignores" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "server_id" text NOT NULL,
  "kind" text NOT NULL,
  "origin_key" text NOT NULL,
  "name" text NOT NULL,
  "created_by" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "import_ignores_server_fk" FOREIGN KEY ("server_id", "team_id") REFERENCES "public"."servers" ("id", "team_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "import_ignores_kind_check" CHECK (kind = ANY (ARRAY['app'::text, 'data_service'::text]))
);
-- Create index "import_ignores_server_key" to table: "import_ignores"
CREATE UNIQUE INDEX "import_ignores_server_key" ON "public"."import_ignores" ("server_id", "origin_key");
-- Create index "import_ignores_team_idx" to table: "import_ignores"
CREATE INDEX "import_ignores_team_idx" ON "public"."import_ignores" ("team_id");
