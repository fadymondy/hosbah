-- Create "mail_relay_credentials" table
CREATE TABLE "public"."mail_relay_credentials" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "mail_domain_id" text NOT NULL,
  "app_id" text NOT NULL,
  "local_part" text NOT NULL,
  "password_sealed" text NOT NULL,
  "attached" boolean NOT NULL DEFAULT false,
  "status" text NOT NULL DEFAULT 'new',
  "error" text NULL,
  "created_by" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "mail_relay_credentials_local_part_check" CHECK (local_part ~ '^[a-z0-9][a-z0-9._-]{0,62}$'::text),
  CONSTRAINT "mail_relay_credentials_sealed_check" CHECK (password_sealed ~~ 'csv1:%'::text),
  CONSTRAINT "mail_relay_credentials_status_check" CHECK (status = ANY (ARRAY['new'::text, 'creating'::text, 'ready'::text, 'failed'::text, 'removing'::text]))
);
-- Create index "mail_relay_credentials_app_idx" to table: "mail_relay_credentials"
CREATE INDEX "mail_relay_credentials_app_idx" ON "public"."mail_relay_credentials" ("app_id");
-- Create index "mail_relay_credentials_domain_idx" to table: "mail_relay_credentials"
CREATE INDEX "mail_relay_credentials_domain_idx" ON "public"."mail_relay_credentials" ("mail_domain_id");
-- Create index "mail_relay_credentials_domain_local_key" to table: "mail_relay_credentials"
CREATE UNIQUE INDEX "mail_relay_credentials_domain_local_key" ON "public"."mail_relay_credentials" ("mail_domain_id", "local_part");
-- Create index "mail_relay_credentials_id_team_key" to table: "mail_relay_credentials"
CREATE UNIQUE INDEX "mail_relay_credentials_id_team_key" ON "public"."mail_relay_credentials" ("id", "team_id");
