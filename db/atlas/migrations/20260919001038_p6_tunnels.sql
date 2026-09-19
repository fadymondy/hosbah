-- Modify "credentials" table
ALTER TABLE "public"."credentials" DROP CONSTRAINT "credentials_provider_check", ADD CONSTRAINT "credentials_provider_check" CHECK (provider = ANY (ARRAY['proxmox'::text, 'cloudflare'::text, 'frp'::text, 'npm'::text, 'tailscale'::text]));
-- Create "tunnels" table
CREATE TABLE "public"."tunnels" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "server_id" text NOT NULL,
  "credential_id" text NOT NULL,
  "kind" text NOT NULL,
  "remote_port" integer NULL,
  "status" text NOT NULL DEFAULT 'pending',
  "address" text NULL,
  "error" text NULL,
  "created_by" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "tunnels_credential_fk" FOREIGN KEY ("credential_id") REFERENCES "public"."credentials" ("id") ON UPDATE NO ACTION ON DELETE RESTRICT,
  CONSTRAINT "tunnels_server_fk" FOREIGN KEY ("server_id", "team_id") REFERENCES "public"."servers" ("id", "team_id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "tunnels_team_fk" FOREIGN KEY ("team_id") REFERENCES "public"."teams" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "tunnels_kind_check" CHECK (kind = ANY (ARRAY['frp'::text, 'tailscale'::text])),
  CONSTRAINT "tunnels_port_check" CHECK ((remote_port IS NULL) OR ((remote_port >= 1) AND (remote_port <= 65535))),
  CONSTRAINT "tunnels_status_check" CHECK (status = ANY (ARRAY['pending'::text, 'active'::text, 'failed'::text, 'removing'::text]))
);
-- Create index "tunnels_credential_port_key" to table: "tunnels"
CREATE UNIQUE INDEX "tunnels_credential_port_key" ON "public"."tunnels" ("credential_id", "remote_port") WHERE (remote_port IS NOT NULL);
-- Create index "tunnels_server_kind_key" to table: "tunnels"
CREATE UNIQUE INDEX "tunnels_server_kind_key" ON "public"."tunnels" ("server_id", "kind");
-- Create index "tunnels_team_idx" to table: "tunnels"
CREATE INDEX "tunnels_team_idx" ON "public"."tunnels" ("team_id");
