// Regenerates src/openapi.d.ts from the Go API's OpenAPI document.
//
// This shells out to `go run ./cmd/api openapi`, which prints the spec straight from the
// compiled handlers (no server, no port, no auth). If that fails (e.g. the server tree is mid-change),
// the hand-written types in src/types.ts stay authoritative; src/openapi.d.ts is reference only.
//
// Override for the rare case you want a live instance instead:
//   HOSBAH_OPENAPI=https://example/openapi.json npm run gen:types
import { execFileSync } from "node:child_process"
import { mkdtempSync, writeFileSync, rmSync } from "node:fs"
import { tmpdir } from "node:os"
import { dirname, join, resolve } from "node:path"
import { fileURLToPath } from "node:url"

const here = dirname(fileURLToPath(import.meta.url)) // apps/shared/scripts
const repoRoot = resolve(here, "..", "..", "..")

let src = process.env.HOSBAH_OPENAPI
let tmp

if (!src) {
  const yaml = execFileSync("go", ["run", "./cmd/api", "openapi"], {
    cwd: repoRoot,
    encoding: "utf8",
    maxBuffer: 128 * 1024 * 1024,
  })
  tmp = mkdtempSync(join(tmpdir(), "hosbah-openapi-"))
  src = join(tmp, "openapi.yaml")
  writeFileSync(src, yaml)
}

try {
  execFileSync("npx", ["openapi-typescript", src, "-o", "src/openapi.d.ts"], {
    cwd: resolve(here, ".."),
    stdio: "inherit",
    shell: true,
  })
} finally {
  if (tmp) rmSync(tmp, { recursive: true, force: true })
}
