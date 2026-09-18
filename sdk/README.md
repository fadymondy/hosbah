# Cloudy SDKs

Every SDK and the CLI talk to a team's MCP endpoint (`POST /api/mcp`, JSON-RPC `tools/call`) with a
team token (`cld_…`, minted under **MCP tokens**). So they get every Cloudy feature, including new ones,
through a generic `call(tool, args)`. The token's scopes, the audit log and the team's
destructive-action approvals apply exactly as they do for AI agents.

| Language | Path | Install | Tests |
|---|---|---|---|
| Go | `pkg/cloudy` | `go get github.com/fadymondy/cloudy/pkg/cloudy` | `go test ./pkg/cloudy` |
| Node / TypeScript | `sdk/node` | `npm i @cloudy/sdk` | `cd sdk/node && npm test` |
| Python | `sdk/python` | `pip install cloudy-sdk` | `python -m unittest discover tests` |
| PHP / Laravel | `sdk/php` | `composer require fadymondy/cloudy-sdk` | `php tests/sdk_test.php` |
| CLI (Windows, macOS, Linux) | `cmd/cloudy` | `make cli` → `bin/cli/cloudy-<os>-<arch>` | `go test ./cmd/cloudy` |

Typed helpers exist for the common calls (deploy, env, domains, proxy hosts, connecting a server);
`tools()` / `cloudy tools` lists everything else.

**Mobile / first-party clients** (React Native and similar) use the REST API with a user session instead:
first `GET /api/auth/csrf` (it sets the `togo_csrf` cookie), then `POST /api/auth/login` (or `/register`) with that value in
`X-CSRF-Token`. The response carries `{"token": …}`. Send it as `Authorization: Bearer …` on the `/api/teams/{team}/…` routes
documented at `/docs`; Bearer requests need no CSRF token. (Checked end to end on 2026-09-19.)
