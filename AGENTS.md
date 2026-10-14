# AGENTS.md

Guidance for AI coding agents (Codex, Cursor, Claude Code and others) working in this repository.

- Read [CLAUDE.md](CLAUDE.md) first: commands, architecture invariants (tenancy, RLS, signed agent
  operations, crypto), codegen ownership and naming. It applies to every agent, not only Claude.
- The detailed design record is [docs/architecture.md](docs/architecture.md).
- Contribution rules, checks and the DCO sign-off are in [CONTRIBUTING.md](CONTRIBUTING.md).
- Never commit secrets, `.env` files, real hostnames or IP addresses; use RFC 5737 addresses
  (`203.0.113.x`) and `example.com` in tests and docs.
