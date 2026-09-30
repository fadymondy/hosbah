# CircleXO hub integration

Hosbah can run as an app on the CircleXO hub: hub sign-in next to Hosbah's own login, hub orgs
mapped to teams, hub-governed plan limits for those teams, and hub-issued tokens on the team MCP
endpoint. **All of it is off unless `CIRCLEXO_ISSUER` is set.** With it unset nothing changes: the
routes below (except the config probe) do not exist, no `circlexo_*` table is read and the quota
overlay is not installed.

Code: `internal/circlexo` (service, SSO, sync, webhooks, entitlements overlay), `internal/mcp/hub.go`
(federated MCP), the overlay hook in `internal/quota`, wiring in `internal/server/circlexo.go`.
Tests: `internal/server/circlexo_test.go`, against the SDK's `circlexotest` fake hub.

## Configuration

Names only; real values live in the environment or the vault, never in the repo. See `.env.example`.

| Variable | Purpose |
| --- | --- |
| `CIRCLEXO_ISSUER` | The hub's base URL. Turns the integration on. |
| `CIRCLEXO_API_URL` | The hub's app API, when not the issuer. |
| `CIRCLEXO_APP_ID` | Hosbah's app id at the hub (also the token audience). |
| `CIRCLEXO_CLIENT_ID` | OIDC client id. |
| `CIRCLEXO_CLIENT_SECRET` or `CIRCLEXO_PRIVATE_KEY` + `CIRCLEXO_KEY_ID` | Client authentication (one of the two). |
| `CIRCLEXO_APP_KEY` | App API key: entitlements, tenants, org members. |
| `CIRCLEXO_WEBHOOK_SECRET` | Signing secret (`whsec_...`) for webhooks and provision/deprovision calls. Required. |
| `CIRCLEXO_SESSION_KEY` | Seals the sign-in state cookie. Empty: derived from `HOSBAH_KEK`. |
| `CIRCLEXO_REDIRECT_URL` | Optional. Default `HOSBAH_WEB_ORIGIN` + `/auth/circlexo/callback`. |

A half-set or invalid configuration is logged at error level at boot and leaves the integration
disabled; Hosbah still starts.

## Endpoints

| Route | Auth | Purpose |
| --- | --- | --- |
| `GET /auth/circlexo/login` | public | Start sign-in (OIDC authorization code + PKCE). `?return_to=/local/path`. |
| `GET /auth/circlexo/callback` | public | Finish sign-in. Failures go to `/login?circlexo_error=<code>`. |
| `GET /auth/circlexo/logout` | public | Clear Hosbah's session cookie, then the hub's end-session. |
| `GET /sso/circlexo?org=<slug>` | public | The app launch URL the hub dashboard links to. |
| `POST /api/circlexo/events` | webhook signature | Hub webhooks (below). |
| `POST /api/circlexo/provision` | webhook signature | Create or return the team for an org (idempotent). Answers `{product_tenant_id, slug}`. |
| `POST /api/circlexo/deprovision` | webhook signature | Mark the org's link inactive. |
| `GET /api/circlexo/config` | public | `{"enabled": bool}`: the login page shows the button only when true. Always mounted. |
| `POST /api/mcp` | token | Existing; also accepts hub-issued access tokens. |

`web/next.config.mjs` rewrites `/auth/circlexo/*` and `/sso/circlexo` to the API, because the hub
redirects the browser to the web origin.

Webhooks handled: `org.app_installed`, `org.app_removed`, `member.added`, `member.role_changed`,
`member.removed`, `session.revoked`, `entitlement.changed`, and any `subscription.*` (drops the
entitlement cache; `subscription.*` is not in the hub's event catalogue yet). Signatures are
verified (`webhook-id`, `webhook-timestamp`, `webhook-signature`), replays are dropped by id, and a
bad or missing signature is a 401 before anything runs.

## Migration and tables

`db/atlas/migrations/20260930100000_add_circlexo.sql` (desired state in
`internal/db/schema/zz_circlexo.sql`). Platform tables: no RLS, they hold no tenant data.

- `circlexo_user_links`: hub subject to Hosbah user. Unique both ways.
- `circlexo_org_links`: hub org to team, with `status` `active` or `inactive`, plus the team slug and locale (FORCE RLS on `teams` blocks a raw read, so they are kept here). Unique both ways.
- `circlexo_sessions`: hub session id, user and JWT `iat`, for `session.revoked`.
- `circlexo_webhook_events`: processed event ids.

## Mapping rules

**Sign-in.** In order: a linked subject signs in as its user; otherwise the hub must vouch for the
email (`email_verified`), or it is refused; a Hosbah account with that email is linked only if
Hosbah itself verified the address, otherwise refused (`account_conflict`), so nobody can register
a stranger's address and inherit their hub sign-in; otherwise a passwordless, verified account is
created and linked. Disabled accounts are refused. The session is Hosbah's normal one, so every
existing login method keeps working (dual login).

**Landing.** After sign-in the user lands in `/<locale>/t/<teamId>` of the org in the token (or the
org named by the launch URL), added with their hub role. If the hub confirms the org has the app but
the install webhook has not arrived, the team is provisioned on the spot.

**Org to team.** One active team per org. Provision is idempotent: the same org returns the same
team, reactivating an inactive link. It reads the org and members from the hub, creates the team
(own DEK, owner membership, audit entry) and syncs members, then confirms the tenant to the hub.

**Roles.** Hub `owner` maps to `owner`, `admin` to `admin`, `billing` to `viewer`, anything else
to `deployer`. The last owner is never demoted or removed by the hub. `member.removed` deletes the
membership (Hosbah has no suspended membership state). Removing the app marks the link inactive and
deletes nothing: the team, members and data stay and its limits go back to what a platform admin
set. Hub-side member sync is not gated by the member cap; only inviting and accepting are.

## Entitlements

For a team with an **active** link, `servers`, `apps`, `data_services` and `members` (counted
limits, always hard caps: Hosbah has no overage billing) come from the hub through the SDK cache.
An absent or disabled feature is a limit of 0; a feature with no limit is unlimited. Fail closed
with a read-only grace: if the hub is unreachable, the last known answer is used for the SDK's grace
window with adds refused (402, reads and deletes still work); past the window, or when the hub has
no entitlements for the org, the limits are 0 and the team is read-only. `entitlement.changed` and
`subscription.*` drop the cache. Unlinked teams are never touched. `quota.Quota` carries `Hub`,
`ReadOnly` and `MaxMembers` so the API and the UI can say why something was refused.

Usage reporting is not wired: Hosbah meters nothing pay-as-you-go.

## Federated MCP

`/api/mcp` verifies a hub token against the hub's JWKS with audience = the app id, then maps the
subject through `circlexo_user_links` and the org through `circlexo_org_links` (active only), and
re-reads the team role on every call. An unlinked user, an unknown or deinstalled org, a non-member,
a token with no org, a disabled user, a suspended team or a service token is refused (403 with a
reason, or 401 for a token that is not valid). `hbu_`/`cld_` tokens are unchanged.

Scopes: a token that names Hosbah scopes (`read`, `write`, ...) gets exactly those; a token carrying
an `act` chain (exchanged by the hub's gateway for an agent) gets `read`, `write` and `deploy`;
any other hub token gets `read`. Hub tokens never get `secrets:read` or `destructive`: revealing
secrets and deleting stay with a person's own `hbu_` token. Everything is narrowed by the user's
current team role. The `act` chain is recorded on audit entries as `via_actors`.

## Differences from the draft manifest (`manifests/drafts/hosbah/circlexo.app.yaml`)

- Post-logout URI: Hosbah returns to `<web origin>/login`, the draft lists `https://app.hosbah.com/`. Register `/login` (or change the code).
- `subscription.*` webhooks are accepted but are not in the hub's catalogue; the manifest lists them.
- The manifest tools for deploys, secret reveals and deletes are declared destructive and need an approval; hub agent tokens are simply refused for `secrets:read` and `destructive` tools (no approval path), and `deploy` is allowed for gateway tokens. Tools that reveal secrets (`reveal_*`, minting product keys) will be refused through the hub.
- No protected-resource metadata document is served by Hosbah for the hub (`/.well-known/oauth-protected-resource` does not exist here).
- No usage reporter (nothing is metered), and no billing page link: billing lives entirely at the hub.
- Entitlement feature keys match the draft: `servers`, `apps`, `data_services`, `members`. `members` is new for the hub and is enforced when inviting or accepting an invitation.

## Known limits

- Hosbah's 2FA is not asked for on hub sign-in: the hub is the identity provider and owns second factors.
- Hub sign-in is not gated by Hosbah's sign-up mode (closed or invite-only); the hub decides who gets in, and joining an org's team needs the org to be linked.
- Hosbah's session is a stateless JWT, so logout ends it in this browser only. `session.revoked` works by remembering (user, `iat`) of hub-born sessions; two sessions of one user minted in the same second share an `iat`, so revoking one ends both. Password sessions are unaffected.
- The provision and deprovision request shape was designed here; the webhooks are the contractual path.

## SDK dependency

`github.com/circlexo/circlexo-go` is a public module pinned at `v0.1.1` (v0.1.0 requested offline_access, which the hub refuses; the SDK default scopes are now openid profile email org entitlements, and Hosbah sets none itself) in `go.mod` (no `replace`).

## Owner cut-over checklist

1. Register Hosbah at the hub; note the app id, client credentials, app key and webhook secret.
2. Register the redirect URI (`<web origin>/auth/circlexo/callback`), the post-logout URI (`<web origin>/login`), the launch URL (`<web origin>/sso/circlexo`), the webhook URL (`<API>/api/circlexo/events`) and the provision/deprovision URLs.
3. Set the `CIRCLEXO_*` variables in the environment (not the repo).
4. Run the migration (`togo migrate` / `go run ./cmd/migrate`): it creates the four tables.
5. Set `CIRCLEXO_ISSUER` last and restart: the login button appears and the routes mount. Look for "circlexo: hub integration enabled" in the boot log.
6. Install the app on a test org and confirm: a team appears, members sync, the hub shows the tenant active, and the limits follow the hub plan.
