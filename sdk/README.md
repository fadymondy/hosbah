# Hosbah SDKs

Every SDK and the CLI talk to a team's MCP endpoint (`POST /api/mcp`, JSON-RPC `tools/call`) with a
team token (`hbu_…`, minted under **MCP tokens**; `cld_…` tokens minted before the rename keep working). So they get every Hosbah feature, including new ones,
through a generic `call(tool, args)`. The token's scopes, the audit log and the team's
destructive-action approvals apply exactly as they do for AI agents.

| Language | Repository | Install | Tests |
|---|---|---|---|
| Go | [fadymondy/hosbah-go](https://github.com/fadymondy/hosbah-go) (source: `pkg/hosbah`) | `go get github.com/fadymondy/hosbah-go` | `go test ./pkg/hosbah` |
| Node / TypeScript | [fadymondy/hosbah-node](https://github.com/fadymondy/hosbah-node) (source: `sdk/node`) | `npm i github:fadymondy/hosbah-node` | `cd sdk/node && npm test` |
| Python | [fadymondy/hosbah-python](https://github.com/fadymondy/hosbah-python) (source: `sdk/python`) | `pip install git+https://github.com/fadymondy/hosbah-python` | `python -m unittest discover tests` |
| PHP / Laravel | [fadymondy/hosbah-php](https://github.com/fadymondy/hosbah-php) (source: `sdk/php`) | `composer config repositories.hosbah vcs https://github.com/fadymondy/hosbah-php && composer require fadymondy/hosbah-sdk:dev-main` | `php tests/sdk_test.php` |
| CLI (Windows, macOS, Linux) | this repository, `cmd/hosbah` | `make cli` → `bin/cli/hosbah-<os>-<arch>` | `go test ./cmd/hosbah` |

The code lives here; each SDK is published to its own public repository with `scripts/publish-sdks.sh` (run it after changing an SDK). Not yet on npm, PyPI or Packagist: install from the repositories as above.

Typed helpers exist for the common calls (deploy, env, domains, proxy hosts, connecting a server);
`tools()` / `hosbah tools` lists everything else.

**Mobile / first-party clients** (React Native and similar) use the REST API with a user session instead:
first `GET /api/auth/csrf` (it sets the `togo_csrf` cookie), then `POST /api/auth/login` (or `/register`) with that value in
`X-CSRF-Token`. The response carries `{"token": …}`. Send it as `Authorization: Bearer …` on the `/api/teams/{team}/…` routes
documented at `/docs`; Bearer requests need no CSRF token. (Checked end to end on 2026-09-19.)
