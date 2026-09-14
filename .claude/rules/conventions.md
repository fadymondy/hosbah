# togo project conventions

1. **Generator-first.** Entities are added with `togo make:resource`, not hand-written. The resource manifest `togo.resources.yaml` is the source of truth.
2. **Ownership classes.**
   - *Fragments* (per-resource, hand-editable): models, queries, schema, resolvers, handlers, pages.
   - *Registries* (`*.gen.go`, DO NOT EDIT): regenerated from the manifest.
   - *Codegen output* (`internal/**/gen/`): produced by sqlc/gqlgen — never hand-edit.
3. **Codegen order**: `togo generate` runs sqlc → gqlgen → atlas diff → OpenAPI export. The OpenAPI export compiles the whole program and is the integration gate.
4. **Config is dynamic.** Connections/URLs/endpoints come from `togo.yaml` + `.env`; never hard-code them.
5. **API-first.** Every resource is exposed over both GraphQL and REST/OpenAPI.
