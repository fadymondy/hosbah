/** @type {import('next').NextConfig} */
// Security headers on every response (MH-1253): pin HTTPS, no MIME sniffing, no full-URL referrers,
// and the app can only be framed by itself (clickjacking). Nothing embeds these pages cross-origin.
const SECURITY_HEADERS = {
  source: "/:path*",
  headers: [
    { key: "Strict-Transport-Security", value: "max-age=31536000" },
    { key: "X-Content-Type-Options", value: "nosniff" },
    { key: "Referrer-Policy", value: "strict-origin-when-cross-origin" },
    { key: "X-Frame-Options", value: "SAMEORIGIN" },
    { key: "Content-Security-Policy", value: "frame-ancestors 'self'" },
  ],
}

const nextConfig = {
  // Don't let `next dev` write AGENTS.md / CLAUDE.md into the app.
  agentRules: false,
  // In a git worktree, web/node_modules is a junction into the main checkout: tell Turbopack the
  // real project root so it doesn't refuse to follow that symlink out of the worktree.
  // Editing Nasaq itself: node_modules/@fadymondy/nasaq is a junction to the local Nasaq repo (docs/ux/NASAQ-LOCAL.md).
  // Turbopack needs a root above both, and one React (Nasaq has its own copy).
  ...(process.env.HOSBAH_NASAQ_LOCAL
    ? {
        turbopack: {
          root: process.env.HOSBAH_NASAQ_LOCAL,
          resolveAlias: { react: "./node_modules/react", "react-dom": "./node_modules/react-dom" },
        },
      }
    : process.env.HOSBAH_TURBOPACK_ROOT
      ? { turbopack: { root: process.env.HOSBAH_TURBOPACK_ROOT } }
      : {}),
  // cloudy-app.fadymondy.com is the progress preview: Cloudflare → NPM → SSH reverse tunnel → this dev
  // server. Next 16 dev won't hydrate a page served to an origin that isn't listed here.
  allowedDevOrigins: ["127.0.0.1", "localhost", ...(process.env.DEV_ORIGINS ?? "").split(",").map((s) => s.trim()).filter(Boolean)],
  // The browser only talks to this origin: /api/* is proxied to the Go API, so
  // the togo session cookie (HttpOnly) and CSRF cookie are first-party.
  async rewrites() {
    const origin = process.env.API_ORIGIN ?? "http://localhost:8710"
    return [
      { source: "/api/:path*", destination: `${origin}/api/:path*` },
      // GitHub App webhooks and per-app deploy hooks are served by the API too.
      { source: "/hooks/:path*", destination: `${origin}/hooks/:path*` },
      // Sign in with CircleXO (internal/circlexo): the hub redirects the browser to these on the web
      // origin, so they are the API's, rewritten there. Off (404) unless CIRCLEXO_ISSUER is set.
      { source: "/auth/circlexo/:path*", destination: `${origin}/auth/circlexo/:path*` },
      { source: "/sso/circlexo", destination: `${origin}/sso/circlexo` },
      // The OAuth token endpoint for integrations (MH-1153); /oauth/authorize is the web's own page.
      { source: "/oauth/token", destination: `${origin}/oauth/token` },
      // The real-user-monitoring beacon (internal/rum); its ingest endpoint is under /api/rum/.
      { source: "/rum.js", destination: `${origin}/rum.js` },
    ]
  },
  async headers() {
    // Dev chunks use stable filenames with a 4h max-age; a stale stylesheet
    // cost fadymondy.com an hour of wrong diagnoses. Dev only.
    // The service worker must never be cached, or an old one keeps serving after a deploy.
    const sw = { source: "/sw.js", headers: [{ key: "Cache-Control", value: "no-cache, no-store, must-revalidate" }] }
    return process.env.NODE_ENV === "development"
      ? [
          SECURITY_HEADERS,
          sw,
          {
            source: "/_next/static/:path*",
            headers: [{ key: "Cache-Control", value: "no-store, must-revalidate" }],
          },
        ]
      : [SECURITY_HEADERS, sw]
  },
}

export default nextConfig
