-- Create "github_deliveries" table
CREATE TABLE "public"."github_deliveries" (
  "delivery_id" text NOT NULL,
  "event" text NOT NULL,
  "received_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("delivery_id")
);
-- Create index "github_deliveries_received_idx" to table: "github_deliveries"
CREATE INDEX "github_deliveries_received_idx" ON "public"."github_deliveries" ("received_at");
-- Create "github_installations" table
CREATE TABLE "public"."github_installations" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "installation_id" bigint NOT NULL,
  "account_login" text NOT NULL,
  "account_type" text NOT NULL,
  "repository_selection" text NOT NULL,
  "installed_by" text NOT NULL,
  "suspended_at" timestamptz NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "github_installations_team_fk" FOREIGN KEY ("team_id") REFERENCES "public"."teams" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "github_installations_account_type_check" CHECK (account_type = ANY (ARRAY['User'::text, 'Organization'::text, 'Enterprise'::text])),
  CONSTRAINT "github_installations_installation_check" CHECK (installation_id > 0),
  CONSTRAINT "github_installations_selection_check" CHECK (repository_selection = ANY (ARRAY['all'::text, 'selected'::text]))
);
-- Create index "github_installations_installation_key" to table: "github_installations"
CREATE UNIQUE INDEX "github_installations_installation_key" ON "public"."github_installations" ("installation_id");
-- Create index "github_installations_team_idx" to table: "github_installations"
CREATE INDEX "github_installations_team_idx" ON "public"."github_installations" ("team_id");
-- Create "oauth_states" table
CREATE TABLE "public"."oauth_states" (
  "id" text NOT NULL,
  "browser_hash" text NOT NULL,
  "purpose" text NOT NULL,
  "provider" text NOT NULL DEFAULT '',
  "code_verifier" text NOT NULL DEFAULT '',
  "nonce" text NOT NULL DEFAULT '',
  "next" text NOT NULL DEFAULT '',
  "locale" text NOT NULL DEFAULT 'en',
  "invitation_hash" text NOT NULL DEFAULT '',
  "user_id" text NULL,
  "team_id" text NULL,
  "expires_at" timestamptz NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "oauth_states_user_id_fkey" FOREIGN KEY ("user_id") REFERENCES "public"."users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "oauth_states_purpose_check" CHECK (purpose = ANY (ARRAY['login'::text, 'link'::text, 'github_install'::text]))
);
-- Create index "oauth_states_expires_idx" to table: "oauth_states"
CREATE INDEX "oauth_states_expires_idx" ON "public"."oauth_states" ("expires_at");
-- Create "user_identities" table
CREATE TABLE "public"."user_identities" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "user_id" text NOT NULL,
  "provider" text NOT NULL,
  "subject" text NOT NULL,
  "email_at_link" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "last_used_at" timestamptz NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "user_identities_user_id_fkey" FOREIGN KEY ("user_id") REFERENCES "public"."users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "user_identities_provider_check" CHECK (provider = ANY (ARRAY['google'::text, 'github'::text])),
  CONSTRAINT "user_identities_subject_check" CHECK ((length(subject) >= 1) AND (length(subject) <= 255))
);
-- Create index "user_identities_provider_subject_key" to table: "user_identities"
CREATE UNIQUE INDEX "user_identities_provider_subject_key" ON "public"."user_identities" ("provider", "subject");
-- Create index "user_identities_user_idx" to table: "user_identities"
CREATE INDEX "user_identities_user_idx" ON "public"."user_identities" ("user_id");
