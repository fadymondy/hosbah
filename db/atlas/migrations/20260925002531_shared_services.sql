-- Create "app_service_links" table
CREATE TABLE "public"."app_service_links" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "app_id" text NOT NULL,
  "service_kind" text NOT NULL,
  "service_id" text NOT NULL,
  "variable" text NOT NULL,
  "source" text NOT NULL DEFAULT 'auto',
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "app_service_links_service_kind_check" CHECK (service_kind = ANY (ARRAY['shared'::text, 'data'::text])),
  CONSTRAINT "app_service_links_source_check" CHECK (source = ANY (ARRAY['auto'::text, 'manual'::text]))
);
-- Create index "app_service_links_app_idx" to table: "app_service_links"
CREATE INDEX "app_service_links_app_idx" ON "public"."app_service_links" ("app_id");
-- Create index "app_service_links_app_variable_key" to table: "app_service_links"
CREATE UNIQUE INDEX "app_service_links_app_variable_key" ON "public"."app_service_links" ("app_id", "variable");
-- Create index "app_service_links_team_idx" to table: "app_service_links"
CREATE INDEX "app_service_links_team_idx" ON "public"."app_service_links" ("team_id");
-- Create "shared_services" table
CREATE TABLE "public"."shared_services" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "server_id" text NOT NULL,
  "guest_vmid" text NOT NULL,
  "guest_type" text NOT NULL,
  "kind" text NOT NULL,
  "name" text NOT NULL,
  "host" text NULL,
  "port" bigint NULL,
  "hidden" boolean NOT NULL DEFAULT false,
  "name_manual" boolean NOT NULL DEFAULT false,
  "kind_manual" boolean NOT NULL DEFAULT false,
  "last_seen_at" timestamptz NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "shared_services_guest_type_check" CHECK (guest_type = ANY (ARRAY['lxc'::text, 'qemu'::text])),
  CONSTRAINT "shared_services_kind_check" CHECK (kind = ANY (ARRAY['postgres'::text, 'mysql'::text, 'redis'::text, 'nats'::text, 'minio'::text, 'searxng'::text, 'firecrawl'::text, 'browser'::text, 'meilisearch'::text, 'rabbitmq'::text, 'elasticsearch'::text, 'opensearch'::text, 'clickhouse'::text, 'mongodb'::text, 'other'::text])),
  CONSTRAINT "shared_services_port_check" CHECK ((port IS NULL) OR ((port >= 1) AND (port <= 65535)))
);
-- Create index "shared_services_id_team_key" to table: "shared_services"
CREATE UNIQUE INDEX "shared_services_id_team_key" ON "public"."shared_services" ("id", "team_id");
-- Create index "shared_services_server_guest_key" to table: "shared_services"
CREATE UNIQUE INDEX "shared_services_server_guest_key" ON "public"."shared_services" ("server_id", "guest_vmid");
-- Create index "shared_services_team_idx" to table: "shared_services"
CREATE INDEX "shared_services_team_idx" ON "public"."shared_services" ("team_id");
