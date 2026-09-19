-- Modify "users" table
ALTER TABLE "public"."users" ADD COLUMN "email_unverified_at" timestamptz NULL;
-- Create "account_codes" table
CREATE TABLE "public"."account_codes" (
  "email" text NOT NULL,
  "purpose" text NOT NULL,
  "code_hash" text NOT NULL,
  "attempts" integer NOT NULL DEFAULT 0,
  "expires_at" timestamptz NOT NULL,
  "sent_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("email", "purpose"),
  CONSTRAINT "account_codes_purpose_check" CHECK (purpose = ANY (ARRAY['verify'::text, 'reset'::text, 'login'::text]))
);
-- Create "account_challenges" table
CREATE TABLE "public"."account_challenges" (
  "id" text NOT NULL,
  "user_id" text NOT NULL,
  "attempts" integer NOT NULL DEFAULT 0,
  "expires_at" timestamptz NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "account_challenges_user_id_fkey" FOREIGN KEY ("user_id") REFERENCES "public"."users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Create index "account_challenges_expires_idx" to table: "account_challenges"
CREATE INDEX "account_challenges_expires_idx" ON "public"."account_challenges" ("expires_at");
-- Create "account_recovery_codes" table
CREATE TABLE "public"."account_recovery_codes" (
  "user_id" text NOT NULL,
  "code_hash" text NOT NULL,
  "used_at" timestamptz NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("user_id", "code_hash"),
  CONSTRAINT "account_recovery_codes_user_id_fkey" FOREIGN KEY ("user_id") REFERENCES "public"."users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Create "account_totp" table
CREATE TABLE "public"."account_totp" (
  "user_id" text NOT NULL,
  "secret_sealed" text NOT NULL,
  "enabled" boolean NOT NULL DEFAULT false,
  "last_step" bigint NOT NULL DEFAULT 0,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "enabled_at" timestamptz NULL,
  PRIMARY KEY ("user_id"),
  CONSTRAINT "account_totp_user_id_fkey" FOREIGN KEY ("user_id") REFERENCES "public"."users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
