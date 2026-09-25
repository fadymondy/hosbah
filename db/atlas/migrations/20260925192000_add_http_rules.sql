-- Create "app_httprules" table
CREATE TABLE "public"."app_httprules" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "app_id" text NOT NULL,
  "team_id" text NOT NULL,
  "redirects" jsonb NOT NULL DEFAULT '[]',
  "headers" jsonb NOT NULL DEFAULT '[]',
  "basic_auth" jsonb NOT NULL DEFAULT '{}',
  "access" jsonb NOT NULL DEFAULT '{}',
  "status" text NOT NULL DEFAULT 'ok',
  "error" text NULL,
  "created_by" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "app_httprules_app_id_key" UNIQUE ("app_id")
);
-- Create index "app_httprules_team_idx" to table: "app_httprules"
CREATE INDEX "app_httprules_team_idx" ON "public"."app_httprules" ("team_id");
-- Create "proxy_host_httprules" table
CREATE TABLE "public"."proxy_host_httprules" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "proxy_host_id" text NOT NULL,
  "team_id" text NOT NULL,
  "redirects" jsonb NOT NULL DEFAULT '[]',
  "headers" jsonb NOT NULL DEFAULT '[]',
  "basic_auth" jsonb NOT NULL DEFAULT '{}',
  "access" jsonb NOT NULL DEFAULT '{}',
  "status" text NOT NULL DEFAULT 'ok',
  "error" text NULL,
  "created_by" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "proxy_host_httprules_proxy_host_id_key" UNIQUE ("proxy_host_id")
);
-- Create index "proxy_host_httprules_team_idx" to table: "proxy_host_httprules"
CREATE INDEX "proxy_host_httprules_team_idx" ON "public"."proxy_host_httprules" ("team_id");
