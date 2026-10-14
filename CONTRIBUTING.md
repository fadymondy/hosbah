# Contributing to Hosbah

Thanks for wanting to help. This guide covers how to get a local setup running, the conventions
the codebase follows, and how a change gets merged.

## Ways to contribute

- Report a bug or request a feature through the [issue forms](https://github.com/fadymondy/hosbah/issues/new/choose).
- Pick up an issue labelled [`good first issue`](https://github.com/fadymondy/hosbah/labels/good%20first%20issue)
  or [`help wanted`](https://github.com/fadymondy/hosbah/labels/help%20wanted). Comment on it first so nobody
  duplicates the work.
- Improve the docs. Typos and unclear sentences are worth a PR.

Security problems are **not** issues. Follow [SECURITY.md](SECURITY.md) instead.

## Local setup

Requirements: Go (version in `go.mod`), Node 24, Docker, and the [togo CLI](https://github.com/togo-framework) (optional; every step below has a plain `go` equivalent).

```bash
docker volume create cloudy_pgdata && docker compose up -d   # Postgres on :55491 with the cloudy_app role
cp .env.example .env    # set AUTH_SECRET (openssl rand -hex 32) and HOSBAH_KEK (openssl rand -base64 32)

# codegen (sqlc + gqlgen); internal/db/gen and internal/graph/gen are gitignored
sqlc generate && go run github.com/99designs/gqlgen generate   # or: togo generate
go run ./cmd/migrate    # or: togo migrate
go run ./cmd/api        # API on :8710
cd web && npm install && npm run dev   # web on :3710
```

To test servers locally without a VPS, build the agent with `make agent` and use the systemd container in `deploy/dev-target` (see [docs/architecture.md](docs/architecture.md#try-the-bring-your-own-path-locally)).

## Before you open a pull request

```bash
go vet ./...
go test -count=1 ./...          # set TEST_DATABASE_URL to run the isolation sweep and RLS tests
cd web && npx tsc --noEmit && npm run build
```

CI runs the same, plus a full `docker-compose.selfhost.yml` boot. New team-scoped routes or MCP tools must be added to the isolation sweep (`internal/server/isolation_test.go`), and every MCP tool must declare a scope.

## Conventions

- **Small, focused PRs.** One concern per PR; refactors separate from behaviour changes.
- **Branch names:** `feat/…`, `fix/…`, `docs/…`, `chore/…` off `main`.
- **Commit messages:** [Conventional Commits](https://www.conventionalcommits.org/) style
  (`feat: …`, `fix: …`, `docs: …`), imperative mood, explain *why* in the body.
- **Tests:** a bug fix comes with a test that fails without it; a feature comes with tests for its
  main path and its permission checks.
- **No secrets** in code, tests, fixtures or screenshots. Configuration goes in `.env`
  (documented in `.env.example`).
- **AI-assisted contributions are welcome.** Agents should read [AGENTS.md](AGENTS.md). You are
  responsible for every line you submit: review it, run it, and test it as if you wrote it.

## Developer Certificate of Origin (DCO)

Every commit must be signed off, certifying the [Developer Certificate of Origin](https://developercertificate.org/):
you wrote the change, or otherwise have the right to submit it under the project's license.

```bash
git commit -s -m "fix: describe the change"
```

This adds a `Signed-off-by: Your Name <you@example.com>` trailer that matches your Git identity.
Forgot? `git commit --amend -s` for the last commit, or `git rebase --signoff main` for a branch.

## License

Hosbah is licensed under the [GNU AGPL-3.0](LICENSE). By contributing you agree your contribution
is licensed under the same terms.

## Community

- **Questions and ideas:** [GitHub Discussions](https://github.com/fadymondy/hosbah/discussions)
- **Bugs and feature requests:** [Issues](https://github.com/fadymondy/hosbah/issues/new/choose)

Everyone taking part is expected to follow the [Code of Conduct](CODE_OF_CONDUCT.md).
