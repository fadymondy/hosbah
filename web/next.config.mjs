/** @type {import('next').NextConfig} */
const nextConfig = {
  // Don't let `next dev` write AGENTS.md / CLAUDE.md into the app.
  agentRules: false,
  // In a git worktree, web/node_modules is a junction into the main checkout: tell Turbopack the
  // real project root so it doesn't refuse to follow that symlink out of the worktree.
  ...(process.env.HOSBAH_TURBOPACK_ROOT ? { turbopack: { root: process.env.HOSBAH_TURBOPACK_ROOT } } : {}),
  // cloudy-app.fadymondy.com is the progress preview: Cloudflare → NPM → SSH reverse tunnel → this dev
  // server. Next 16 dev won't hydrate a page served to an origin that isn't listed here.
  allowedDevOrigins: ["127.0.0.1", "localhost", "cloudy-app.fadymondy.com", "dev.hosbah.com"],
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
          sw,
          {
            source: "/_next/static/:path*",
            headers: [{ key: "Cache-Control", value: "no-store, must-revalidate" }],
          },
        ]
      : [sw]
  },
}

export default nextConfig
