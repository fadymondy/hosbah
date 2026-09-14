-- Create "activity_log" table
CREATE TABLE "public"."activity_log" (
  "id" text NOT NULL,
  "action" text NOT NULL,
  "subject_type" text NOT NULL DEFAULT '',
  "subject_id" text NOT NULL DEFAULT '',
  "causer_id" text NOT NULL DEFAULT '',
  "changes" text NOT NULL DEFAULT '{}',
  "ip" text NOT NULL DEFAULT '',
  "user_agent" text NOT NULL DEFAULT '',
  "created_at" text NOT NULL,
  "team_id" text NULL,
  PRIMARY KEY ("id")
);
-- Create index "activity_log_team_idx" to table: "activity_log"
CREATE INDEX "activity_log_team_idx" ON "public"."activity_log" ("team_id", "created_at");
-- Create index "idx_activity_action" to table: "activity_log"
CREATE INDEX "idx_activity_action" ON "public"."activity_log" ("action", "created_at");
-- Create index "idx_activity_causer" to table: "activity_log"
CREATE INDEX "idx_activity_causer" ON "public"."activity_log" ("causer_id", "created_at");
-- Create index "idx_activity_subject" to table: "activity_log"
CREATE INDEX "idx_activity_subject" ON "public"."activity_log" ("subject_type", "subject_id", "created_at");
-- Create "auth_totp" table
CREATE TABLE "public"."auth_totp" (
  "subject" text NOT NULL,
  "secret" text NOT NULL,
  "enabled" text NOT NULL DEFAULT '',
  PRIMARY KEY ("subject")
);
-- Create "users" table
CREATE TABLE "public"."users" (
  "id" text NOT NULL,
  "email" text NOT NULL,
  "password_hash" text NOT NULL,
  "roles" text NOT NULL DEFAULT '',
  "permissions" text NOT NULL DEFAULT '',
  "created_at" text NOT NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "users_email_key" UNIQUE ("email")
);
