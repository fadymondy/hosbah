-- Hosbah: platform administration (hand-written, not a make:resource entity: neither table is a
-- tenant resource and neither is exposed through generated CRUD).

-- platform_settings: exactly one row (id is always true). Read by anyone — the public subset is
-- served unauthenticated, and sign-up, team creation and AddServer enforce the limits — written
-- only by a platform admin (RLS: *_platform_row_level_security.sql).
CREATE TABLE platform_settings (
    id boolean PRIMARY KEY DEFAULT true,
    signup_mode text NOT NULL DEFAULT 'open',
    announcement text NOT NULL DEFAULT '',
    announcement_level text NOT NULL DEFAULT 'info',
    support_email text NOT NULL DEFAULT '',
    max_teams_per_user bigint NOT NULL DEFAULT 0,
    max_servers_per_team bigint NOT NULL DEFAULT 0,
    updated_by text,
    updated_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE platform_settings ADD CONSTRAINT platform_settings_singleton CHECK (id);
ALTER TABLE platform_settings ADD CONSTRAINT platform_settings_signup_mode_check CHECK (signup_mode IN ('open', 'invite_only', 'closed'));
ALTER TABLE platform_settings ADD CONSTRAINT platform_settings_announcement_level_check CHECK (announcement_level IN ('info', 'warning'));
ALTER TABLE platform_settings ADD CONSTRAINT platform_settings_announcement_length CHECK (char_length(announcement) <= 500);
ALTER TABLE platform_settings ADD CONSTRAINT platform_settings_limits_check CHECK (max_teams_per_user >= 0 AND max_servers_per_team >= 0);

-- platform_audit: what platform admins did (user, team and settings changes). Append-only:
-- RLS grants no UPDATE or DELETE. details never holds a secret.
CREATE TABLE platform_audit (
    id text PRIMARY KEY DEFAULT gen_random_uuid()::text,
    actor_id text NOT NULL,
    action text NOT NULL,
    target_type text NOT NULL,
    target_id text NOT NULL,
    details jsonb NOT NULL DEFAULT '{}',
    ip text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX platform_audit_created_idx ON platform_audit (created_at);
