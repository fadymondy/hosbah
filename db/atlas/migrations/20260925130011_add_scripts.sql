-- Create "script_run_servers" table
CREATE TABLE "public"."script_run_servers" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "script_run_id" text NOT NULL,
  "server_id" text NOT NULL,
  "operation_id" text NULL,
  "status" text NOT NULL DEFAULT 'pending',
  "exit_code" bigint NULL,
  "started_at" timestamptz NULL,
  "finished_at" timestamptz NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "script_run_servers_status_check" CHECK (status = ANY (ARRAY['pending'::text, 'sent'::text, 'running'::text, 'ok'::text, 'failed'::text, 'timeout'::text]))
);
-- Create index "script_run_servers_operation_key" to table: "script_run_servers"
CREATE UNIQUE INDEX "script_run_servers_operation_key" ON "public"."script_run_servers" ("operation_id") WHERE (operation_id IS NOT NULL);
-- Create index "script_run_servers_run_idx" to table: "script_run_servers"
CREATE INDEX "script_run_servers_run_idx" ON "public"."script_run_servers" ("team_id", "script_run_id");
-- Create index "script_run_servers_server_idx" to table: "script_run_servers"
CREATE INDEX "script_run_servers_server_idx" ON "public"."script_run_servers" ("team_id", "server_id", "created_at" DESC);
-- Create "script_runs" table
CREATE TABLE "public"."script_runs" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "script_id" text NULL,
  "builtin_name" text NULL,
  "params" jsonb NOT NULL DEFAULT '{}',
  "status" text NOT NULL DEFAULT 'running',
  "created_by" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "script_runs_one_source_check" CHECK ((script_id IS NOT NULL) <> (builtin_name IS NOT NULL)),
  CONSTRAINT "script_runs_status_check" CHECK (status = ANY (ARRAY['running'::text, 'ok'::text, 'failed'::text]))
);
-- Create index "script_runs_script_idx" to table: "script_runs"
CREATE INDEX "script_runs_script_idx" ON "public"."script_runs" ("team_id", "script_id") WHERE (script_id IS NOT NULL);
-- Create index "script_runs_team_idx" to table: "script_runs"
CREATE INDEX "script_runs_team_idx" ON "public"."script_runs" ("team_id", "created_at" DESC);
-- Create "scripts" table
CREATE TABLE "public"."scripts" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "name" text NOT NULL,
  "description" text NOT NULL DEFAULT '',
  "body" text NOT NULL,
  "default_args" jsonb NOT NULL DEFAULT '{}',
  "env_names" jsonb NOT NULL DEFAULT '[]',
  "run_as" text NOT NULL DEFAULT 'root',
  "timeout_seconds" bigint NOT NULL DEFAULT 600,
  "created_by" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "scripts_team_id_name_key" UNIQUE ("team_id", "name"),
  CONSTRAINT "scripts_name_check" CHECK ((length(name) >= 1) AND (length(name) <= 128)),
  CONSTRAINT "scripts_run_as_check" CHECK (run_as = ANY (ARRAY['root'::text, 'app'::text])),
  CONSTRAINT "scripts_timeout_check" CHECK ((timeout_seconds >= 1) AND (timeout_seconds <= 3600))
);
-- Create index "scripts_team_idx" to table: "scripts"
CREATE INDEX "scripts_team_idx" ON "public"."scripts" ("team_id");
