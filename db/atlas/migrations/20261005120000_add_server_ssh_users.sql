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
ALTER TABLE server_ssh_users ADD CONSTRAINT server_ssh_users_server_fk FOREIGN KEY (server_id, team_id) REFERENCES servers (id, team_id) ON DELETE CASCADE;

-- server_ssh_users: members read and write in plain team scope (the handlers gate on the role).
ALTER TABLE server_ssh_users ENABLE ROW LEVEL SECURITY;
ALTER TABLE server_ssh_users FORCE ROW LEVEL SECURITY;
CREATE POLICY server_ssh_users_team_all ON server_ssh_users FOR ALL
  USING (team_id = nullif(current_setting('app.team_id', true), ''))
  WITH CHECK (team_id = nullif(current_setting('app.team_id', true), ''));
