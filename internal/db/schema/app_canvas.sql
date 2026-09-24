-- Hosbah: where the boxes of a site's overview canvas sit (hand-written: one row per app, keyed by
-- the app, with a JSON document — not a CRUD resource). layout is
-- {"nodes": {"<node key>": {"x": 1, "y": 2}}, "viewport": {"x": 0, "y": 0, "zoom": 1}}; node keys are
-- stable ("app", "domain:<hostname>", "db:<service id>", …) so positions survive reloads. The API
-- validates its shape and caps it at 64 KiB; the check below is the backstop. Cosmetic: nothing reads
-- it but the web app. The row goes with its app (foreign key in zz_constraints.sql).
CREATE TABLE app_canvas (
    app_id text PRIMARY KEY,
    team_id text NOT NULL,
    layout jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE app_canvas ADD CONSTRAINT app_canvas_layout_check CHECK (jsonb_typeof(layout) = 'object' AND octet_length(layout::text) <= 65536);
CREATE INDEX app_canvas_team_idx ON app_canvas (team_id);
