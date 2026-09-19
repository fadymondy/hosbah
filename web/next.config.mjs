/** @type {import('next').NextConfig} */
const nextConfig = {
  // Don't let `next dev` write AGENTS.md / CLAUDE.md into the app.
  agentRules: false,
  // cloudy-app.fadymondy.com is the progress preview: Cloudflare → NPM → SSH reverse tunnel → this dev
  // server. Next 16 dev won't hydrate a page served to an origin that isn't listed here.
  allowedDevOrigins: ["127.0.0.1", "localhost", "cloudy-app.fadymondy.com", "dev.hosbah.com"],
  // The browser only talks to this origin: /api/* is proxied to the Go API, so
  // the togo session cookie (HttpOnly) and CSRF cookie are first-party.
  async rewrites() {
    const origin = process.env.API_ORIGIN ?? "http://localhost:8710"
    return [{ source: "/api/:path*", destination: `${origin}/api/:path*` }]
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
