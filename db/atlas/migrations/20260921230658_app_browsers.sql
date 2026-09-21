-- Create "app_browsers" table
CREATE TABLE "public"."app_browsers" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "app_id" text NOT NULL,
  "name" text NOT NULL,
  "image" text NOT NULL,
  "memory_mb" bigint NOT NULL DEFAULT 1024,
  "cpus" double precision NOT NULL DEFAULT 1,
  "max_sessions" bigint NOT NULL DEFAULT 5,
  "idle_timeout_seconds" bigint NOT NULL DEFAULT 300,
  "env_prefix" text NOT NULL DEFAULT 'BROWSER',
  "token_sealed" text NOT NULL,
  "host_port" bigint NULL,
  "status" text NOT NULL DEFAULT 'creating',
  "error" text NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "app_browsers_app_fk" FOREIGN KEY ("app_id", "team_id") REFERENCES "public"."apps" ("id", "team_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "app_browsers_cpus_check" CHECK ((cpus >= (0.25)::double precision) AND (cpus <= (4)::double precision)),
  CONSTRAINT "app_browsers_env_prefix_check" CHECK (env_prefix ~ '^[A-Z][A-Z0-9_]{0,20}$'::text),
  CONSTRAINT "app_browsers_host_port_check" CHECK ((host_port IS NULL) OR ((host_port >= 1024) AND (host_port <= 65535))),
  CONSTRAINT "app_browsers_idle_timeout_check" CHECK ((idle_timeout_seconds >= 30) AND (idle_timeout_seconds <= 3600)),
  CONSTRAINT "app_browsers_max_sessions_check" CHECK ((max_sessions >= 1) AND (max_sessions <= 20)),
  CONSTRAINT "app_browsers_memory_check" CHECK ((memory_mb >= 256) AND (memory_mb <= 4096)),
  CONSTRAINT "app_browsers_name_check" CHECK (name ~ '^[a-z][a-z0-9-]{0,30}$'::text),
  CONSTRAINT "app_browsers_sealed_check" CHECK (token_sealed ~~ 'csv1:%'::text),
  CONSTRAINT "app_browsers_status_check" CHECK (status = ANY (ARRAY['creating'::text, 'ready'::text, 'failed'::text, 'removing'::text]))
);
-- Create index "app_browsers_app_idx" to table: "app_browsers"
CREATE INDEX "app_browsers_app_idx" ON "public"."app_browsers" ("app_id");
-- Create index "app_browsers_app_name_key" to table: "app_browsers"
CREATE UNIQUE INDEX "app_browsers_app_name_key" ON "public"."app_browsers" ("app_id", "name");
-- Create index "app_browsers_app_prefix_key" to table: "app_browsers"
CREATE UNIQUE INDEX "app_browsers_app_prefix_key" ON "public"."app_browsers" ("app_id", "env_prefix");
-- Create index "app_browsers_id_team_key" to table: "app_browsers"
CREATE UNIQUE INDEX "app_browsers_id_team_key" ON "public"."app_browsers" ("id", "team_id");
-- Create index "app_browsers_team_idx" to table: "app_browsers"
CREATE INDEX "app_browsers_team_idx" ON "public"."app_browsers" ("team_id");
