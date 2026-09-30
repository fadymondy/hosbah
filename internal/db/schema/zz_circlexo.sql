-- Hosbah: CircleXO hub integration (internal/circlexo, docs/circlexo.md). Platform data, not
-- tenant-scoped: read before a team is known (sign-in, webhooks, MCP token mapping), so like
-- oauth_states and users there is no row-level security. Nothing here is used unless
-- CIRCLEXO_ISSUER is set. The teams foreign key is here (sorted after team.sql) for the reason
-- zz_constraints.sql gives.

-- A hub account (its `sub`) and the Hosbah user it signs in as. Unique both ways: one hub account
-- is one Hosbah user, and a Hosbah user has at most one hub account.
CREATE TABLE circlexo_user_links (
    subject    text        PRIMARY KEY,
    user_id    text        NOT NULL UNIQUE REFERENCES users (id) ON DELETE CASCADE,
    email      text        NOT NULL DEFAULT '',
    linked_via text        NOT NULL DEFAULT 'email' CHECK (linked_via IN ('email', 'created')),
    created_at timestamptz NOT NULL DEFAULT now()
);

-- A hub org and the team that serves it. Unique both ways. `status` goes inactive on deprovision
-- (org.app_removed): the team and its data stay, the link stops mapping. team_slug and locale are
-- copied here because a read of `teams` needs a team scope (FORCE row-level security) and the
-- sign-in and webhook paths do not have one yet.
CREATE TABLE circlexo_org_links (
    org_id     text        PRIMARY KEY,
    team_id    text        NOT NULL UNIQUE REFERENCES teams (id) ON DELETE CASCADE,
    team_slug  text        NOT NULL DEFAULT '',
    org_slug   text        NOT NULL DEFAULT '',
    locale     text        NOT NULL DEFAULT 'en' CHECK (locale IN ('en', 'ar')),
    status     text        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Hosbah sessions started from a hub sign-in, by the hub session id (`sid`) and the JWT issue
-- time, so session.revoked can end exactly those and not the user's other sessions.
CREATE TABLE circlexo_sessions (
    id         text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    sid        text        NOT NULL,
    user_id    text        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    issued_at  timestamptz NOT NULL,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX circlexo_sessions_sid ON circlexo_sessions (sid);
CREATE INDEX circlexo_sessions_user ON circlexo_sessions (user_id, issued_at) WHERE revoked_at IS NOT NULL;

-- Webhook ids already processed (Standard Webhooks retries), shared by every API replica.
CREATE TABLE circlexo_webhook_events (
    webhook_id  text        PRIMARY KEY,
    received_at timestamptz NOT NULL DEFAULT now()
);
