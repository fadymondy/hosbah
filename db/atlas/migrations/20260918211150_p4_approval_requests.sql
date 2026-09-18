-- Modify "teams" table
ALTER TABLE "public"."teams" ADD COLUMN "require_destructive_approval" boolean NOT NULL DEFAULT false;
-- Create "approval_requests" table
CREATE TABLE "public"."approval_requests" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "token_id" text NOT NULL,
  "user_id" text NOT NULL,
  "tool" text NOT NULL,
  "resource_type" text NOT NULL,
  "resource_name" text NOT NULL,
  "args_summary" text NOT NULL DEFAULT '{}',
  "status" text NOT NULL DEFAULT 'pending',
  "decided_by" text NULL,
  "decided_at" timestamptz NULL,
  "expires_at" timestamptz NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "approval_requests_decided_check" CHECK ((status = ANY (ARRAY['pending'::text, 'expired'::text])) = (decided_by IS NULL)),
  CONSTRAINT "approval_requests_resource_type_check" CHECK (resource_type = ANY (ARRAY['app'::text, 'server'::text, 'database'::text, 'domain'::text, 'data_service'::text, 'backup'::text, 'invitation'::text, 'member'::text])),
  CONSTRAINT "approval_requests_status_check" CHECK (status = ANY (ARRAY['pending'::text, 'approved'::text, 'rejected'::text, 'used'::text, 'expired'::text]))
);
-- Create index "approval_requests_id_team_key" to table: "approval_requests"
CREATE UNIQUE INDEX "approval_requests_id_team_key" ON "public"."approval_requests" ("id", "team_id");
-- Create index "approval_requests_open_key" to table: "approval_requests"
CREATE UNIQUE INDEX "approval_requests_open_key" ON "public"."approval_requests" ("team_id", "token_id", "tool", "resource_type", "resource_name") WHERE (status = ANY (ARRAY['pending'::text, 'approved'::text]));
-- Create index "approval_requests_team_status_idx" to table: "approval_requests"
CREATE INDEX "approval_requests_team_status_idx" ON "public"."approval_requests" ("team_id", "status", "created_at");
