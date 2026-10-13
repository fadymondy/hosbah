// npm run shots: starts the mock API on a private port, bundles the screenshot driver, and runs the
// real Electron app against the mock (HOSBAH_E2E=1 refuses anything but localhost). Screenshots come
// from webContents.capturePage(), written to $HOSBAH_SHOTS_DIR (default ./shots).
import { spawn } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { build } from "esbuild";

const PORT = process.env.MOCK_PORT ?? "8719";
const api = `http://127.0.0.1:${PORT}`;

await build({
  entryPoints: { shots: "src/main/shots.ts" },
  outdir: "dist",
  outExtension: { ".js": ".cjs" },
  bundle: true,
  platform: "node",
  format: "cjs",
  target: "node22",
  external: ["electron"],
  logLevel: "warning",
});

const mock = spawn(process.execPath, ["scripts/mock-api.mjs"], { env: { ...process.env, PORT }, stdio: ["ignore", "ignore", "inherit"] });
for (let i = 0; i < 50; i++) {
  try {
    await fetch(`${api}/__mock/calls`);
    break;
  } catch {
    await new Promise((r) => setTimeout(r, 100));
  }
}

const userData = mkdtempSync(join(tmpdir(), "hosbah-shots-"));
const { default: electronPath } = await import("electron");
const { ELECTRON_RUN_AS_NODE: _drop, ...env } = process.env;
const child = spawn(electronPath, ["dist/shots.cjs"], {
  stdio: "inherit",
  env: { ...env, HOSBAH_E2E: "1", HOSBAH_USER_DATA: userData, HOSBAH_API_URL: api },
});
const code = await new Promise((r) => child.on("exit", r));
mock.kill();
rmSync(userData, { recursive: true, force: true });
process.exit(code ?? 1);
