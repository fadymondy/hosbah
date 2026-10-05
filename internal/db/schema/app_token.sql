-- Hand-written (MH-1153): the integration API's app tokens and one-time panel links.
--
-- An app token (hba_…, internal/tokens) is scoped to exactly one app: /api/integrations/app*
-- resolves it by hash and never looks past app_id. Only the SHA-256 is stored. Deleting the app
-- deletes its tokens, so a token dies with its app. (The admin MCP token shares the hba_ prefix;
-- each is looked up only in its own table, so neither can authenticate as the other.)
CREATE TABLE app_tokens (
    id text PRIMARY KEY DEFAULT gen_random_uuid()::text,
    team_id text NOT NULL,
    app_id text NOT NULL,
    label text NOT NULL,
    token_hash text NOT NULL,
    created_by text NOT NULL,
    last_used_at timestamptz,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT app_tokens_label_check CHECK (char_length(label) BETWEEN 1 AND 120)
);

CREATE UNIQUE INDEX app_tokens_token_hash_key ON app_tokens (token_hash);
CREATE INDEX app_tokens_app_idx ON app_tokens (app_id, created_at);
CREATE INDEX app_tokens_team_idx ON app_tokens (team_id);

-- A one-time sign-in link to one app's page in the panel, minted by an app token for a customer
-- email. Only the hash of its secret is stored; it is good once (used_at) and for a few minutes.
CREATE TABLE app_panel_links (
    id text PRIMARY KEY DEFAULT gen_random_uuid()::text,
    team_id text NOT NULL,
    app_id text NOT NULL,
    app_token_id text NOT NULL,
    link_hash text NOT NULL,
    email text NOT NULL,
    name text NOT NULL DEFAULT '',
    locale text NOT NULL DEFAULT 'en',
    expires_at timestamptz NOT NULL,
    used_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT app_panel_links_locale_check CHECK (locale IN ('en', 'ar'))
);

CREATE UNIQUE INDEX app_panel_links_link_hash_key ON app_panel_links (link_hash);
CREATE INDEX app_panel_links_app_idx ON app_panel_links (app_id);
CREATE INDEX app_panel_links_team_idx ON app_panel_links (team_id);
