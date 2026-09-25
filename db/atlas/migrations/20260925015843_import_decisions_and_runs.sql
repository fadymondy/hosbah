-- Modify "import_ignores" table
ALTER TABLE "public"."import_ignores" ADD CONSTRAINT "import_ignores_decision_check" CHECK (decision = ANY (ARRAY['ignore'::text, 'app'::text, 'database'::text, 'shared_service'::text])), ADD COLUMN "decision" text NOT NULL DEFAULT 'ignore';
-- Create "import_items" table
CREATE TABLE "public"."import_items" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "server_id" text NOT NULL,
  "run_id" text NOT NULL,
  "origin_kind" text NOT NULL,
  "origin_key" text NOT NULL,
  "name" text NOT NULL,
  "detected_kind" text NULL,
  "target" text NOT NULL,
  "outcome" text NOT NULL,
  "reason" text NULL,
  "error" text NULL,
  "record_id" text NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "import_items_server_fk" FOREIGN KEY ("server_id", "team_id") REFERENCES "public"."servers" ("id", "team_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "import_items_outcome_check" CHECK (outcome = ANY (ARRAY['imported_app'::text, 'imported_data_service'::text, 'imported_shared_service'::text, 'linked_guest'::text, 'updated'::text, 'missing'::text, 'removed'::text, 'skipped'::text, 'ignored'::text, 'error'::text])),
  CONSTRAINT "import_items_target_check" CHECK (target = ANY (ARRAY['app'::text, 'data_service'::text]))
);
-- Create index "import_items_run_idx" to table: "import_items"
CREATE INDEX "import_items_run_idx" ON "public"."import_items" ("run_id");
-- Create index "import_items_server_key" to table: "import_items"
CREATE UNIQUE INDEX "import_items_server_key" ON "public"."import_items" ("server_id", "origin_key");
-- Create index "import_items_team_idx" to table: "import_items"
CREATE INDEX "import_items_team_idx" ON "public"."import_items" ("team_id");
-- Create "import_runs" table
CREATE TABLE "public"."import_runs" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "server_id" text NOT NULL,
  "status" text NOT NULL DEFAULT 'ok',
  "error" text NULL,
  "discovered" integer NOT NULL DEFAULT 0,
  "created" integer NOT NULL DEFAULT 0,
  "updated" integer NOT NULL DEFAULT 0,
  "missing" integer NOT NULL DEFAULT 0,
  "removed" integer NOT NULL DEFAULT 0,
  "ignored" integer NOT NULL DEFAULT 0,
  "started_at" timestamptz NOT NULL DEFAULT now(),
  "finished_at" timestamptz NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "import_runs_server_fk" FOREIGN KEY ("server_id", "team_id") REFERENCES "public"."servers" ("id", "team_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "import_runs_status_check" CHECK (status = ANY (ARRAY['ok'::text, 'error'::text]))
);
-- Create index "import_runs_server_key" to table: "import_runs"
CREATE UNIQUE INDEX "import_runs_server_key" ON "public"."import_runs" ("server_id");
-- Create index "import_runs_team_idx" to table: "import_runs"
CREATE INDEX "import_runs_team_idx" ON "public"."import_runs" ("team_id");
