-- Hand-written (not togo-generated: a server-scoped credential, like tunnels and proxy hosts).
-- PostgreSQL schema fragment; desired state for atlas migrate diff.
CREATE TABLE sftp_users (
    id text PRIMARY KEY DEFAULT gen_random_uuid()::text,
    team_id text NOT NULL,
    server_id text NOT NULL,
    username text NOT NULL,
    public_keys text NOT NULL DEFAULT '',
    password_sealed text,
    root_dir text NOT NULL,
    read_only boolean NOT NULL DEFAULT false,
    status text NOT NULL DEFAULT 'pending',
    error text,
    created_by text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Hosbah: one system user per row, chrooted (OpenSSH internal-sftp, ForceCommand + ChrootDirectory)
-- into root_dir on its server — never a shell, never any other command. public_keys is one or more
-- OpenSSH public keys, one per line (authorized_keys verbatim); password_sealed, when set, is the
-- user's login password sealed with the team DEK (context sftp:<id>), re-sealed to the target
-- agent's own key inside the signed sftp.user operation — this column never holds plaintext, and a
-- row must have at least one of the two (checked in code: an empty user nobody can reach is not a
-- state Hosbah creates, but is not worth a CHECK that would fight a key-only user losing its last key).
ALTER TABLE sftp_users ADD CONSTRAINT sftp_users_username_check CHECK (username ~ '^[a-z_][a-z0-9_-]{0,31}$');
ALTER TABLE sftp_users ADD CONSTRAINT sftp_users_root_dir_check CHECK (root_dir ~ '^/');
ALTER TABLE sftp_users ADD CONSTRAINT sftp_users_password_check CHECK (password_sealed IS NULL OR password_sealed LIKE 'csv1:%');
ALTER TABLE sftp_users ADD CONSTRAINT sftp_users_status_check CHECK (status IN ('pending', 'active', 'failed', 'removing'));
CREATE UNIQUE INDEX sftp_users_server_username_key ON sftp_users (server_id, username);
CREATE INDEX sftp_users_team_idx ON sftp_users (team_id);
