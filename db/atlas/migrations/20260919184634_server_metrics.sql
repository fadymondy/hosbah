-- Create index "deployments_team_feed_idx" to table: "deployments"
CREATE INDEX "deployments_team_feed_idx" ON "public"."deployments" ("team_id", "created_at" DESC, "id" DESC);
-- Create "server_metrics" table
CREATE TABLE "public"."server_metrics" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "server_id" text NOT NULL,
  "team_id" text NOT NULL,
  "at" timestamptz NOT NULL,
  "step" bigint NOT NULL,
  "samples" bigint NOT NULL DEFAULT 1,
  "cpu_percent" double precision NOT NULL,
  "mem_used" bigint NOT NULL,
  "mem_total" bigint NOT NULL,
  "disk_used" bigint NOT NULL,
  "disk_total" bigint NOT NULL,
  "load1" double precision NOT NULL,
  "net_rx" double precision NOT NULL DEFAULT 0,
  "net_tx" double precision NOT NULL DEFAULT 0,
  "gpu" jsonb NOT NULL DEFAULT '[]',
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "server_metrics_server_fk" FOREIGN KEY ("server_id", "team_id") REFERENCES "public"."servers" ("id", "team_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "server_metrics_step_check" CHECK (step = ANY (ARRAY[(60)::bigint, (900)::bigint]))
);
-- Create index "server_metrics_bucket_key" to table: "server_metrics"
CREATE UNIQUE INDEX "server_metrics_bucket_key" ON "public"."server_metrics" ("server_id", "step", "at");
-- Create index "server_metrics_step_at_idx" to table: "server_metrics"
CREATE INDEX "server_metrics_step_at_idx" ON "public"."server_metrics" ("step", "at");
-- Create index "server_metrics_team_at_idx" to table: "server_metrics"
CREATE INDEX "server_metrics_team_at_idx" ON "public"."server_metrics" ("team_id", "at");
