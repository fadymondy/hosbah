// Bundles the main process and the preload to CommonJS (the sandboxed preload must be one file).
import { build, context } from "esbuild";

const watch = process.argv.includes("--watch");
const opts = {
  entryPoints: { main: "src/main/main.ts", preload: "src/main/preload.ts" },
  outdir: "dist",
  outExtension: { ".js": ".cjs" },
  bundle: true,
  platform: "node",
  format: "cjs",
  target: "node22",
  external: ["electron", "electron-updater"],
  sourcemap: watch ? "inline" : false,
  minify: !watch,
  logLevel: "info",
};
if (watch) await (await context(opts)).watch();
else await build(opts);
