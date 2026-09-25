-- Create "data_service_metrics" table
CREATE TABLE "public"."data_service_metrics" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "service_id" text NOT NULL,
  "team_id" text NOT NULL,
  "at" timestamptz NOT NULL,
  "step" bigint NOT NULL,
  "connections" bigint NOT NULL,
  "max_connections" bigint NOT NULL,
  "cache_hit_ratio" double precision NOT NULL,
  "size_bytes" bigint NOT NULL,
  "slow_query_count" bigint NOT NULL,
  "tx_counter" bigint NOT NULL DEFAULT 0,
  "rate_per_sec" double precision NOT NULL DEFAULT 0,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "data_service_metrics_service_fk" FOREIGN KEY ("service_id", "team_id") REFERENCES "public"."data_services" ("id", "team_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "data_service_metrics_step_check" CHECK (step = ANY (ARRAY[(60)::bigint, (900)::bigint]))
);
-- Create index "data_service_metrics_bucket_key" to table: "data_service_metrics"
CREATE UNIQUE INDEX "data_service_metrics_bucket_key" ON "public"."data_service_metrics" ("service_id", "step", "at");
-- Create index "data_service_metrics_step_at_idx" to table: "data_service_metrics"
CREATE INDEX "data_service_metrics_step_at_idx" ON "public"."data_service_metrics" ("step", "at");
-- Create index "data_service_metrics_team_at_idx" to table: "data_service_metrics"
CREATE INDEX "data_service_metrics_team_at_idx" ON "public"."data_service_metrics" ("team_id", "at");
