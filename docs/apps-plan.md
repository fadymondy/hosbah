# Hosbah companion apps — plan (phase 1)

Date: 2026-10-13. Owner review required before any merge (a push to `main` auto-deploys Hosbah).

## Goal

Phone (iOS + Android, Expo) and desktop (Electron, Windows + macOS, tray) apps that make Hosbah
easy to watch and act on from anywhere. **Push alerts are the main reason the apps exist.**

## Template choice: Mahaam

I compared Mahaam (`apps/mobile`, `apps/desktop`, `apps/shared`), Zekra (`mobile/`, `desktop/`)
and Health Debug (`app/`, `desktop/`). **Mahaam is the base for both apps**:

- Mobile: most screens (45 routes), the only UI tests (jest-expo + Maestro), a small injectable
  REST client typed from the server's OpenAPI, SecureStore tokens, biometrics
  (expo-local-authentication), en/ar with live RTL, dark/light, Expo 57 / RN 0.86.
- Desktop: newest stack (Electron 44, electron-builder 26, electron-updater), Vite + React 19
  renderer with no network access (all API calls go through the main process over IPC),
  `safeStorage` token storage, a tray whose icon is redrawn per state, notarize hook.
- Health Debug has no push, no biometrics, no packaged desktop; it is not a candidate.

Taken from **Zekra** where Mahaam has nothing: Firebase messaging + Crashlytics wired only when
`google-services.json` / `GoogleService-Info.plist` exist (so builds without them still work),
and the Codemagic store workflows (`android-release` → Play **internal**, `ios-release` →
TestFlight). The Go FCM sender is ported from Mahaam's `internal/push/native.go` (FCM HTTP v1,
service-account JWT, no Firebase SDK; APNs reached through FCM).

## Layout (Mahaam's)

```
apps/
  package.json        npm workspace: shared, desktop (mobile has its own lockfile, like Mahaam)
  shared/             @hosbah/client — REST client + types, auth/session flow, i18n (en/ar),
                      status/severity rules, deep-link parser, brand tokens
  mobile/             Expo app  com.fadymondy.hosbah        scheme hosbah://
  desktop/            Electron  com.fadymondy.hosbah.desktop scheme hosbah://
codemagic.yaml        android-internal + ios-testflight only (never review/production)
```

## Auth (same as the web dashboard)

The web signs in with email + password (+ two-factor challenge) and receives a 24 h JWT; Google /
GitHub sign-in ends in the same session. Hosbah blocks togo PATs on purpose (a PAT would act across
every team), and the MCP OAuth tokens only work on `/api/mcp`. So the apps need one new, narrow
server piece: **device sessions**.

1. App signs in exactly like the web: `POST /api/auth/login` (→ `2fa_required` → `POST
   /api/auth/2fa/challenge`). It holds the 24 h JWT as a Bearer token.
2. App calls `POST /api/me/devices` (Bearer) → `{device, refresh_token: "hbr_…"}`. Only the
   SHA-256 hash is stored; the token goes to SecureStore / `safeStorage`.
3. When the JWT expires: `POST /api/auth/device/refresh {refresh_token}` → a fresh JWT + a rotated
   refresh token (90-day sliding window). Refused when the account is disabled, the device was
   removed, or `sessions_revoked_at` is newer than the device (same rule as web sessions).
4. Sign-out: `DELETE /api/me/devices/{id}` — drops the refresh token and the push token together.
5. Account → Devices (`GET /api/me/devices`) lists them; removing one signs that device out.

Nothing about the web session changes.

## Server: push + preferences (PR 1)

- `devices` table (per user, RLS on `app.user_id`; system read for the sender): name, platform
  (ios|android|windows|macos|linux), app (mobile|desktop), refresh hash, push token (unique),
  locale, app version, last seen.
- `PUT /api/me/devices/{device}/push {token}` registers an FCM token; `POST
  /api/me/devices/{device}/test` sends a test push.
- `notification_prefs` (per user): `GET/PUT /api/me/notification-prefs` — per event on/off.
- `internal/push`: FCM HTTP v1 sender (`HOSBAH_FCM_PROJECT_ID`,
  `HOSBAH_FCM_SERVICE_ACCOUNT_FILE`; unset → disabled, logged once) and `Notify(team, alert)`:
  team members → minus muted events → their devices' push tokens → send; dead tokens are cleared.
- Events and where they fire:
  | event | source |
  |---|---|
  | `deploy.succeeded` / `deploy.failed` | `internal/deploy` (where the in-app row is written) |
  | `app.down` / `app.up` | `internal/monitors` opened / resolved |
  | `resource.high` | new sweeper on server metrics (CPU / RAM / disk ≥ 90 %, 1 h cooldown) |
  | `server.offline` | same sweeper (agent silent past the online window) |
  | `cert.expiring` | `internal/certcheck` (14 / 7 / 1 days) — now also an in-app row |
  | `backup.failed` | `internal/dbsvc` backup result — now also an in-app row + channels |
- Payload `data`: `{team, kind, subject_type, subject_id, url}`; `url` is the deep link
  (`hosbah://t/<team>/apps/<id>` …) the apps open on tap.

## Screens (both apps)

Overview (servers, online, CPU/RAM/disk) · Apps (status, last deploy, logs) · App detail with
Redeploy / Restart / Start / Stop (confirm dialog for each; phone adds biometric confirmation;
needs the `deploy` role) · Domains + SSL expiry · Backups status · Alerts (in-app notifications,
mark read) · Settings (team, push per event, language AR/EN + RTL, theme light/dark/system,
devices, sign out). Arabic-first: default locale `ar` unless the phone/OS is English.

Desktop: native notifications from the team's `notifications/stream` (SSE) and a tray icon
coloured green / amber / red (all good / something degraded or warnings / something down or
failed), with a menu of the latest alerts.

## Brand

`web/app/grid/brand-cloudy.css`: brand `#2e6f9e` (dark `#7fb0d6`), gold accent `#c9a227`, IBM Plex Sans Arabic
for Arabic, Inter for English, the three stacked layers mark. Name: **حوسبة** / Hosbah.

## Build / CI

- Codemagic: `hosbah-android-internal` (AAB → Play **internal** track, draft) and
  `hosbah-ios-testflight` (TestFlight only). Never `review` / `production`. Env groups
  `hosbah_app_env`, `hosbah_android_keystore`, `hosbah_google_play`; Firebase files from base64
  env vars `GOOGLE_SERVICES_JSON` / `GOOGLE_SERVICE_INFO_PLIST` (names only — values in the vault).
- electron-builder: NSIS (Windows x64) + dmg/zip (macOS universal), GitHub publish, auto-update.
- **iOS archiving and macOS signing/notarizing happen on the Mac** (`apps/desktop/RELEASING.md`).
- Bundle IDs: `com.fadymondy.hosbah` (phone), `com.fadymondy.hosbah.desktop`.

## PRs (stacked, merged after review)

1. `feat/apps-push-api` → `main`: devices, refresh, prefs, push sender, event hooks, tests.
2. `feat/apps-mobile` → PR 1: `apps/shared`, `apps/mobile`, `codemagic.yaml`.
3. `feat/apps-desktop` → PR 2: `apps/desktop`.

## Testing rules

Local stack only (Postgres :55491, API :8710) with a seeded test team and **mock** data; no real
restart / stop / redeploy against production apps. Fixtures and screenshots use example data only.

## Shared with the Moharrik apps (sibling thread)

Same template (Mahaam), same layout (`apps/{shared,mobile,desktop}`), same naming
(`com.fadymondy.<product>[.desktop]`, `<product>://`), same Codemagic shape (internal/TestFlight
only) and the same Go FCM sender port. Candidates to extract later into one package: the FCM
v1 sender, the device-session refresh flow, the tray status-icon renderer and the
SecureStore/`safeStorage` session store.

## Remaining owner setup

Firebase project + `google-services.json` / `GoogleService-Info.plist` and the FCM service account
(set `HOSBAH_FCM_*` on the control plane), Play Console app + first manual AAB upload, App Store Connect
app, Codemagic env groups, Mac archive/notarize, review + merge order 1 → 2 → 3.
