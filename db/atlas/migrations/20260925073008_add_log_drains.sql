-- Create "log_drains" table
CREATE TABLE "public"."log_drains" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "app_id" text NOT NULL DEFAULT '',
  "name" text NOT NULL,
  "kind" text NOT NULL,
  "config_json" text NOT NULL DEFAULT '{}',
  "auth_sealed" text NOT NULL DEFAULT '',
  "internal" boolean NOT NULL DEFAULT false,
  "enabled" boolean NOT NULL DEFAULT true,
  "shipped" bigint NOT NULL DEFAULT 0,
  "dropped" bigint NOT NULL DEFAULT 0,
  "last_success_at" timestamptz NULL,
  "last_error_at" timestamptz NULL,
  "last_error" text NOT NULL DEFAULT '',
  "created_by" text NOT NULL DEFAULT '',
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "log_drains_kind_check" CHECK (kind = ANY (ARRAY['syslog'::text, 'http'::text, 'loki'::text]))
);
-- Create index "log_drains_app_idx" to table: "log_drains"
CREATE INDEX "log_drains_app_idx" ON "public"."log_drains" ("app_id") WHERE (app_id <> ''::text);
-- Create index "log_drains_id_team_key" to table: "log_drains"
CREATE UNIQUE INDEX "log_drains_id_team_key" ON "public"."log_drains" ("id", "team_id");
-- Create index "log_drains_team_idx" to table: "log_drains"
CREATE INDEX "log_drains_team_idx" ON "public"."log_drains" ("team_id");
