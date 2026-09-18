-- Create "proxy_hosts" table
CREATE TABLE "public"."proxy_hosts" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "server_id" text NOT NULL,
  "hostname" text NOT NULL,
  "upstream" text NOT NULL,
  "tls" text NOT NULL DEFAULT 'auto',
  "enabled" boolean NOT NULL DEFAULT true,
  "websockets" boolean NOT NULL DEFAULT true,
  "verify_token" text NOT NULL,
  "verified_at" timestamptz NULL,
  "created_by" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "proxy_hosts_server_fk" FOREIGN KEY ("server_id", "team_id") REFERENCES "public"."servers" ("id", "team_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "proxy_hosts_team_fk" FOREIGN KEY ("team_id") REFERENCES "public"."teams" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "proxy_hosts_hostname_check" CHECK ((hostname = lower(hostname)) AND (length(hostname) <= 253) AND (hostname ~ '^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$'::text)),
  CONSTRAINT "proxy_hosts_tls_check" CHECK (tls = ANY (ARRAY['auto'::text, 'internal'::text, 'off'::text])),
  CONSTRAINT "proxy_hosts_upstream_check" CHECK ((length(upstream) >= 3) AND (length(upstream) <= 300))
);
-- Create index "proxy_hosts_hostname_key" to table: "proxy_hosts"
CREATE UNIQUE INDEX "proxy_hosts_hostname_key" ON "public"."proxy_hosts" ("hostname");
-- Create index "proxy_hosts_server_idx" to table: "proxy_hosts"
CREATE INDEX "proxy_hosts_server_idx" ON "public"."proxy_hosts" ("server_id");
