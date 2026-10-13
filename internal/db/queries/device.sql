-- Hosbah queries: signed-in apps (internal/api/devices.go, internal/account/devices.go, internal/push).

-- name: InsertDevice :one
INSERT INTO devices (user_id, name, platform, app, locale, app_version, refresh_hash)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListDevicesOfUser :many
SELECT * FROM devices WHERE user_id = $1 ORDER BY last_seen_at DESC;

-- name: DeleteDeviceOfUser :execrows
DELETE FROM devices WHERE id = $1 AND user_id = $2;

-- name: SetDevicePushToken :execrows
-- A token moves with its install: ClearPushTokenElsewhere first (the unique index), then this.
UPDATE devices SET push_token = sqlc.narg('push_token'), locale = coalesce(sqlc.narg('locale'), locale),
    app_version = coalesce(sqlc.narg('app_version'), app_version), last_seen_at = now()
WHERE id = sqlc.arg('id') AND user_id = sqlc.arg('user_id');

-- name: ClearPushTokenElsewhere :exec
UPDATE devices SET push_token = NULL WHERE push_token = sqlc.arg('push_token') AND id <> sqlc.arg('id');

-- name: DeviceByRefreshHash :one
-- System scope: the refresh call is not signed in yet.
SELECT d.*, u.disabled_at, u.sessions_revoked_at, u.email_unverified_at, u.panel_server_id,
       u.email, u.roles, u.permissions
FROM devices d JOIN users u ON u.id = d.user_id
WHERE d.refresh_hash = $1;

-- name: RotateDeviceRefresh :execrows
-- Compare-and-swap on the old hash, so two refreshes racing with the same token cannot both win.
UPDATE devices SET refresh_hash = sqlc.arg('new_hash'), refreshed_at = now(), last_seen_at = now()
WHERE id = sqlc.arg('id') AND refresh_hash = sqlc.arg('old_hash');

-- name: DeleteDevice :exec
DELETE FROM devices WHERE id = $1;

-- name: PushTargetsOfTeam :many
-- System scope (internal/push): every install of the team's members that can take a push and
-- has not muted this event. Suspended teams and disabled accounts get nothing.
SELECT d.id, d.user_id, d.push_token::text AS push_token, d.locale
FROM team_members tm
JOIN teams t ON t.id = tm.team_id AND t.suspended_at IS NULL
JOIN users u ON u.id = tm.user_id AND u.disabled_at IS NULL
JOIN devices d ON d.user_id = tm.user_id AND d.push_token IS NOT NULL
LEFT JOIN notification_prefs p ON p.user_id = tm.user_id
WHERE tm.team_id = sqlc.arg('team_id')
  AND NOT (sqlc.arg('kind')::text = ANY (coalesce(p.muted_events, '{}'::text[])));

-- name: PushTargetsOfDevice :many
SELECT d.id, d.user_id, d.push_token::text AS push_token, d.locale
FROM devices d WHERE d.id = $1 AND d.user_id = $2 AND d.push_token IS NOT NULL;

-- name: DropPushToken :exec
UPDATE devices SET push_token = NULL WHERE push_token = $1;

-- name: NotificationPrefsOf :one
SELECT * FROM notification_prefs WHERE user_id = $1;

-- name: UpsertNotificationPrefs :one
INSERT INTO notification_prefs (user_id, muted_events, updated_at) VALUES ($1, $2, now())
ON CONFLICT (user_id) DO UPDATE SET muted_events = EXCLUDED.muted_events, updated_at = now()
RETURNING *;

-- name: AlertableServers :many
-- System scope (internal/push sweeper): enrolled servers with their last heartbeat and metrics.
SELECT id, team_id, name, metrics, last_seen_at FROM servers WHERE status = 'enrolled';
