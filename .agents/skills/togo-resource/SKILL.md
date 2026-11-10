---
name: togo-resource
description: Scaffold a new resource (model + sqlc + Atlas + GraphQL + REST + Next.js page) in this togo project. Use when the user wants to add an entity/table/CRUD.
---

# Add a resource to this togo app

To add a resource, run the togo generator, then the codegen pipeline:

```bash
togo make:resource <Name> <field:type ...>
togo generate
togo migrate
```

## Field types
`string`, `text`, `int`, `bool`, `float`, `decimal`, `uuid`, `time`, `date`, `json`.
A trailing `?` marks a field nullable, e.g. `body:text?`.

## Example
```bash
togo make:resource Post title:string body:text? published:bool
```

This generates the model, sqlc query + schema, Atlas table, GraphQL type +
resolver, Huma REST handler, seeder, and a Next.js page — all as per-resource
fragments, plus the regenerated route/resolver registries. Use `--dry-run` to
preview and `--force` to overwrite existing fragments.
