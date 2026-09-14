# web

The Next.js (App Router) frontend for this togo app. It talks to the Go backend
over REST (`/api`) and GraphQL (`/graphql`), proxied in dev via `next.config.mjs`.

Run it as part of the whole stack:

```bash
togo serve            # backend + frontend together (installs deps on first run)
togo serve --web-only # just the frontend
```

Or directly:

```bash
npm install
npm run dev
```

## Layout

```
app/                 App Router pages + layout (shell with sidebar)
  page.tsx           dashboard
  <resource>/page.tsx  generated per resource (togo make:resource)
components/          Sidebar, ApiStatus, …
lib/api/             API client + types
lib/hooks/           data hooks (generated per resource)
```

Styling is Tailwind CSS v4 (`app/globals.css`).
