-- Create "app_edge_caches" table
CREATE TABLE "public"."app_edge_caches" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "app_id" text NOT NULL,
  "team_id" text NOT NULL,
  "enabled" boolean NOT NULL DEFAULT false,
  "ttl_seconds" bigint NOT NULL DEFAULT 60,
  "bypass_paths" jsonb NOT NULL DEFAULT '[]',
  "bypass_cookies" text NOT NULL DEFAULT '',
  "status" text NOT NULL DEFAULT 'off',
  "error" text NULL,
  "hits" bigint NOT NULL DEFAULT 0,
  "misses" bigint NOT NULL DEFAULT 0,
  "last_purged_at" timestamptz NULL,
  "created_by" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "app_edge_caches_app_id_key" UNIQUE ("app_id")
);
-- Create index "app_edge_caches_team_idx" to table: "app_edge_caches"
CREATE INDEX "app_edge_caches_team_idx" ON "public"."app_edge_caches" ("team_id");
