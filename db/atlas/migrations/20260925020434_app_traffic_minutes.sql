-- Create "app_traffic_minutes" table
CREATE TABLE "public"."app_traffic_minutes" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "app_id" text NOT NULL,
  "server_id" text NOT NULL,
  "team_id" text NOT NULL,
  "at" timestamptz NOT NULL,
  "step" bigint NOT NULL,
  "requests" bigint NOT NULL DEFAULT 0,
  "status_2xx" bigint NOT NULL DEFAULT 0,
  "status_3xx" bigint NOT NULL DEFAULT 0,
  "status_4xx" bigint NOT NULL DEFAULT 0,
  "status_5xx" bigint NOT NULL DEFAULT 0,
  "bytes_out" bigint NOT NULL DEFAULT 0,
  "duration_p50_ms" double precision NOT NULL DEFAULT 0,
  "duration_p95_ms" double precision NOT NULL DEFAULT 0,
  "unique_visitors" bigint NOT NULL DEFAULT 0,
  "top_paths" jsonb NOT NULL DEFAULT '[]',
  "top_referrers" jsonb NOT NULL DEFAULT '[]',
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "app_traffic_minutes_app_fk" FOREIGN KEY ("app_id", "team_id") REFERENCES "public"."apps" ("id", "team_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "app_traffic_minutes_server_fk" FOREIGN KEY ("server_id", "team_id") REFERENCES "public"."servers" ("id", "team_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "app_traffic_minutes_step_check" CHECK (step = ANY (ARRAY[(60)::bigint, (900)::bigint]))
);
-- Create index "app_traffic_minutes_bucket_key" to table: "app_traffic_minutes"
CREATE UNIQUE INDEX "app_traffic_minutes_bucket_key" ON "public"."app_traffic_minutes" ("app_id", "step", "at");
-- Create index "app_traffic_minutes_step_at_idx" to table: "app_traffic_minutes"
CREATE INDEX "app_traffic_minutes_step_at_idx" ON "public"."app_traffic_minutes" ("step", "at");
-- Create index "app_traffic_minutes_team_at_idx" to table: "app_traffic_minutes"
CREATE INDEX "app_traffic_minutes_team_at_idx" ON "public"."app_traffic_minutes" ("team_id", "at");
