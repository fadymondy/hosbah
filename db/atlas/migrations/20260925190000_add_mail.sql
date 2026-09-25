-- Create "mail_aliases" table
CREATE TABLE "public"."mail_aliases" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "mail_domain_id" text NOT NULL,
  "local_part" text NOT NULL,
  "kind" text NOT NULL DEFAULT 'alias',
  "destinations" jsonb NOT NULL,
  "status" text NOT NULL DEFAULT 'new',
  "error" text NULL,
  "created_by" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "mail_aliases_destinations_check" CHECK (jsonb_typeof(destinations) = 'array'::text),
  CONSTRAINT "mail_aliases_kind_check" CHECK (kind = ANY (ARRAY['alias'::text, 'forward'::text])),
  CONSTRAINT "mail_aliases_local_part_check" CHECK (local_part ~ '^[a-z0-9][a-z0-9._-]{0,62}$'::text),
  CONSTRAINT "mail_aliases_status_check" CHECK (status = ANY (ARRAY['new'::text, 'creating'::text, 'ready'::text, 'failed'::text, 'removing'::text]))
);
-- Create index "mail_aliases_domain_idx" to table: "mail_aliases"
CREATE INDEX "mail_aliases_domain_idx" ON "public"."mail_aliases" ("mail_domain_id");
-- Create index "mail_aliases_domain_local_key" to table: "mail_aliases"
CREATE UNIQUE INDEX "mail_aliases_domain_local_key" ON "public"."mail_aliases" ("mail_domain_id", "local_part");
-- Create index "mail_aliases_id_team_key" to table: "mail_aliases"
CREATE UNIQUE INDEX "mail_aliases_id_team_key" ON "public"."mail_aliases" ("id", "team_id");
-- Create "mail_domains" table
CREATE TABLE "public"."mail_domains" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "mail_server_id" text NOT NULL,
  "domain" text NOT NULL,
  "dkim_selector" text NULL,
  "dkim_public_key" text NULL,
  "status" text NOT NULL DEFAULT 'new',
  "error" text NULL,
  "created_by" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "mail_domains_domain_check" CHECK (domain ~ '^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$'::text),
  CONSTRAINT "mail_domains_status_check" CHECK (status = ANY (ARRAY['new'::text, 'creating'::text, 'active'::text, 'failed'::text, 'removing'::text]))
);
-- Create index "mail_domains_id_team_key" to table: "mail_domains"
CREATE UNIQUE INDEX "mail_domains_id_team_key" ON "public"."mail_domains" ("id", "team_id");
-- Create index "mail_domains_server_idx" to table: "mail_domains"
CREATE INDEX "mail_domains_server_idx" ON "public"."mail_domains" ("mail_server_id");
-- Create index "mail_domains_team_domain_key" to table: "mail_domains"
CREATE UNIQUE INDEX "mail_domains_team_domain_key" ON "public"."mail_domains" ("team_id", "domain");
-- Create "mail_servers" table
CREATE TABLE "public"."mail_servers" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "server_id" text NOT NULL,
  "hostname" text NOT NULL,
  "container" text NULL,
  "admin_port" bigint NOT NULL DEFAULT 8080,
  "admin_sealed" text NULL,
  "status" text NOT NULL DEFAULT 'new',
  "error" text NULL,
  "created_by" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "mail_servers_hostname_check" CHECK (hostname ~ '^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$'::text),
  CONSTRAINT "mail_servers_port_check" CHECK ((admin_port >= 1) AND (admin_port <= 65535)),
  CONSTRAINT "mail_servers_sealed_check" CHECK ((admin_sealed IS NULL) OR (admin_sealed ~~ 'csv1:%'::text)),
  CONSTRAINT "mail_servers_status_check" CHECK (status = ANY (ARRAY['new'::text, 'installing'::text, 'running'::text, 'failed'::text, 'removing'::text]))
);
-- Create index "mail_servers_id_team_key" to table: "mail_servers"
CREATE UNIQUE INDEX "mail_servers_id_team_key" ON "public"."mail_servers" ("id", "team_id");
-- Create index "mail_servers_server_idx" to table: "mail_servers"
CREATE INDEX "mail_servers_server_idx" ON "public"."mail_servers" ("server_id");
-- Create index "mail_servers_team_hostname_key" to table: "mail_servers"
CREATE UNIQUE INDEX "mail_servers_team_hostname_key" ON "public"."mail_servers" ("team_id", "hostname");
-- Create index "mail_servers_team_server_key" to table: "mail_servers"
CREATE UNIQUE INDEX "mail_servers_team_server_key" ON "public"."mail_servers" ("team_id", "server_id");
-- Create "mailboxes" table
CREATE TABLE "public"."mailboxes" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "mail_domain_id" text NOT NULL,
  "local_part" text NOT NULL,
  "quota_mb" bigint NOT NULL DEFAULT 1024,
  "password_sealed" text NOT NULL,
  "status" text NOT NULL DEFAULT 'new',
  "error" text NULL,
  "created_by" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "mailboxes_local_part_check" CHECK (local_part ~ '^[a-z0-9][a-z0-9._-]{0,62}$'::text),
  CONSTRAINT "mailboxes_quota_check" CHECK ((quota_mb >= 1) AND (quota_mb <= 1048576)),
  CONSTRAINT "mailboxes_sealed_check" CHECK (password_sealed ~~ 'csv1:%'::text),
  CONSTRAINT "mailboxes_status_check" CHECK (status = ANY (ARRAY['new'::text, 'creating'::text, 'ready'::text, 'failed'::text, 'removing'::text]))
);
-- Create index "mailboxes_domain_idx" to table: "mailboxes"
CREATE INDEX "mailboxes_domain_idx" ON "public"."mailboxes" ("mail_domain_id");
-- Create index "mailboxes_domain_local_key" to table: "mailboxes"
CREATE UNIQUE INDEX "mailboxes_domain_local_key" ON "public"."mailboxes" ("mail_domain_id", "local_part");
-- Create index "mailboxes_id_team_key" to table: "mailboxes"
CREATE UNIQUE INDEX "mailboxes_id_team_key" ON "public"."mailboxes" ("id", "team_id");
