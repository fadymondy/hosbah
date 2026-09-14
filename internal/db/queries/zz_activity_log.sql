-- Cloudy audit queries over togo audit's activity_log (+ team_id). Run inside tenancy.Tx.
-- name: InsertActivity :exec
INSERT INTO activity_log (id, action, subject_type, subject_id, causer_id, changes, ip, user_agent, created_at, team_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: ListTeamActivity :many
SELECT a.id, a.action, a.subject_type, a.subject_id, a.causer_id, a.changes, a.ip, a.created_at,
       coalesce(u.email, '')::text AS causer_email
FROM activity_log a
LEFT JOIN users u ON u.id = a.causer_id
WHERE a.team_id = $1
ORDER BY a.created_at DESC
LIMIT $2;
