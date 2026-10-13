// npm run dev: Vite for the renderer (hot reload), esbuild --watch for main/preload, then Electron.
import { spawn } from "node:child_process";
import { createServer } from "vite";

const server = await createServer({ configFile: "vite.config.mts" });
await server.listen();
const url = server.resolvedUrls.local[0];
const main = spawn(process.execPath, ["scripts/build-main.mjs", "--watch"], { stdio: "inherit" });
await new Promise((r) => setTimeout(r, 800));
const { ELECTRON_RUN_AS_NODE: _drop, ...env } = process.env;
// The electron package's default export is the path of its binary. Spawning that directly works
// everywhere; spawning npx.cmd fails with EINVAL on Windows under Node >= 18.20.2, which refuses
// .cmd files without a shell (CVE-2024-27980).
const { default: electronPath } = await import("electron");
const electron = spawn(electronPath, [".", ...process.argv.slice(2)], {
  stdio: "inherit",
  env: { ...env, HOSBAH_DEV_URL: url },
});
electron.on("exit", () => {
  main.kill();
  void server.close();
  process.exit(0);
});
