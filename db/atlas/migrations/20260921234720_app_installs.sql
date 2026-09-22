-- Create "app_installs" table
CREATE TABLE "public"."app_installs" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "app_id" text NOT NULL,
  "recipe_id" text NOT NULL,
  "recipe_version" text NOT NULL,
  "name" text NOT NULL,
  "server_id" text NOT NULL,
  "host_port" bigint NULL,
  "proxy_host_id" text NULL,
  "secrets" text NOT NULL,
  "env_injected" text[] NOT NULL DEFAULT '{}',
  "status" text NOT NULL DEFAULT 'creating',
  "error" text NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "app_installs_app_fk" FOREIGN KEY ("app_id", "team_id") REFERENCES "public"."apps" ("id", "team_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "app_installs_proxy_fk" FOREIGN KEY ("proxy_host_id") REFERENCES "public"."proxy_hosts" ("id") ON UPDATE NO ACTION ON DELETE SET NULL,
  CONSTRAINT "app_installs_server_fk" FOREIGN KEY ("server_id", "team_id") REFERENCES "public"."servers" ("id", "team_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "app_installs_host_port_check" CHECK ((host_port IS NULL) OR ((host_port >= 1) AND (host_port <= 65535))),
  CONSTRAINT "app_installs_name_check" CHECK (name ~ '^[a-z][a-z0-9-]{0,30}$'::text),
  CONSTRAINT "app_installs_recipe_check" CHECK (recipe_id ~ '^[a-z][a-z0-9-]{1,40}$'::text),
  CONSTRAINT "app_installs_sealed_check" CHECK (secrets ~~ 'csv1:%'::text),
  CONSTRAINT "app_installs_status_check" CHECK (status = ANY (ARRAY['creating'::text, 'ready'::text, 'failed'::text, 'removing'::text]))
);
-- Create index "app_installs_app_idx" to table: "app_installs"
CREATE INDEX "app_installs_app_idx" ON "public"."app_installs" ("app_id");
-- Create index "app_installs_app_name_key" to table: "app_installs"
CREATE UNIQUE INDEX "app_installs_app_name_key" ON "public"."app_installs" ("app_id", "name");
-- Create index "app_installs_id_team_key" to table: "app_installs"
CREATE UNIQUE INDEX "app_installs_id_team_key" ON "public"."app_installs" ("id", "team_id");
-- Create index "app_installs_server_idx" to table: "app_installs"
CREATE INDEX "app_installs_server_idx" ON "public"."app_installs" ("server_id");
-- Create index "app_installs_team_idx" to table: "app_installs"
CREATE INDEX "app_installs_team_idx" ON "public"."app_installs" ("team_id");
