-- Create "app_scans" table
CREATE TABLE "public"."app_scans" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "app_id" text NOT NULL,
  "server_id" text NOT NULL,
  "release_id" text NULL,
  "image" text NOT NULL,
  "status" text NOT NULL DEFAULT 'running',
  "trivy_version" text NOT NULL DEFAULT '',
  "critical_count" bigint NOT NULL DEFAULT 0,
  "high_count" bigint NOT NULL DEFAULT 0,
  "medium_count" bigint NOT NULL DEFAULT 0,
  "low_count" bigint NOT NULL DEFAULT 0,
  "unknown_count" bigint NOT NULL DEFAULT 0,
  "findings" jsonb NOT NULL DEFAULT '[]',
  "truncated" boolean NOT NULL DEFAULT false,
  "error" text NOT NULL DEFAULT '',
  "triggered_by" text NOT NULL DEFAULT 'member',
  "created_by" text NULL,
  "started_at" timestamptz NULL,
  "finished_at" timestamptz NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "app_scans_findings_check" CHECK (jsonb_typeof(findings) = 'array'::text),
  CONSTRAINT "app_scans_status_check" CHECK (status = ANY (ARRAY['running'::text, 'ok'::text, 'failed'::text])),
  CONSTRAINT "app_scans_triggered_by_check" CHECK (triggered_by = ANY (ARRAY['member'::text, 'mcp'::text, 'deploy'::text]))
);
-- Create index "app_scans_app_idx" to table: "app_scans"
CREATE INDEX "app_scans_app_idx" ON "public"."app_scans" ("app_id", "created_at" DESC);
-- Create index "app_scans_team_idx" to table: "app_scans"
CREATE INDEX "app_scans_team_idx" ON "public"."app_scans" ("team_id", "created_at" DESC);
