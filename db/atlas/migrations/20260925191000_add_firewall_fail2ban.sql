-- Create "fail2ban_settings" table
CREATE TABLE "public"."fail2ban_settings" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "server_id" text NOT NULL,
  "enabled" boolean NOT NULL DEFAULT false,
  "bantime" integer NOT NULL DEFAULT 600,
  "findtime" integer NOT NULL DEFAULT 600,
  "maxretry" integer NOT NULL DEFAULT 5,
  "updated_by" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "fail2ban_settings_bantime_check" CHECK ((bantime >= 30) AND (bantime <= 31536000)),
  CONSTRAINT "fail2ban_settings_findtime_check" CHECK ((findtime >= 10) AND (findtime <= 31536000)),
  CONSTRAINT "fail2ban_settings_maxretry_check" CHECK ((maxretry >= 1) AND (maxretry <= 100))
);
-- Create index "fail2ban_settings_server_key" to table: "fail2ban_settings"
CREATE UNIQUE INDEX "fail2ban_settings_server_key" ON "public"."fail2ban_settings" ("server_id");
-- Create index "fail2ban_settings_team_idx" to table: "fail2ban_settings"
CREATE INDEX "fail2ban_settings_team_idx" ON "public"."fail2ban_settings" ("team_id");
-- Create "firewall_rules" table
CREATE TABLE "public"."firewall_rules" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "server_id" text NOT NULL,
  "action" text NOT NULL,
  "proto" text NOT NULL,
  "port_start" integer NOT NULL,
  "port_end" integer NOT NULL,
  "source_cidr" text NOT NULL DEFAULT 'any',
  "comment" text NOT NULL DEFAULT '',
  "enabled" boolean NOT NULL DEFAULT true,
  "position" integer NOT NULL DEFAULT 0,
  "created_by" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "firewall_rules_action_check" CHECK (action = ANY (ARRAY['allow'::text, 'deny'::text])),
  CONSTRAINT "firewall_rules_comment_check" CHECK (length(comment) <= 200),
  CONSTRAINT "firewall_rules_port_check" CHECK ((port_start >= 1) AND (port_start <= 65535) AND (port_end >= port_start) AND (port_end <= 65535)),
  CONSTRAINT "firewall_rules_proto_check" CHECK (proto = ANY (ARRAY['tcp'::text, 'udp'::text, 'any'::text]))
);
-- Create index "firewall_rules_server_idx" to table: "firewall_rules"
CREATE INDEX "firewall_rules_server_idx" ON "public"."firewall_rules" ("server_id", "position");
-- Create index "firewall_rules_team_idx" to table: "firewall_rules"
CREATE INDEX "firewall_rules_team_idx" ON "public"."firewall_rules" ("team_id");
-- Create "firewall_settings" table
CREATE TABLE "public"."firewall_settings" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "server_id" text NOT NULL,
  "default_policy" text NOT NULL DEFAULT 'accept',
  "ssh_port" integer NOT NULL DEFAULT 22,
  "updated_by" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "firewall_settings_policy_check" CHECK (default_policy = ANY (ARRAY['accept'::text, 'drop'::text])),
  CONSTRAINT "firewall_settings_ssh_port_check" CHECK ((ssh_port >= 1) AND (ssh_port <= 65535))
);
-- Create index "firewall_settings_server_key" to table: "firewall_settings"
CREATE UNIQUE INDEX "firewall_settings_server_key" ON "public"."firewall_settings" ("server_id");
-- Create index "firewall_settings_team_idx" to table: "firewall_settings"
CREATE INDEX "firewall_settings_team_idx" ON "public"."firewall_settings" ("team_id");
