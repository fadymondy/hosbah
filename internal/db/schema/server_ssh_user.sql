-- Hosbah: shell logins Hosbah created on a server for people to reach it over SSH (the SSH page of a
-- server and of a server user's panel). One row per (server_id, username). The unix user itself is
-- made by the typed sshuser.set op and carries a GECOS marker, so Hosbah only ever edits or removes
-- a login it created, never root or a user someone made by hand. status: 'ready' (the last op
-- succeeded) or 'failed' (last_error says why). Passwords are never stored: they are sealed to the
-- agent's key and shown once. Keys reuse the team SSH key library (ssh_key_installs, unix_user).
CREATE TABLE server_ssh_users (
    id text PRIMARY KEY DEFAULT gen_random_uuid()::text,
    team_id text NOT NULL,
    server_id text NOT NULL,
    username text NOT NULL,
    sudo boolean NOT NULL DEFAULT false,
    status text NOT NULL DEFAULT 'ready',
    last_error text NOT NULL DEFAULT '',
    created_by text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE server_ssh_users ADD CONSTRAINT server_ssh_users_status_check CHECK (status IN ('ready', 'failed'));
ALTER TABLE server_ssh_users ADD CONSTRAINT server_ssh_users_username_check CHECK (username ~ '^[a-z_][a-z0-9_-]{0,31}$');
CREATE UNIQUE INDEX server_ssh_users_server_username_key ON server_ssh_users (server_id, username);
CREATE INDEX server_ssh_users_team_idx ON server_ssh_users (team_id);
