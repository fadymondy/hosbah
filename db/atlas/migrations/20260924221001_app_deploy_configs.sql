-- Create "app_deploy_config_versions" table
CREATE TABLE "public"."app_deploy_config_versions" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "app_id" text NOT NULL,
  "team_id" text NOT NULL,
  "version" integer NOT NULL,
  "mode" text NOT NULL,
  "preset" text NOT NULL DEFAULT '',
  "steps" jsonb NOT NULL DEFAULT '[]',
  "updated_by" text NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "app_deploy_config_versions_app_fk" FOREIGN KEY ("app_id", "team_id") REFERENCES "public"."apps" ("id", "team_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "app_deploy_config_versions_mode_check" CHECK (mode = ANY (ARRAY['auto'::text, 'fast'::text, 'full'::text])),
  CONSTRAINT "app_deploy_config_versions_steps_check" CHECK ((jsonb_typeof(steps) = 'array'::text) AND (octet_length((steps)::text) <= 65536))
);
-- Create index "app_deploy_config_versions_app_version_idx" to table: "app_deploy_config_versions"
CREATE UNIQUE INDEX "app_deploy_config_versions_app_version_idx" ON "public"."app_deploy_config_versions" ("app_id", "version");
-- Create index "app_deploy_config_versions_team_idx" to table: "app_deploy_config_versions"
CREATE INDEX "app_deploy_config_versions_team_idx" ON "public"."app_deploy_config_versions" ("team_id");
-- Create "app_deploy_configs" table
CREATE TABLE "public"."app_deploy_configs" (
  "app_id" text NOT NULL,
  "team_id" text NOT NULL,
  "mode" text NOT NULL DEFAULT 'auto',
  "preset" text NOT NULL DEFAULT '',
  "steps" jsonb NOT NULL DEFAULT '[]',
  "version" integer NOT NULL DEFAULT 1,
  "updated_by" text NULL,
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("app_id"),
  CONSTRAINT "app_deploy_configs_app_fk" FOREIGN KEY ("app_id", "team_id") REFERENCES "public"."apps" ("id", "team_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "app_deploy_configs_mode_check" CHECK (mode = ANY (ARRAY['auto'::text, 'fast'::text, 'full'::text])),
  CONSTRAINT "app_deploy_configs_steps_check" CHECK ((jsonb_typeof(steps) = 'array'::text) AND (octet_length((steps)::text) <= 65536))
);
-- Create index "app_deploy_configs_team_idx" to table: "app_deploy_configs"
CREATE INDEX "app_deploy_configs_team_idx" ON "public"."app_deploy_configs" ("team_id");
