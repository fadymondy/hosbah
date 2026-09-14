/** @type {import('next').NextConfig} */
const nextConfig = {
  // Don't let `next dev` write AGENTS.md / CLAUDE.md into the app.
  agentRules: false,
  // The browser only talks to this origin: /api/* is proxied to the Go API, so
  // the togo session cookie (HttpOnly) and CSRF cookie are first-party.
  async rewrites() {
    const origin = process.env.API_ORIGIN ?? "http://localhost:8710"
    return [{ source: "/api/:path*", destination: `${origin}/api/:path*` }]
  },
  async headers() {
    // Dev chunks use stable filenames with a 4h max-age; a stale stylesheet
    // cost fadymondy.com an hour of wrong diagnoses. Dev only.
    return process.env.NODE_ENV === "development"
      ? [
          {
            source: "/_next/static/:path*",
            headers: [{ key: "Cache-Control", value: "no-store, must-revalidate" }],
          },
        ]
      : []
  },
}

export default nextConfig
