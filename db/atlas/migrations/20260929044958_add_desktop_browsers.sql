-- Modify "app_browsers" table
ALTER TABLE "public"."app_browsers" DROP CONSTRAINT "app_browsers_memory_check", ADD CONSTRAINT "app_browsers_memory_check" CHECK ((memory_mb >= 256) AND (memory_mb <= 8192)), ADD CONSTRAINT "app_browsers_hostname_check" CHECK ((hostname IS NULL) OR (hostname ~ '^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$'::text)), ADD CONSTRAINT "app_browsers_kind_check" CHECK (kind = ANY (ARRAY['headless'::text, 'desktop'::text])), ADD COLUMN "kind" text NOT NULL DEFAULT 'headless', ADD COLUMN "hostname" text NULL;
-- Create index "app_browsers_hostname_key" to table: "app_browsers"
CREATE UNIQUE INDEX "app_browsers_hostname_key" ON "public"."app_browsers" ("hostname") WHERE (hostname IS NOT NULL);
-- Modify "proxy_hosts" table
ALTER TABLE "public"."proxy_hosts" ADD CONSTRAINT "proxy_hosts_forward_auth_check" CHECK ((forward_auth IS NULL) OR (forward_auth ~~ 'https://%'::text)), ADD COLUMN "browser_id" text NULL, ADD COLUMN "forward_auth" text NULL;
-- Create index "proxy_hosts_browser_idx" to table: "proxy_hosts"
CREATE INDEX "proxy_hosts_browser_idx" ON "public"."proxy_hosts" ("browser_id") WHERE (browser_id IS NOT NULL);
-- Create "browser_auth_tokens" table
CREATE TABLE "public"."browser_auth_tokens" (
  "id" text NOT NULL DEFAULT (gen_random_uuid())::text,
  "team_id" text NOT NULL,
  "browser_id" text NOT NULL,
  "user_id" text NOT NULL,
  "token_hash" text NOT NULL,
  "expires_at" timestamptz NOT NULL,
  "used_at" timestamptz NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "browser_auth_tokens_expiry_check" CHECK (expires_at > created_at)
);
-- Create index "browser_auth_tokens_browser_idx" to table: "browser_auth_tokens"
CREATE INDEX "browser_auth_tokens_browser_idx" ON "public"."browser_auth_tokens" ("browser_id");
-- Create index "browser_auth_tokens_expires_idx" to table: "browser_auth_tokens"
CREATE INDEX "browser_auth_tokens_expires_idx" ON "public"."browser_auth_tokens" ("expires_at");
-- Create index "browser_auth_tokens_token_hash_key" to table: "browser_auth_tokens"
CREATE UNIQUE INDEX "browser_auth_tokens_token_hash_key" ON "public"."browser_auth_tokens" ("token_hash");
