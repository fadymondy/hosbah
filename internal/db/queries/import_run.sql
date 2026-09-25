-- Hand-written query fragment (MH-574 A4) — safe to edit.

-- name: UpsertImportRun :one
INSERT INTO import_runs (team_id, server_id, status, error, discovered, created, updated, missing, removed, ignored, started_at, finished_at)
VALUES (sqlc.arg('team_id'), sqlc.arg('server_id'), sqlc.arg('status'), sqlc.arg('error'), sqlc.arg('discovered'), sqlc.arg('created'),
        sqlc.arg('updated'), sqlc.arg('missing'), sqlc.arg('removed'), sqlc.arg('ignored'), sqlc.arg('started_at'), sqlc.arg('finished_at'))
ON CONFLICT (server_id) DO UPDATE SET
  status = excluded.status, error = excluded.error, discovered = excluded.discovered, created = excluded.created,
  updated = excluded.updated, missing = excluded.missing, removed = excluded.removed, ignored = excluded.ignored,
  started_at = excluded.started_at, finished_at = excluded.finished_at, updated_at = now()
RETURNING *;

-- name: ImportRunOfServer :one
SELECT * FROM import_runs WHERE server_id = $1 AND team_id = $2;

-- name: DeleteImportItemsOfServer :exec
DELETE FROM import_items WHERE server_id = $1 AND team_id = $2;

-- name: InsertImportItem :one
INSERT INTO import_items (team_id, server_id, run_id, origin_kind, origin_key, name, detected_kind, target, outcome, reason, error, record_id)
VALUES (sqlc.arg('team_id'), sqlc.arg('server_id'), sqlc.arg('run_id'), sqlc.arg('origin_kind'), sqlc.arg('origin_key'), sqlc.arg('name'),
        sqlc.arg('detected_kind'), sqlc.arg('target'), sqlc.arg('outcome'), sqlc.arg('reason'), sqlc.arg('error'), sqlc.arg('record_id'))
RETURNING *;

-- name: ImportItemsOfServer :many
SELECT * FROM import_items WHERE server_id = $1 AND team_id = $2 ORDER BY name;
