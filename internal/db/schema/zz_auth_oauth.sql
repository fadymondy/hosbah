-- Hosbah: sign-in with Google and GitHub (internal/account/oauth.go), and the one-time states of
-- every browser round trip through a third party (OAuth sign-in, linking, the GitHub App install).
-- Hand-written like the rest of the account cycle: platform data, never exposed through generated CRUD.

-- An external account linked to a Hosbah user. subject is the provider's stable user id (Google's
-- "sub", GitHub's numeric id), never the email: an identity keeps working after the address changes
-- at the provider. email_at_link is the provider-verified address when it was linked (shown in the
-- account settings). FORCE row-level security: a signed-in user reads and deletes only their own;
-- the sign-in callback, before anyone is signed in, looks identities up in system scope.
CREATE TABLE user_identities (
    id text PRIMARY KEY DEFAULT gen_random_uuid()::text,
    user_id text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    provider text NOT NULL,
    subject text NOT NULL,
    email_at_link text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    -- Apple only: the refresh token Apple issued at sign-in, with the client id it was issued to,
    -- sealed with the platform key (crypto.SealPlatform). Deleting the account revokes it at
    -- Apple, as App Store review requires. Empty for every other provider.
    provider_token_sealed text NOT NULL DEFAULT ''
);
ALTER TABLE user_identities ADD CONSTRAINT user_identities_provider_check CHECK (provider IN ('google', 'github', 'apple'));
ALTER TABLE user_identities ADD CONSTRAINT user_identities_subject_check CHECK (length(subject) BETWEEN 1 AND 255);
CREATE UNIQUE INDEX user_identities_provider_subject_key ON user_identities (provider, subject);
CREATE INDEX user_identities_user_idx ON user_identities (user_id);

-- One browser round trip through a provider. id is the SHA-256 of the state parameter sent to the
-- provider; browser_hash the SHA-256 of the random value in the HttpOnly cookie set on the same
-- browser, so a state only completes in the browser that started it (no login CSRF, no replay from
-- another machine). Single use: the callback deletes the row and only the request that deleted it
-- goes on. Ten minutes. code_verifier is the PKCE secret, which never leaves the server; nonce is
-- Google's id_token nonce. next is an already-validated same-origin path. invitation_hash is the
-- SHA-256 of an invitation token a sign-up carried (invite-only mode). user_id: the signed-in
-- user for purpose 'link' and 'github_install'; team_id: the team for 'github_install'. Like
-- account_challenges it has no row-level security: it is read before anyone is signed in.
CREATE TABLE oauth_states (
    id text PRIMARY KEY,
    browser_hash text NOT NULL,
    purpose text NOT NULL,
    provider text NOT NULL DEFAULT '',
    code_verifier text NOT NULL DEFAULT '',
    nonce text NOT NULL DEFAULT '',
    next text NOT NULL DEFAULT '',
    locale text NOT NULL DEFAULT 'en',
    invitation_hash text NOT NULL DEFAULT '',
    user_id text REFERENCES users (id) ON DELETE CASCADE,
    team_id text,
    -- The phone app's round trip (?app=1): the SHA-256 (hex) of a verifier only the app holds. The
    -- callback then hands the app a one-time code (purpose 'app_handoff': id = SHA-256 of the code,
    -- browser_hash = this challenge, user_id = the account) instead of a web session, and only the
    -- app can trade it for one (POST /api/auth/oauth/exchange with the verifier).
    app_challenge text NOT NULL DEFAULT '',
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE oauth_states ADD CONSTRAINT oauth_states_purpose_check CHECK (purpose IN ('login', 'link', 'github_install', 'app_handoff'));
CREATE INDEX oauth_states_expires_idx ON oauth_states (expires_at);
