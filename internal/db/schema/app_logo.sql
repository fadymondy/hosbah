-- Hosbah: an app's uploaded logo (hand-written: togo make:resource has no bytea field type).
-- One image per app, at most 512 KiB of PNG, JPEG or WebP (checked by the API, which sniffs the
-- bytes). etag is the hex SHA-256 of data; the API serves the image at a URL versioned by it.
-- The row goes with its app (foreign key in zz_constraints.sql).
CREATE TABLE app_logos (
    app_id text PRIMARY KEY,
    team_id text NOT NULL,
    content_type text NOT NULL,
    data bytea NOT NULL,
    etag text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE app_logos ADD CONSTRAINT app_logos_content_type_check CHECK (content_type IN ('image/png', 'image/jpeg', 'image/webp'));
ALTER TABLE app_logos ADD CONSTRAINT app_logos_size_check CHECK (octet_length(data) BETWEEN 1 AND 524288);
CREATE INDEX app_logos_team_idx ON app_logos (team_id);
