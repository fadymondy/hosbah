-- Create "app_rum_settings" table
CREATE TABLE "public"."app_rum_settings" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "app_id" text NOT NULL,
  "team_id" text NOT NULL,
  "enabled" boolean NOT NULL DEFAULT false,
  "key_hash" text NULL,
  "created_by" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "app_rum_settings_app_id_key" UNIQUE ("app_id")
);
-- Create index "app_rum_settings_key_hash_key" to table: "app_rum_settings"
CREATE UNIQUE INDEX "app_rum_settings_key_hash_key" ON "public"."app_rum_settings" ("key_hash") WHERE (key_hash IS NOT NULL);
-- Create index "app_rum_settings_team_idx" to table: "app_rum_settings"
CREATE INDEX "app_rum_settings_team_idx" ON "public"."app_rum_settings" ("team_id");
-- Create "rum_samples" table
CREATE TABLE "public"."rum_samples" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "app_id" text NOT NULL,
  "team_id" text NOT NULL,
  "path" text NOT NULL,
  "device" text NOT NULL,
  "effective_type" text NULL,
  "lcp_ms" real NULL,
  "cls" real NULL,
  "inp_ms" real NULL,
  "fcp_ms" real NULL,
  "ttfb_ms" real NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "rum_samples_device_check" CHECK (device = ANY (ARRAY['mobile'::text, 'desktop'::text])),
  CONSTRAINT "rum_samples_path_check" CHECK ((length(path) >= 1) AND (length(path) <= 300))
);
-- Create index "rum_samples_app_created_idx" to table: "rum_samples"
CREATE INDEX "rum_samples_app_created_idx" ON "public"."rum_samples" ("team_id", "app_id", "created_at");
-- Create index "rum_samples_app_path_created_idx" to table: "rum_samples"
CREATE INDEX "rum_samples_app_path_created_idx" ON "public"."rum_samples" ("team_id", "app_id", "path", "created_at");
-- Create index "rum_samples_created_idx" to table: "rum_samples"
CREATE INDEX "rum_samples_created_idx" ON "public"."rum_samples" ("created_at");
