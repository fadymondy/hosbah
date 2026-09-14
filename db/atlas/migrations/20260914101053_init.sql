-- Create "admin_mcp_tokens" table
CREATE TABLE "public"."admin_mcp_tokens" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "user_id" text NOT NULL,
  "name" text NOT NULL,
  "token_hash" text NOT NULL,
  "expires_at" timestamptz NULL,
  "last_used_at" timestamptz NULL,
  "revoked_at" timestamptz NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id")
);
-- Create index "admin_mcp_tokens_token_hash_key" to table: "admin_mcp_tokens"
CREATE UNIQUE INDEX "admin_mcp_tokens_token_hash_key" ON "public"."admin_mcp_tokens" ("token_hash");
-- Create "teams" table
CREATE TABLE "public"."teams" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "name" text NOT NULL,
  "slug" text NOT NULL,
  "owner_id" text NOT NULL,
  "dek_wrapped" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id")
);
-- Create index "teams_slug_key" to table: "teams"
CREATE UNIQUE INDEX "teams_slug_key" ON "public"."teams" ("slug");
-- Create "invitations" table
CREATE TABLE "public"."invitations" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "email" text NOT NULL,
  "role" text NOT NULL,
  "token_hash" text NOT NULL,
  "status" text NOT NULL,
  "invited_by" text NOT NULL,
  "expires_at" timestamptz NOT NULL,
  "accepted_at" timestamptz NULL,
  "accepted_by" text NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "invitations_team_fk" FOREIGN KEY ("team_id") REFERENCES "public"."teams" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "invitations_role_check" CHECK (role = ANY (ARRAY['admin'::text, 'deployer'::text, 'viewer'::text])),
  CONSTRAINT "invitations_status_check" CHECK (status = ANY (ARRAY['pending'::text, 'accepted'::text, 'revoked'::text]))
);
-- Create index "invitations_team_idx" to table: "invitations"
CREATE INDEX "invitations_team_idx" ON "public"."invitations" ("team_id", "created_at");
-- Create index "invitations_token_hash_key" to table: "invitations"
CREATE UNIQUE INDEX "invitations_token_hash_key" ON "public"."invitations" ("token_hash");
-- Create "mcp_tokens" table
CREATE TABLE "public"."mcp_tokens" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "user_id" text NOT NULL,
  "name" text NOT NULL,
  "token_hash" text NOT NULL,
  "scopes" text NOT NULL,
  "allowlist" jsonb NULL,
  "expires_at" timestamptz NULL,
  "last_used_at" timestamptz NULL,
  "revoked_at" timestamptz NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "mcp_tokens_team_fk" FOREIGN KEY ("team_id") REFERENCES "public"."teams" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Create index "mcp_tokens_team_idx" to table: "mcp_tokens"
CREATE INDEX "mcp_tokens_team_idx" ON "public"."mcp_tokens" ("team_id", "created_at");
-- Create index "mcp_tokens_token_hash_key" to table: "mcp_tokens"
CREATE UNIQUE INDEX "mcp_tokens_token_hash_key" ON "public"."mcp_tokens" ("token_hash");
-- Create "reports" table
CREATE TABLE "public"."reports" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NULL,
  "number" bigint NOT NULL,
  "key" text NOT NULL,
  "title" text NOT NULL,
  "body" text NULL,
  "issue_type" text NOT NULL,
  "status" text NOT NULL,
  "state" text NOT NULL,
  "resolution" text NULL,
  "priority" text NOT NULL,
  "labels" text NULL,
  "assignee_id" text NULL,
  "reporter_id" text NULL,
  "parent_id" text NULL,
  "milestone" text NULL,
  "external_source" text NULL,
  "external_id" text NULL,
  "external_key" text NULL,
  "external_url" text NULL,
  "page_url" text NULL,
  "route" text NULL,
  "selector" text NULL,
  "screenshot" text NULL,
  "console_log" text NULL,
  "network_log" text NULL,
  "user_agent" text NULL,
  "viewport" text NULL,
  "closed_at" timestamptz NULL,
  "sort_order" bigint NOT NULL DEFAULT 0,
  "started_at" timestamptz NULL,
  "finished_at" timestamptz NULL,
  "agent_seconds" bigint NOT NULL DEFAULT 0,
  "agent_session" text NULL,
  "human_only" boolean NOT NULL DEFAULT false,
  "ai_model" text NULL,
  "ai_effort" text NULL,
  "agent" text NULL,
  "skill" text NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id")
);
-- Create index "reports_key_key" to table: "reports"
CREATE UNIQUE INDEX "reports_key_key" ON "public"."reports" ("key");
-- Create index "reports_number_key" to table: "reports"
CREATE UNIQUE INDEX "reports_number_key" ON "public"."reports" ("number");
-- Create index "reports_reporter_idx" to table: "reports"
CREATE INDEX "reports_reporter_idx" ON "public"."reports" ("reporter_id", "created_at");
-- Create index "reports_status_idx" to table: "reports"
CREATE INDEX "reports_status_idx" ON "public"."reports" ("status", "sort_order");
-- Create "report_comments" table
CREATE TABLE "public"."report_comments" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "report_id" text NOT NULL,
  "author_id" text NULL,
  "author_kind" text NOT NULL,
  "body" text NOT NULL,
  "mentions" text NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "report_comments_report_fk" FOREIGN KEY ("report_id") REFERENCES "public"."reports" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Create index "report_comments_report_idx" to table: "report_comments"
CREATE INDEX "report_comments_report_idx" ON "public"."report_comments" ("report_id", "created_at");
-- Create "team_members" table
CREATE TABLE "public"."team_members" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "user_id" text NOT NULL,
  "role" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "team_members_team_fk" FOREIGN KEY ("team_id") REFERENCES "public"."teams" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "team_members_role_check" CHECK (role = ANY (ARRAY['owner'::text, 'admin'::text, 'deployer'::text, 'viewer'::text]))
);
-- Create index "team_members_team_user_key" to table: "team_members"
CREATE UNIQUE INDEX "team_members_team_user_key" ON "public"."team_members" ("team_id", "user_id");
-- Create index "team_members_user_idx" to table: "team_members"
CREATE INDEX "team_members_user_idx" ON "public"."team_members" ("user_id");
