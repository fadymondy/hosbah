"use client"

import { useEffect, useState } from "react"

import { apiFetch } from "@/lib/api"
import type { SignupMode } from "@/lib/admin"

/** GET /api/settings/public: what every visitor may know about the platform's settings. */
export type PublicSettings = {
  signup_mode: SignupMode
  announcement: string
  announcement_level: "info" | "warning"
  support_email: string
}

// One request per page load, shared by every component that asks.
let pending: Promise<PublicSettings | null> | null = null

function fetchOnce(): Promise<PublicSettings | null> {
  pending ??= apiFetch<PublicSettings>("/api/settings/public").catch(() => null)
  return pending
}

/** The public settings, or null while loading (and if they cannot be read: callers then behave as before). */
export function usePublicSettings(): PublicSettings | null {
  const [s, setS] = useState<PublicSettings | null>(null)
  useEffect(() => {
    let live = true
    void fetchOnce().then((v) => live && setS(v))
    return () => {
      live = false
    }
  }, [])
  return s
}

/** The invitation token when the sign-up came from an invitation link (?next=/xx/invite/<token>). */
export function invitationTokenFrom(next: string | null | undefined): string | undefined {
  const m = next?.match(/\/invite\/([^/?#]+)/)
  return m ? decodeURIComponent(m[1]) : undefined
}
