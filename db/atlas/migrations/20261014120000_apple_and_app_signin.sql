-- Sign in with Apple, and the phone app's OAuth hand-off (internal/account/oauth.go, apple.go).
ALTER TABLE "user_identities" DROP CONSTRAINT "user_identities_provider_check";
ALTER TABLE "user_identities" ADD CONSTRAINT "user_identities_provider_check" CHECK (provider IN ('google', 'github', 'apple'));
ALTER TABLE "user_identities" ADD COLUMN "provider_token_sealed" text NOT NULL DEFAULT '';
ALTER TABLE "oauth_states" DROP CONSTRAINT "oauth_states_purpose_check";
ALTER TABLE "oauth_states" ADD CONSTRAINT "oauth_states_purpose_check" CHECK (purpose IN ('login', 'link', 'github_install', 'app_handoff'));
ALTER TABLE "oauth_states" ADD COLUMN "app_challenge" text NOT NULL DEFAULT '';
