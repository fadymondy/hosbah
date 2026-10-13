-- Hosbah: which alerts a user wants pushed to their phones and desktops. A missing row means every
-- event is on; muted_events lists the ones switched off (internal/push.Events).
CREATE TABLE notification_prefs (
    user_id text PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    muted_events text[] NOT NULL DEFAULT '{}',
    updated_at timestamptz NOT NULL DEFAULT now()
);
