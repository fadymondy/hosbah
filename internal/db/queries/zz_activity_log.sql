-- Hosbah audit queries over togo audit's activity_log (+ team_id). Run inside tenancy.Tx.
-- name: InsertActivity :exec
INSERT INTO activity_log (id, action, subject_type, subject_id, causer_id, changes, ip, user_agent, created_at, team_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: ListTeamActivity :many
-- created_at is stored as ISO-8601 text (the togo audit plugin's own shape), so since/until are
-- text too and compare lexicographically, which sorts correctly for that format.
SELECT a.id, a.action, a.subject_type, a.subject_id, a.causer_id, a.changes, a.ip, a.created_at,
       coalesce(u.email, '')::text AS causer_email
FROM activity_log a
LEFT JOIN users u ON u.id = a.causer_id
WHERE a.team_id = sqlc.arg('team_id')::text
  AND (sqlc.arg('actor')::text = '' OR a.causer_id = sqlc.arg('actor')::text OR coalesce(u.email, '') ILIKE '%' || sqlc.arg('actor')::text || '%')
  AND (sqlc.arg('action')::text = '' OR a.action LIKE sqlc.arg('action')::text || '%')
  AND (sqlc.arg('subject_type')::text = '' OR a.subject_type = sqlc.arg('subject_type')::text)
  AND (sqlc.arg('subject_id')::text = '' OR a.subject_id = sqlc.arg('subject_id')::text)
  AND (sqlc.arg('since')::text = '' OR a.created_at >= sqlc.arg('since')::text)
  AND (sqlc.arg('until')::text = '' OR a.created_at <= sqlc.arg('until')::text)
  AND (sqlc.arg('q')::text = '' OR a.action ILIKE '%' || sqlc.arg('q')::text || '%' OR a.subject_type ILIKE '%' || sqlc.arg('q')::text || '%'
       OR a.subject_id ILIKE '%' || sqlc.arg('q')::text || '%' OR a.changes ILIKE '%' || sqlc.arg('q')::text || '%'
       OR coalesce(u.email, '') ILIKE '%' || sqlc.arg('q')::text || '%')
ORDER BY a.created_at DESC, a.id
LIMIT sqlc.arg('lim')::int OFFSET sqlc.arg('off')::int;

-- name: CountTeamActivity :one
SELECT count(*) FROM activity_log a
LEFT JOIN users u ON u.id = a.causer_id
WHERE a.team_id = sqlc.arg('team_id')::text
  AND (sqlc.arg('actor')::text = '' OR a.causer_id = sqlc.arg('actor')::text OR coalesce(u.email, '') ILIKE '%' || sqlc.arg('actor')::text || '%')
  AND (sqlc.arg('action')::text = '' OR a.action LIKE sqlc.arg('action')::text || '%')
  AND (sqlc.arg('subject_type')::text = '' OR a.subject_type = sqlc.arg('subject_type')::text)
  AND (sqlc.arg('subject_id')::text = '' OR a.subject_id = sqlc.arg('subject_id')::text)
  AND (sqlc.arg('since')::text = '' OR a.created_at >= sqlc.arg('since')::text)
  AND (sqlc.arg('until')::text = '' OR a.created_at <= sqlc.arg('until')::text)
  AND (sqlc.arg('q')::text = '' OR a.action ILIKE '%' || sqlc.arg('q')::text || '%' OR a.subject_type ILIKE '%' || sqlc.arg('q')::text || '%'
       OR a.subject_id ILIKE '%' || sqlc.arg('q')::text || '%' OR a.changes ILIKE '%' || sqlc.arg('q')::text || '%'
       OR coalesce(u.email, '') ILIKE '%' || sqlc.arg('q')::text || '%');
