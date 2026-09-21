-- Create "app_realtime" table
CREATE TABLE "public"."app_realtime" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "app_id" text NOT NULL,
  "enabled" boolean NOT NULL DEFAULT true,
  "kind" text NOT NULL,
  "process" text NOT NULL,
  "port" bigint NOT NULL,
  "path" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "app_realtime_app_fk" FOREIGN KEY ("app_id", "team_id") REFERENCES "public"."apps" ("id", "team_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "app_realtime_kind_check" CHECK (kind = ANY (ARRAY['reverb'::text, 'socketio'::text, 'ws'::text, 'go'::text, 'custom'::text])),
  CONSTRAINT "app_realtime_path_check" CHECK (((length(path) >= 1) AND (length(path) <= 100)) AND (path ~ '^/[A-Za-z0-9._/-]*$'::text) AND ((path <> '/'::text) OR (kind = 'custom'::text))),
  CONSTRAINT "app_realtime_port_check" CHECK ((port >= 1) AND (port <= 65535)),
  CONSTRAINT "app_realtime_process_check" CHECK (process ~ '^[a-z][a-z0-9_-]{0,30}$'::text)
);
-- Create index "app_realtime_app_key" to table: "app_realtime"
CREATE UNIQUE INDEX "app_realtime_app_key" ON "public"."app_realtime" ("app_id");
-- Create index "app_realtime_id_team_key" to table: "app_realtime"
CREATE UNIQUE INDEX "app_realtime_id_team_key" ON "public"."app_realtime" ("id", "team_id");
-- Create index "app_realtime_team_idx" to table: "app_realtime"
CREATE INDEX "app_realtime_team_idx" ON "public"."app_realtime" ("team_id");
