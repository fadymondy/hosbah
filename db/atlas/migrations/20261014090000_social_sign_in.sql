-- Sign in with Apple, the phone app's social sign-in, and self-service account deletion
-- (internal/account/apple.go, native.go, delete.go). Desired state: internal/db/schema/zz_auth_oauth.sql
-- and zz_auth_account.sql. Hand-written: `togo migrate:diff` cannot load the schema fragments in
-- name order on main (panel_session.sql references users before zz_auth.sql creates it).

-- Apple joins Google and GitHub; the name Apple shares once, and Apple's sealed refresh token.
ALTER TABLE "user_identities" DROP CONSTRAINT "user_identities_provider_check";
ALTER TABLE "user_identities" ADD CONSTRAINT "user_identities_provider_check" CHECK (provider IN ('google', 'github', 'apple'));
ALTER TABLE "user_identities" ADD COLUMN "display_name" text NOT NULL DEFAULT '';
ALTER TABLE "user_identities" ADD COLUMN "provider_token" text NOT NULL DEFAULT '';

-- A round trip the phone app started (its PKCE challenge), and Apple's form_post answer kept for the
-- same-site GET that finishes it.
ALTER TABLE "oauth_states" ADD COLUMN "app_challenge" text NOT NULL DEFAULT '';
ALTER TABLE "oauth_states" ADD COLUMN "provider_code" text NOT NULL DEFAULT '';
ALTER TABLE "oauth_states" ADD COLUMN "provider_user" text NOT NULL DEFAULT '';

-- One-time codes the phone app redeems with its PKCE verifier. No row-level security (redeemed
-- before anyone is signed in), like oauth_states.
CREATE TABLE "oauth_app_codes" (
  "id" text NOT NULL,
  "user_id" text NOT NULL,
  "provider" text NOT NULL,
  "code_challenge" text NOT NULL,
  "expires_at" timestamptz NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "oauth_app_codes_user_id_fkey" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
CREATE INDEX "oauth_app_codes_expires_idx" ON "oauth_app_codes" ("expires_at");

-- The emailed code that confirms deleting an account without a password.
ALTER TABLE "account_codes" DROP CONSTRAINT "account_codes_purpose_check";
ALTER TABLE "account_codes" ADD CONSTRAINT "account_codes_purpose_check" CHECK (purpose IN ('verify', 'reset', 'login', 'delete'));
