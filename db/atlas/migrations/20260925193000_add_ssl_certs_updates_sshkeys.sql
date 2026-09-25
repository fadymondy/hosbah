-- Create "server_update_statuses" table
CREATE TABLE "public"."server_update_statuses" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "server_id" text NOT NULL,
  "os_family" text NOT NULL DEFAULT '',
  "package_manager" text NOT NULL DEFAULT '',
  "upgradable_count" bigint NOT NULL DEFAULT 0,
  "packages" jsonb NOT NULL DEFAULT '[]',
  "reboot_required" boolean NOT NULL DEFAULT false,
  "auto_security_updates" boolean NOT NULL DEFAULT false,
  "auto_supported" boolean NOT NULL DEFAULT true,
  "last_checked_at" timestamptz NULL,
  "last_upgraded_at" timestamptz NULL,
  "last_error" text NOT NULL DEFAULT '',
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "server_update_statuses_os_family_check" CHECK (os_family = ANY (ARRAY[''::text, 'debian'::text, 'rhel'::text, 'alpine'::text, 'other'::text])),
  CONSTRAINT "server_update_statuses_pm_check" CHECK (package_manager = ANY (ARRAY[''::text, 'apt'::text, 'dnf'::text, 'apk'::text]))
);
-- Create index "server_update_statuses_server_key" to table: "server_update_statuses"
CREATE UNIQUE INDEX "server_update_statuses_server_key" ON "public"."server_update_statuses" ("server_id");
-- Create index "server_update_statuses_team_idx" to table: "server_update_statuses"
CREATE INDEX "server_update_statuses_team_idx" ON "public"."server_update_statuses" ("team_id");
-- Create "ssh_keys" table
CREATE TABLE "public"."ssh_keys" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "name" text NOT NULL,
  "key_type" text NOT NULL,
  "public_key" text NOT NULL,
  "fingerprint" text NOT NULL,
  "created_by" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "ssh_keys_name_check" CHECK ((length(name) >= 1) AND (length(name) <= 80)),
  CONSTRAINT "ssh_keys_public_key_check" CHECK ((length(public_key) >= 1) AND (length(public_key) <= 8192)),
  CONSTRAINT "ssh_keys_type_check" CHECK (key_type = ANY (ARRAY['ed25519'::text, 'rsa'::text, 'ecdsa'::text]))
);
-- Create index "ssh_keys_id_team_key" to table: "ssh_keys"
CREATE UNIQUE INDEX "ssh_keys_id_team_key" ON "public"."ssh_keys" ("id", "team_id");
-- Create index "ssh_keys_team_fingerprint_key" to table: "ssh_keys"
CREATE UNIQUE INDEX "ssh_keys_team_fingerprint_key" ON "public"."ssh_keys" ("team_id", "fingerprint");
-- Create index "ssh_keys_team_idx" to table: "ssh_keys"
CREATE INDEX "ssh_keys_team_idx" ON "public"."ssh_keys" ("team_id");
-- Create index "ssh_keys_team_name_key" to table: "ssh_keys"
CREATE UNIQUE INDEX "ssh_keys_team_name_key" ON "public"."ssh_keys" ("team_id", "name");
-- Create "ssl_certificates" table
CREATE TABLE "public"."ssl_certificates" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "hostname" text NOT NULL,
  "source" text NOT NULL,
  "ok" boolean NOT NULL DEFAULT false,
  "issuer" text NOT NULL DEFAULT '',
  "not_before" timestamptz NULL,
  "not_after" timestamptz NULL,
  "san_match" boolean NOT NULL DEFAULT false,
  "error" text NOT NULL DEFAULT '',
  "alerted_14" boolean NOT NULL DEFAULT false,
  "alerted_7" boolean NOT NULL DEFAULT false,
  "alerted_1" boolean NOT NULL DEFAULT false,
  "alerted_failure" boolean NOT NULL DEFAULT false,
  "last_checked_at" timestamptz NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "ssl_certificates_hostname_check" CHECK (length(hostname) <= 253),
  CONSTRAINT "ssl_certificates_source_check" CHECK (source = ANY (ARRAY['app'::text, 'proxy'::text]))
);
-- Create index "ssl_certificates_not_after_idx" to table: "ssl_certificates"
CREATE INDEX "ssl_certificates_not_after_idx" ON "public"."ssl_certificates" ("not_after");
-- Create index "ssl_certificates_team_hostname_key" to table: "ssl_certificates"
CREATE UNIQUE INDEX "ssl_certificates_team_hostname_key" ON "public"."ssl_certificates" ("team_id", "hostname");
-- Create index "ssl_certificates_team_idx" to table: "ssl_certificates"
CREATE INDEX "ssl_certificates_team_idx" ON "public"."ssl_certificates" ("team_id");
-- Create "ssh_key_installs" table
CREATE TABLE "public"."ssh_key_installs" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "ssh_key_id" text NOT NULL,
  "server_id" text NOT NULL,
  "unix_user" text NOT NULL,
  "status" text NOT NULL DEFAULT 'installed',
  "last_operation_id" text NULL,
  "last_error" text NOT NULL DEFAULT '',
  "installed_at" timestamptz NULL,
  "removed_at" timestamptz NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "ssh_key_installs_key_fk" FOREIGN KEY ("ssh_key_id", "team_id") REFERENCES "public"."ssh_keys" ("id", "team_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "ssh_key_installs_status_check" CHECK (status = ANY (ARRAY['installed'::text, 'removed'::text, 'failed'::text])),
  CONSTRAINT "ssh_key_installs_user_check" CHECK (unix_user ~ '^[a-z_][a-z0-9_-]{0,31}$'::text)
);
-- Create index "ssh_key_installs_key_server_user_key" to table: "ssh_key_installs"
CREATE UNIQUE INDEX "ssh_key_installs_key_server_user_key" ON "public"."ssh_key_installs" ("ssh_key_id", "server_id", "unix_user");
-- Create index "ssh_key_installs_server_idx" to table: "ssh_key_installs"
CREATE INDEX "ssh_key_installs_server_idx" ON "public"."ssh_key_installs" ("server_id");
-- Create index "ssh_key_installs_team_idx" to table: "ssh_key_installs"
CREATE INDEX "ssh_key_installs_team_idx" ON "public"."ssh_key_installs" ("team_id");
