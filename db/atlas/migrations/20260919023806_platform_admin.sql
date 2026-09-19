-- Modify "teams" table
ALTER TABLE "public"."teams" ADD COLUMN "suspended_at" timestamptz NULL;
-- Modify "users" table
ALTER TABLE "public"."users" ADD COLUMN "disabled_at" timestamptz NULL, ADD COLUMN "sessions_revoked_at" timestamptz NULL;
-- Create "platform_audit" table
CREATE TABLE "public"."platform_audit" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "actor_id" text NOT NULL,
  "action" text NOT NULL,
  "target_type" text NOT NULL,
  "target_id" text NOT NULL,
  "details" jsonb NOT NULL DEFAULT '{}',
  "ip" text NOT NULL DEFAULT '',
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id")
);
-- Create index "platform_audit_created_idx" to table: "platform_audit"
CREATE INDEX "platform_audit_created_idx" ON "public"."platform_audit" ("created_at");
-- Create "platform_settings" table
CREATE TABLE "public"."platform_settings" (
  "id" boolean NOT NULL DEFAULT true,
  "signup_mode" text NOT NULL DEFAULT 'open',
  "announcement" text NOT NULL DEFAULT '',
  "announcement_level" text NOT NULL DEFAULT 'info',
  "support_email" text NOT NULL DEFAULT '',
  "max_teams_per_user" bigint NOT NULL DEFAULT 0,
  "max_servers_per_team" bigint NOT NULL DEFAULT 0,
  "updated_by" text NULL,
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "platform_settings_announcement_length" CHECK (char_length(announcement) <= 500),
  CONSTRAINT "platform_settings_announcement_level_check" CHECK (announcement_level = ANY (ARRAY['info'::text, 'warning'::text])),
  CONSTRAINT "platform_settings_limits_check" CHECK ((max_teams_per_user >= 0) AND (max_servers_per_team >= 0)),
  CONSTRAINT "platform_settings_signup_mode_check" CHECK (signup_mode = ANY (ARRAY['open'::text, 'invite_only'::text, 'closed'::text])),
  CONSTRAINT "platform_settings_singleton" CHECK (id)
);
