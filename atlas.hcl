// Atlas project config — Cloudy's migration ledger.
//
// Desired state = the per-resource SQL fragments togo writes (internal/db/schema/*.sql).
// togo 0.2.31 points this at db/atlas/schema/*.hcl, but make:resource never writes HCL there,
// so the stock config diffs against an empty schema. Versioned migrations live in
// db/atlas/migrations and are the only thing ever applied to a database: `togo migrate`
// (cmd/migrate) applies them in order and records each one, with its checksum, in
// schema_migrations. atlas.sum is committed — it is the integrity half of the ledger.
//
//   togo migrate:diff <name>   -> new versioned file from the schema fragments
//   atlas migrate new <name>   -> hand-written file (RLS policies, grants) then `atlas migrate hash`
//   togo migrate               -> apply pending files, refuse edited ones
env "local" {
  src = "file://internal/db/schema"
  url = getenv("DATABASE_URL")
  dev = getenv("DEV_DATABASE_URL")
  migration {
    dir = "file://db/atlas/migrations"
  }
}
