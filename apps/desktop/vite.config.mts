import { fileURLToPath } from "node:url";
import { defineConfig, type Plugin } from "vite";
import react from "@vitejs/plugin-react";
import tailwind from "@tailwindcss/vite";

// The renderer makes no network requests of its own (the main process does), so production CSP
// allows nothing but its own files. Dev relaxes script/connect for Vite's HMR client.
const PROD_CSP =
  "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; font-src 'self'; img-src 'self' data:; connect-src 'none'; base-uri 'none'; form-action 'none'; object-src 'none'";
const DEV_CSP =
  "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; font-src 'self'; img-src 'self' data:; connect-src 'self' ws://localhost:5176; object-src 'none'";

function csp(): Plugin {
  let dev = true;
  return {
    name: "hosbah-csp",
    configResolved(c) {
      dev = c.command === "serve";
    },
    transformIndexHtml(html) {
      return html.replace("<!--CSP-->", `<meta http-equiv="Content-Security-Policy" content="${dev ? DEV_CSP : PROD_CSP}" />`);
    },
  };
}

export default defineConfig({
  root: "src/renderer",
  base: "./",
  plugins: [react(), tailwind(), csp()],
  resolve: { alias: { "@": fileURLToPath(new URL("./src/renderer", import.meta.url)) } },
  server: { port: 5176, strictPort: true },
  build: {
    chunkSizeWarningLimit: 4000,
    outDir: "../../dist/renderer",
    emptyOutDir: true,
    assetsInlineLimit: 0,
    target: "chrome140",
  },
});
