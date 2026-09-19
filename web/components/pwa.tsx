"use client"

import { useEffect } from "react"

/**
 * Registers the service worker (public/sw.js) so Hosbah installs as an app and shows a friendly
 * page offline. Production only: in development it would cache stale bundles between reloads.
 */
export function RegisterServiceWorker() {
  useEffect(() => {
    if (process.env.NODE_ENV !== "production" || !("serviceWorker" in navigator)) return
    navigator.serviceWorker.register("/sw.js", { scope: "/" }).catch(() => {
      // Not fatal: the app works the same without it, only without install/offline support.
    })
  }, [])
  return null
}
