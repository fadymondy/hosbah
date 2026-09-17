-- Create "apps" table
CREATE TABLE "public"."apps" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "server_id" text NOT NULL,
  "name" text NOT NULL,
  "repo_url" text NOT NULL,
  "branch" text NOT NULL,
  "runtime" text NULL,
  "port" bigint NOT NULL DEFAULT 8080,
  "healthcheck_path" text NOT NULL DEFAULT '/',
  "healthcheck_timeout" bigint NOT NULL DEFAULT 60,
  "autodeploy_paused" boolean NOT NULL DEFAULT false,
  "deploy_hook_hash" text NULL,
  "current_release_id" text NULL,
  "status" text NOT NULL DEFAULT 'new',
  "git_installation_id" text NULL,
  "created_by" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "apps_server_fk" FOREIGN KEY ("server_id", "team_id") REFERENCES "public"."servers" ("id", "team_id") ON UPDATE NO ACTION ON DELETE RESTRICT,
  CONSTRAINT "apps_team_fk" FOREIGN KEY ("team_id") REFERENCES "public"."teams" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "apps_healthcheck_path_check" CHECK (healthcheck_path ~~ '/%'::text),
  CONSTRAINT "apps_healthcheck_timeout_check" CHECK ((healthcheck_timeout >= 5) AND (healthcheck_timeout <= 600)),
  CONSTRAINT "apps_name_check" CHECK (name ~ '^[a-z0-9][a-z0-9-]{0,39}$'::text),
  CONSTRAINT "apps_port_check" CHECK ((port >= 1) AND (port <= 65535)),
  CONSTRAINT "apps_status_check" CHECK (status = ANY (ARRAY['new'::text, 'deploying'::text, 'running'::text, 'failed'::text, 'stopped'::text]))
);
-- Create index "apps_deploy_hook_key" to table: "apps"
CREATE UNIQUE INDEX "apps_deploy_hook_key" ON "public"."apps" ("deploy_hook_hash") WHERE (deploy_hook_hash IS NOT NULL);
-- Create index "apps_id_team_key" to table: "apps"
CREATE UNIQUE INDEX "apps_id_team_key" ON "public"."apps" ("id", "team_id");
-- Create index "apps_server_idx" to table: "apps"
CREATE INDEX "apps_server_idx" ON "public"."apps" ("server_id");
-- Create index "apps_team_name_key" to table: "apps"
CREATE UNIQUE INDEX "apps_team_name_key" ON "public"."apps" ("team_id", "name");
-- Create "deployments" table
CREATE TABLE "public"."deployments" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "app_id" text NOT NULL,
  "server_id" text NOT NULL,
  "number" bigint NOT NULL,
  "ref" text NOT NULL,
  "commit_sha" text NULL,
  "commit_message" text NULL,
  "trigger" text NOT NULL,
  "kind" text NOT NULL,
  "status" text NOT NULL,
  "step" text NULL,
  "image" text NULL,
  "runtime" text NULL,
  "error" text NULL,
  "release_id" text NULL,
  "target_release_id" text NULL,
  "cancel_requested" boolean NOT NULL DEFAULT false,
  "created_by" text NOT NULL,
  "started_at" timestamptz NULL,
  "finished_at" timestamptz NULL,
  "last_seq" bigint NOT NULL DEFAULT 0,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "deployments_app_fk" FOREIGN KEY ("app_id", "team_id") REFERENCES "public"."apps" ("id", "team_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "deployments_server_fk" FOREIGN KEY ("server_id", "team_id") REFERENCES "public"."servers" ("id", "team_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "deployments_kind_check" CHECK (kind = ANY (ARRAY['deploy'::text, 'rollback'::text, 'restart'::text])),
  CONSTRAINT "deployments_status_check" CHECK (status = ANY (ARRAY['queued'::text, 'running'::text, 'succeeded'::text, 'failed'::text, 'cancelled'::text])),
  CONSTRAINT "deployments_trigger_check" CHECK (trigger = ANY (ARRAY['manual'::text, 'hook'::text, 'mcp'::text, 'github'::text]))
);
-- Create index "deployments_app_idx" to table: "deployments"
CREATE INDEX "deployments_app_idx" ON "public"."deployments" ("app_id", "created_at");
-- Create index "deployments_app_number_key" to table: "deployments"
CREATE UNIQUE INDEX "deployments_app_number_key" ON "public"."deployments" ("app_id", "number");
-- Create index "deployments_id_team_key" to table: "deployments"
CREATE UNIQUE INDEX "deployments_id_team_key" ON "public"."deployments" ("id", "team_id");
-- Create index "deployments_one_queued" to table: "deployments"
CREATE UNIQUE INDEX "deployments_one_queued" ON "public"."deployments" ("app_id") WHERE (status = 'queued'::text);
-- Create index "deployments_one_running" to table: "deployments"
CREATE UNIQUE INDEX "deployments_one_running" ON "public"."deployments" ("app_id") WHERE (status = 'running'::text);
-- Create index "deployments_queue_idx" to table: "deployments"
CREATE INDEX "deployments_queue_idx" ON "public"."deployments" ("status", "created_at") WHERE (status = ANY (ARRAY['queued'::text, 'running'::text]));
-- Create "deployment_logs" table
CREATE TABLE "public"."deployment_logs" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "server_id" text NOT NULL,
  "app_id" text NOT NULL,
  "deployment_id" text NOT NULL,
  "operation_id" text NULL,
  "op_seq" bigint NULL,
  "seq" bigint NOT NULL,
  "stream" text NOT NULL,
  "line_sealed" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "deployment_logs_deployment_fk" FOREIGN KEY ("deployment_id", "team_id") REFERENCES "public"."deployments" ("id", "team_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "deployment_logs_server_fk" FOREIGN KEY ("server_id", "team_id") REFERENCES "public"."servers" ("id", "team_id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Create index "deployment_logs_deployment_seq_key" to table: "deployment_logs"
CREATE UNIQUE INDEX "deployment_logs_deployment_seq_key" ON "public"."deployment_logs" ("deployment_id", "seq");
-- Create index "deployment_logs_op_seq_key" to table: "deployment_logs"
CREATE UNIQUE INDEX "deployment_logs_op_seq_key" ON "public"."deployment_logs" ("operation_id", "op_seq") WHERE (operation_id IS NOT NULL);
-- Create "domains" table
CREATE TABLE "public"."domains" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "app_id" text NOT NULL,
  "hostname" text NOT NULL,
  "tls" text NOT NULL DEFAULT 'auto',
  "verify_token" text NOT NULL,
  "verified_at" timestamptz NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "domains_app_fk" FOREIGN KEY ("app_id", "team_id") REFERENCES "public"."apps" ("id", "team_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "domains_hostname_check" CHECK ((hostname = lower(hostname)) AND (length(hostname) <= 253) AND (hostname ~ '^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$'::text)),
  CONSTRAINT "domains_tls_check" CHECK (tls = ANY (ARRAY['auto'::text, 'internal'::text, 'off'::text]))
);
-- Create index "domains_app_idx" to table: "domains"
CREATE INDEX "domains_app_idx" ON "public"."domains" ("app_id");
-- Create index "domains_hostname_key" to table: "domains"
CREATE UNIQUE INDEX "domains_hostname_key" ON "public"."domains" ("hostname");
-- Create "env_vars" table
CREATE TABLE "public"."env_vars" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "app_id" text NOT NULL,
  "name" text NOT NULL,
  "value_sealed" text NOT NULL,
  "updated_by" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "env_vars_app_fk" FOREIGN KEY ("app_id", "team_id") REFERENCES "public"."apps" ("id", "team_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "env_vars_name_check" CHECK (name ~ '^[A-Za-z_][A-Za-z0-9_]{0,127}$'::text),
  CONSTRAINT "env_vars_sealed_check" CHECK (value_sealed ~~ 'csv1:%'::text)
);
-- Create index "env_vars_app_name_key" to table: "env_vars"
CREATE UNIQUE INDEX "env_vars_app_name_key" ON "public"."env_vars" ("app_id", "name");
-- Create "notifications" table
CREATE TABLE "public"."notifications" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "user_id" text NULL,
  "kind" text NOT NULL,
  "title" text NOT NULL,
  "body" text NULL,
  "subject_type" text NULL,
  "subject_id" text NULL,
  "read_at" timestamptz NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "notifications_team_fk" FOREIGN KEY ("team_id") REFERENCES "public"."teams" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Create index "notifications_team_idx" to table: "notifications"
CREATE INDEX "notifications_team_idx" ON "public"."notifications" ("team_id", "created_at");
-- Modify "operations" table
ALTER TABLE "public"."operations" ADD COLUMN "deployment_id" text NULL, ADD CONSTRAINT "operations_deployment_fk" FOREIGN KEY ("deployment_id") REFERENCES "public"."deployments" ("id") ON UPDATE NO ACTION ON DELETE SET NULL;
-- Create index "operations_deployment_idx" to table: "operations"
CREATE INDEX "operations_deployment_idx" ON "public"."operations" ("deployment_id") WHERE (deployment_id IS NOT NULL);
-- Create "process_types" table
CREATE TABLE "public"."process_types" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "app_id" text NOT NULL,
  "name" text NOT NULL,
  "command" text NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "process_types_app_fk" FOREIGN KEY ("app_id", "team_id") REFERENCES "public"."apps" ("id", "team_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "process_types_name_check" CHECK (name ~ '^(web|worker:[a-z0-9_-]{1,32})$'::text)
);
-- Create index "process_types_app_name_key" to table: "process_types"
CREATE UNIQUE INDEX "process_types_app_name_key" ON "public"."process_types" ("app_id", "name");
-- Create "releases" table
CREATE TABLE "public"."releases" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "app_id" text NOT NULL,
  "deployment_id" text NOT NULL,
  "version" bigint NOT NULL,
  "image" text NOT NULL,
  "commit_sha" text NULL,
  "port" bigint NOT NULL,
  "host_port" bigint NULL,
  "containers" jsonb NULL,
  "config" jsonb NOT NULL,
  "status" text NOT NULL,
  "activated_at" timestamptz NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "releases_app_fk" FOREIGN KEY ("app_id", "team_id") REFERENCES "public"."apps" ("id", "team_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "releases_deployment_fk" FOREIGN KEY ("deployment_id", "team_id") REFERENCES "public"."deployments" ("id", "team_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "releases_status_check" CHECK (status = ANY (ARRAY['live'::text, 'standby'::text, 'pruned'::text]))
);
-- Create index "releases_app_idx" to table: "releases"
CREATE INDEX "releases_app_idx" ON "public"."releases" ("app_id", "activated_at");
-- Create index "releases_app_version_key" to table: "releases"
CREATE UNIQUE INDEX "releases_app_version_key" ON "public"."releases" ("app_id", "version");
-- Create index "releases_one_live" to table: "releases"
CREATE UNIQUE INDEX "releases_one_live" ON "public"."releases" ("app_id") WHERE (status = 'live'::text);
