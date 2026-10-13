-- Hosbah: a signed-in phone or desktop app (docs/apps-plan.md). One row per install, owned by one
-- user, never by a team: the same phone follows its user into every team they are in.
--
-- refresh_hash is the SHA-256 of the install's device refresh token (hbr_…), the long-lived half
-- of its session: POST /api/auth/device/refresh trades it for a fresh 24-hour session token and a
-- new refresh token (the old one stops working). push_token is the install's FCM registration
-- token, NULL until the app has notification permission.
CREATE TABLE devices (
    id text PRIMARY KEY DEFAULT gen_random_uuid()::text,
    user_id text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name text NOT NULL,
    platform text NOT NULL,
    app text NOT NULL,
    locale text NOT NULL DEFAULT 'ar',
    app_version text NOT NULL DEFAULT '',
    refresh_hash text NOT NULL,
    push_token text,
    created_at timestamptz NOT NULL DEFAULT now(),
    refreshed_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT devices_platform_check CHECK (platform IN ('ios', 'android', 'windows', 'macos', 'linux')),
    CONSTRAINT devices_app_check CHECK (app IN ('mobile', 'desktop')),
    CONSTRAINT devices_locale_check CHECK (locale IN ('ar', 'en')),
    CONSTRAINT devices_name_check CHECK (char_length(name) BETWEEN 1 AND 80)
);

CREATE UNIQUE INDEX devices_refresh_hash_idx ON devices (refresh_hash);
CREATE UNIQUE INDEX devices_push_token_idx ON devices (push_token) WHERE push_token IS NOT NULL;
CREATE INDEX devices_user_idx ON devices (user_id);
