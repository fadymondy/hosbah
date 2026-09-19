-- Hosbah platform administration (internal/platform). Every query here runs in a
-- tenancy.Scope{PlatformAdmin: true} transaction unless its comment says otherwise; the
-- *_platform_admin_select policies are what let it read across teams. No query here ever
-- selects operations.params, sealed columns or token hashes.

-- name: PlatformSettings :one
-- Any context may read this (platform_settings_select is USING (true)).
SELECT * FROM platform_settings WHERE id = true;

-- name: UpsertPlatformSettings :one
INSERT INTO platform_settings (id, signup_mode, announcement, announcement_level, support_email,
    max_teams_per_user, max_servers_per_team, updated_by, updated_at)
VALUES (true, sqlc.arg('signup_mode'), sqlc.arg('announcement'), sqlc.arg('announcement_level'), sqlc.arg('support_email'),
    sqlc.arg('max_teams_per_user'), sqlc.arg('max_servers_per_team'), sqlc.arg('updated_by'), now())
ON CONFLICT (id) DO UPDATE SET
    signup_mode = EXCLUDED.signup_mode, announcement = EXCLUDED.announcement,
    announcement_level = EXCLUDED.announcement_level, support_email = EXCLUDED.support_email,
    max_teams_per_user = EXCLUDED.max_teams_per_user, max_servers_per_team = EXCLUDED.max_servers_per_team,
    updated_by = EXCLUDED.updated_by, updated_at = now()
RETURNING *;

-- name: InsertPlatformAudit :exec
INSERT INTO platform_audit (actor_id, action, target_type, target_id, details, ip)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: ListPlatformAudit :many
SELECT a.id, a.actor_id, coalesce(u.email, '')::text AS actor_email, a.action, a.target_type, a.target_id,
    a.details, a.ip, a.created_at
FROM platform_audit a
LEFT JOIN users u ON u.id = a.actor_id
ORDER BY a.created_at DESC, a.id
LIMIT sqlc.arg('lim')::int OFFSET sqlc.arg('off')::int;

-- name: CountPlatformAudit :one
SELECT count(*) FROM platform_audit;

-- name: AdminOverviewCounts :one
SELECT
    (SELECT count(*) FROM users)::bigint AS users,
    (SELECT count(*) FROM users WHERE roles ~ '(^|,)\s*admin\s*(,|$)')::bigint AS platform_admins,
    (SELECT count(*) FROM teams)::bigint AS teams,
    (SELECT count(*) FROM servers)::bigint AS servers_total,
    (SELECT count(*) FROM servers WHERE status = 'enrolled' AND last_seen_at > now() - make_interval(secs => sqlc.arg('online_seconds')::int))::bigint AS servers_online,
    (SELECT count(*) FROM apps)::bigint AS apps,
    (SELECT count(*) FROM deployments WHERE created_at > now() - interval '24 hours')::bigint AS deployments_24h,
    (SELECT count(*) FROM deployments WHERE created_at > now() - interval '24 hours' AND status = 'failed')::bigint AS deployments_failed_24h,
    (SELECT count(*) FROM operations WHERE created_at > now() - interval '24 hours')::bigint AS operations_24h,
    (SELECT count(*) FROM operations WHERE created_at > now() - interval '24 hours' AND status IN ('failed', 'timeout'))::bigint AS operations_failed_24h,
    (SELECT count(*) FROM data_services)::bigint AS databases;

-- name: RecentSignups :many
SELECT id, email, created_at FROM users ORDER BY created_at DESC, id LIMIT 5;

-- name: AdminListUsers :many
SELECT u.id, u.email, u.roles, u.created_at, u.disabled_at,
    (SELECT count(*) FROM team_members m WHERE m.user_id = u.id)::bigint AS team_count
FROM users u
WHERE (sqlc.arg('q')::text = '' OR u.email ILIKE '%' || sqlc.arg('q')::text || '%')
ORDER BY u.created_at DESC, u.id
LIMIT sqlc.arg('lim')::int OFFSET sqlc.arg('off')::int;

-- name: AdminCountUsers :one
SELECT count(*) FROM users u
WHERE (sqlc.arg('q')::text = '' OR u.email ILIKE '%' || sqlc.arg('q')::text || '%');

-- name: AdminUserTokenCounts :one
SELECT
    (SELECT count(*) FROM mcp_tokens t WHERE t.user_id = sqlc.arg('user_id')::text AND t.revoked_at IS NULL AND (t.expires_at IS NULL OR t.expires_at > now()))::bigint AS mcp_tokens,
    (SELECT count(*) FROM admin_mcp_tokens t WHERE t.user_id = sqlc.arg('user_id')::text AND t.revoked_at IS NULL AND (t.expires_at IS NULL OR t.expires_at > now()))::bigint AS admin_mcp_tokens;

-- name: SetUserDisabled :exec
UPDATE users SET disabled_at = CASE WHEN sqlc.arg('disabled')::boolean THEN coalesce(disabled_at, now()) ELSE NULL END,
    sessions_revoked_at = CASE WHEN sqlc.arg('disabled')::boolean THEN now() ELSE sessions_revoked_at END
WHERE id = sqlc.arg('id')::text;

-- name: LockPlatformAdmins :many
-- Locks every admin row so two concurrent demotions cannot both see "another admin remains".
SELECT id, disabled_at FROM users WHERE roles ~ '(^|,)\s*admin\s*(,|$)' ORDER BY id FOR UPDATE;

-- name: UserAccountState :one
-- Runs outside any tenant scope (users has no RLS): the per-request "is this account still
-- allowed in" check behind every session.
SELECT disabled_at, sessions_revoked_at FROM users WHERE id = $1;

-- name: SoleOwnedTeams :many
-- Teams where this user is an owner and no other member is.
SELECT t.id, t.slug, t.name
FROM teams t
JOIN team_members m ON m.team_id = t.id AND m.user_id = sqlc.arg('user_id')::text AND m.role = 'owner'
WHERE NOT EXISTS (SELECT 1 FROM team_members o WHERE o.team_id = t.id AND o.role = 'owner' AND o.user_id <> sqlc.arg('user_id')::text)
ORDER BY t.created_at;

-- name: ReassignTeamOwnership :exec
-- teams.owner_id of the teams this user created moves to another owner of the same team.
UPDATE teams t SET owner_id = (
    SELECT o.user_id FROM team_members o WHERE o.team_id = t.id AND o.role = 'owner' AND o.user_id <> sqlc.arg('user_id')::text
    ORDER BY o.created_at LIMIT 1), updated_at = now()
WHERE t.owner_id = sqlc.arg('user_id')::text
  AND EXISTS (SELECT 1 FROM team_members o WHERE o.team_id = t.id AND o.role = 'owner' AND o.user_id <> sqlc.arg('user_id')::text);

-- name: DeleteMembershipsOfUser :exec
DELETE FROM team_members WHERE user_id = $1;

-- name: DeleteMcpTokensOfUser :exec
DELETE FROM mcp_tokens WHERE user_id = $1;

-- name: DeleteAdminMcpTokensOfUser :exec
DELETE FROM admin_mcp_tokens WHERE user_id = $1;

-- name: DeleteUserAccount :execrows
DELETE FROM users WHERE id = $1;

-- name: AdminListTeams :many
SELECT t.id, t.slug, t.name, t.created_at, t.suspended_at,
    (SELECT count(*) FROM team_members m WHERE m.team_id = t.id)::bigint AS members,
    (SELECT count(*) FROM servers s WHERE s.team_id = t.id)::bigint AS servers,
    (SELECT count(*) FROM apps a WHERE a.team_id = t.id)::bigint AS apps
FROM teams t
WHERE (sqlc.arg('q')::text = '' OR t.name ILIKE '%' || sqlc.arg('q')::text || '%' OR t.slug ILIKE '%' || sqlc.arg('q')::text || '%')
ORDER BY t.created_at DESC, t.id
LIMIT sqlc.arg('lim')::int OFFSET sqlc.arg('off')::int;

-- name: AdminCountTeams :one
SELECT count(*) FROM teams t
WHERE (sqlc.arg('q')::text = '' OR t.name ILIKE '%' || sqlc.arg('q')::text || '%' OR t.slug ILIKE '%' || sqlc.arg('q')::text || '%');

-- name: AdminTeamServers :many
SELECT id, name, provider, kind, status, last_seen_at FROM servers WHERE team_id = $1 ORDER BY created_at, id;

-- name: AdminTeamApps :many
SELECT a.id, a.name, a.server_id, s.name AS server_name
FROM apps a JOIN servers s ON s.id = a.server_id
WHERE a.team_id = $1 ORDER BY a.created_at, a.id;

-- name: AdminTeamDataServiceCount :one
SELECT count(*) FROM data_services WHERE team_id = $1;

-- name: SetTeamSuspended :one
UPDATE teams SET suspended_at = CASE WHEN sqlc.arg('suspended')::boolean THEN coalesce(suspended_at, now()) ELSE NULL END,
    updated_at = now()
WHERE id = sqlc.arg('id')::text
RETURNING *;

-- name: AdminListServers :many
SELECT s.id, s.name, s.provider, s.kind, s.status, s.ip_address, s.hostname, s.agent_version, s.last_seen_at, s.created_at,
    t.id AS team_id, t.slug AS team_slug, t.name AS team_name
FROM servers s JOIN teams t ON t.id = s.team_id
WHERE (sqlc.arg('q')::text = '' OR s.name ILIKE '%' || sqlc.arg('q')::text || '%' OR coalesce(s.hostname, '') ILIKE '%' || sqlc.arg('q')::text || '%'
       OR coalesce(s.ip_address, '') ILIKE '%' || sqlc.arg('q')::text || '%' OR t.slug ILIKE '%' || sqlc.arg('q')::text || '%')
  AND (sqlc.arg('status')::text = ''
       OR (sqlc.arg('status')::text = 'online' AND s.status = 'enrolled' AND s.last_seen_at > now() - make_interval(secs => sqlc.arg('online_seconds')::int))
       OR (sqlc.arg('status')::text = 'offline' AND s.status = 'enrolled' AND (s.last_seen_at IS NULL OR s.last_seen_at <= now() - make_interval(secs => sqlc.arg('online_seconds')::int)))
       OR s.status = sqlc.arg('status')::text)
ORDER BY s.created_at DESC, s.id
LIMIT sqlc.arg('lim')::int OFFSET sqlc.arg('off')::int;

-- name: AdminCountServers :one
SELECT count(*) FROM servers s JOIN teams t ON t.id = s.team_id
WHERE (sqlc.arg('q')::text = '' OR s.name ILIKE '%' || sqlc.arg('q')::text || '%' OR coalesce(s.hostname, '') ILIKE '%' || sqlc.arg('q')::text || '%'
       OR coalesce(s.ip_address, '') ILIKE '%' || sqlc.arg('q')::text || '%' OR t.slug ILIKE '%' || sqlc.arg('q')::text || '%')
  AND (sqlc.arg('status')::text = ''
       OR (sqlc.arg('status')::text = 'online' AND s.status = 'enrolled' AND s.last_seen_at > now() - make_interval(secs => sqlc.arg('online_seconds')::int))
       OR (sqlc.arg('status')::text = 'offline' AND s.status = 'enrolled' AND (s.last_seen_at IS NULL OR s.last_seen_at <= now() - make_interval(secs => sqlc.arg('online_seconds')::int)))
       OR s.status = sqlc.arg('status')::text);

-- name: AdminListOperations :many
-- Never params (it can carry re-sealed values) and never result: only the summary columns.
SELECT o.id, o.kind, o.status, o.executor, o.exit_code, left(coalesce(o.error, ''), 300)::text AS error_summary,
    o.created_at, o.finished_at, o.server_id, s.name AS server_name, t.id AS team_id, t.slug AS team_slug, t.name AS team_name
FROM operations o
JOIN servers s ON s.id = o.server_id
JOIN teams t ON t.id = o.team_id
WHERE (sqlc.arg('status')::text = '' OR o.status = sqlc.arg('status')::text)
  AND (sqlc.arg('kind')::text = '' OR o.kind = sqlc.arg('kind')::text)
ORDER BY o.created_at DESC, o.id
LIMIT sqlc.arg('lim')::int OFFSET sqlc.arg('off')::int;

-- name: AdminCountOperations :one
SELECT count(*) FROM operations o
WHERE (sqlc.arg('status')::text = '' OR o.status = sqlc.arg('status')::text)
  AND (sqlc.arg('kind')::text = '' OR o.kind = sqlc.arg('kind')::text);

-- name: CountTeamsOwnedBy :one
-- Runs in the caller's own scope: team_members_select shows a user their own memberships.
SELECT count(*) FROM team_members WHERE user_id = $1 AND role = 'owner';


-- name: TeamSuspendedAt :one
SELECT suspended_at FROM teams WHERE id = $1;

-- name: RevokeMcpTokensOfUser :exec
UPDATE mcp_tokens SET revoked_at = now(), updated_at = now() WHERE user_id = $1 AND revoked_at IS NULL;

-- name: RevokeAdminMcpTokensOfUser :exec
UPDATE admin_mcp_tokens SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL;

-- name: TeamBySlug :one
SELECT * FROM teams WHERE slug = $1;
