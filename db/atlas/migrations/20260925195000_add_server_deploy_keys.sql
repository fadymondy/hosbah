-- Create "server_deploy_keys" table
CREATE TABLE "public"."server_deploy_keys" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "server_id" text NOT NULL,
  "public_key" text NOT NULL,
  "fingerprint" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "server_deploy_keys_public_key_check" CHECK ((length(public_key) >= 1) AND (length(public_key) <= 8192))
);
-- Create index "server_deploy_keys_server_key" to table: "server_deploy_keys"
CREATE UNIQUE INDEX "server_deploy_keys_server_key" ON "public"."server_deploy_keys" ("server_id");
-- Create index "server_deploy_keys_team_idx" to table: "server_deploy_keys"
CREATE INDEX "server_deploy_keys_team_idx" ON "public"."server_deploy_keys" ("team_id");
