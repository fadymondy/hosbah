"use client"

import { useEffect } from "react"

/**
 * Registers the service worker (public/sw.js) so Hosbah installs as an app and shows a friendly
 * page offline. Production only: in development it would cache stale bundles between reloads.
 */
export function RegisterServiceWorker() {
  useEffect(() => {
    if (process.env.NODE_ENV !== "production" || !("serviceWorker" in navigator)) return
    // Versioned URL: an edge cache may hold an old response for the bare /sw.js; bump with sw.js.
    navigator.serviceWorker.register("/sw.js?v=2", { scope: "/" }).catch(() => {
      // Not fatal: the app works the same without it, only without install/offline support.
    })
  }, [])
  return null
}
