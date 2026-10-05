-- Hand-written (MH-1141): a panel account's session on one server's white-label panel.
-- Platform data like users (no team_id, no row-level security): only internal/api/panel.go
-- reads it, by the SHA-256 of the opaque cookie token. Deleting the account or the server
-- ends its sessions.
CREATE TABLE panel_sessions (
    token_hash text PRIMARY KEY,
    user_id text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    server_id text NOT NULL REFERENCES servers (id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX panel_sessions_user_idx ON panel_sessions (user_id);
CREATE INDEX users_panel_server_idx ON users (panel_server_id) WHERE panel_server_id IS NOT NULL;
