# Editing Nasaq from Hosbah

`web/node_modules/@fadymondy/nasaq` is a junction to `../nasaq/packages/nasaq` (the registry 0.1.0 copy is kept in `web/node_modules/.nasaq-registry`). The dev server runs with `HOSBAH_NASAQ_LOCAL=<parent directory of both repos>`, which `next.config` turns into a Turbopack root above both repos and one shared React.

- Rebuild after changing Nasaq: `cd ../nasaq/packages/nasaq && npx tsdown && node scripts/postbuild.mjs` (not `pnpm build`: its Vue step empties `dist/web`).
- `npm install` in `web/` replaces the junction with the registry copy. Re-create it with `New-Item -ItemType Junction`.
- Production builds are unaffected: without `HOSBAH_NASAQ_LOCAL` the config is unchanged and the registry package is used.
