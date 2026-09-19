/*
  Hosbah's service worker (PWA). Deliberately small:
  - /api/* is never touched: sessions, CSRF, live log streams and admin data always go to the
    network, and nothing personal is ever cached.
  - Pages are network-first; offline, a page shows /offline.html (bilingual) instead of an error.
  - Hashed build assets (/_next/static), icons and fonts are cache-first: they never change.
  Bump VERSION to drop every old cache on the next visit.
*/
const VERSION = "hosbah-v2"
const STATIC = `${VERSION}-static`
const OFFLINE = "/offline.html"

// Each file is cached on its own: one that fails (an edge cache serving a stale 404, a network
// blip) must not fail the install, or the browser drops the worker altogether.
const PRECACHE = [OFFLINE, "/icons/icon-192.png"]

self.addEventListener("install", (event) => {
  event.waitUntil(
    caches
      .open(STATIC)
      .then((c) => Promise.allSettled(PRECACHE.map((u) => c.add(u))))
      .then(() => self.skipWaiting()),
  )
})

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((keys) => Promise.all(keys.filter((k) => !k.startsWith(VERSION)).map((k) => caches.delete(k))))
      .then(() => self.clients.claim()),
  )
})

self.addEventListener("fetch", (event) => {
  const req = event.request
  if (req.method !== "GET") return
  const url = new URL(req.url)
  if (url.origin !== self.location.origin) return // Mahaam's widget, fonts from elsewhere…
  if (url.pathname.startsWith("/api/") || url.pathname.startsWith("/agent/") || url.pathname.startsWith("/install/")) return

  if (req.mode === "navigate") {
    event.respondWith(fetch(req).catch(() => caches.match(OFFLINE)))
    return
  }

  if (url.pathname.startsWith("/_next/static/") || url.pathname.startsWith("/icons/") || /\.(woff2?|svg|png|ico)$/.test(url.pathname)) {
    event.respondWith(
      caches.match(req).then(
        (hit) =>
          hit ||
          fetch(req).then((res) => {
            if (res.ok) {
              const copy = res.clone()
              caches.open(STATIC).then((c) => c.put(req, copy))
            }
            return res
          }),
      ),
    )
  }
})
