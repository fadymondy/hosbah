-- Create "cost_line_items" table
CREATE TABLE "public"."cost_line_items" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "name" text NOT NULL,
  "amount_cents" bigint NOT NULL,
  "currency" text NOT NULL DEFAULT 'USD',
  "created_by" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id")
);
-- Create index "cost_line_items_team_idx" to table: "cost_line_items"
CREATE INDEX "cost_line_items_team_idx" ON "public"."cost_line_items" ("team_id");
-- Create "server_costs" table
CREATE TABLE "public"."server_costs" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "server_id" text NOT NULL,
  "amount_cents" bigint NOT NULL,
  "currency" text NOT NULL DEFAULT 'USD',
  "created_by" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id")
);
-- Create index "server_costs_server_id_key" to table: "server_costs"
CREATE UNIQUE INDEX "server_costs_server_id_key" ON "public"."server_costs" ("server_id");
-- Create index "server_costs_team_idx" to table: "server_costs"
CREATE INDEX "server_costs_team_idx" ON "public"."server_costs" ("team_id");
