-- Create "circlexo_user_links" table
CREATE TABLE "public"."circlexo_user_links" (
  "subject" text NOT NULL,
  "user_id" text NOT NULL,
  "email" text NOT NULL DEFAULT '',
  "linked_via" text NOT NULL DEFAULT 'email',
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("subject"),
  CONSTRAINT "circlexo_user_links_user_id_key" UNIQUE ("user_id"),
  CONSTRAINT "circlexo_user_links_user_id_fkey" FOREIGN KEY ("user_id") REFERENCES "public"."users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "circlexo_user_links_linked_via_check" CHECK (linked_via = ANY (ARRAY['email'::text, 'created'::text]))
);
-- Create "circlexo_org_links" table
CREATE TABLE "public"."circlexo_org_links" (
  "org_id" text NOT NULL,
  "team_id" text NOT NULL,
  "team_slug" text NOT NULL DEFAULT '',
  "org_slug" text NOT NULL DEFAULT '',
  "locale" text NOT NULL DEFAULT 'en',
  "status" text NOT NULL DEFAULT 'active',
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("org_id"),
  CONSTRAINT "circlexo_org_links_team_id_key" UNIQUE ("team_id"),
  CONSTRAINT "circlexo_org_links_team_id_fkey" FOREIGN KEY ("team_id") REFERENCES "public"."teams" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "circlexo_org_links_locale_check" CHECK (locale = ANY (ARRAY['en'::text, 'ar'::text])),
  CONSTRAINT "circlexo_org_links_status_check" CHECK (status = ANY (ARRAY['active'::text, 'inactive'::text]))
);
-- Create "circlexo_sessions" table
CREATE TABLE "public"."circlexo_sessions" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "sid" text NOT NULL,
  "user_id" text NOT NULL,
  "issued_at" timestamptz NOT NULL,
  "revoked_at" timestamptz NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "circlexo_sessions_user_id_fkey" FOREIGN KEY ("user_id") REFERENCES "public"."users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Create index "circlexo_sessions_sid" to table: "circlexo_sessions"
CREATE INDEX "circlexo_sessions_sid" ON "public"."circlexo_sessions" ("sid");
-- Create index "circlexo_sessions_user" to table: "circlexo_sessions"
CREATE INDEX "circlexo_sessions_user" ON "public"."circlexo_sessions" ("user_id", "issued_at") WHERE (revoked_at IS NOT NULL);
-- Create "circlexo_webhook_events" table
CREATE TABLE "public"."circlexo_webhook_events" (
  "webhook_id" text NOT NULL,
  "received_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("webhook_id")
);
