-- Create "site_templates" table
CREATE TABLE "public"."site_templates" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "name" text NOT NULL,
  "description" text NOT NULL DEFAULT '',
  "driver" text NOT NULL,
  "source_yaml" text NOT NULL,
  "created_by" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "site_templates_team_id_name_key" UNIQUE ("team_id", "name"),
  CONSTRAINT "site_templates_name_check" CHECK ((length(name) >= 1) AND (length(name) <= 80))
);
-- Create index "site_templates_team_idx" to table: "site_templates"
CREATE INDEX "site_templates_team_idx" ON "public"."site_templates" ("team_id");
