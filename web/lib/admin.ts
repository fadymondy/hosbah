"use client"

import { useCallback, useEffect, useRef, useState } from "react"

import { apiFetch } from "@/lib/api"

/** One page of a platform-admin list (GET /api/admin/<list>?q=&limit=&offset=…). */
export type Page<T> = { items: T[]; total: number }

export const ADMIN_PAGE_SIZE = 25

/**
 * A searchable, paginated admin list. `filters` are extra query params (status, kind…); changing
 * the search or a filter goes back to the first page. The search is debounced so typing does not
 * fire a request per key.
 */
export function useAdminList<T>(path: string, filters: Record<string, string | undefined> = {}) {
  const [q, setQ] = useState("")
  const [debounced, setDebounced] = useState("")
  const [offset, setOffset] = useState(0)
  const [data, setData] = useState<Page<T> | null>(null)
  const [error, setError] = useState<unknown>(null)
  const [loading, setLoading] = useState(true)
  const seq = useRef(0)
  const filterKey = JSON.stringify(filters)

  useEffect(() => {
    const id = setTimeout(() => setDebounced(q.trim()), 250)
    return () => clearTimeout(id)
  }, [q])

  useEffect(() => setOffset(0), [debounced, filterKey])

  const load = useCallback(async () => {
    const mine = ++seq.current
    setLoading(true)
    const params = new URLSearchParams({ limit: String(ADMIN_PAGE_SIZE), offset: String(offset) })
    if (debounced) params.set("q", debounced)
    for (const [k, v] of Object.entries(JSON.parse(filterKey) as Record<string, string | undefined>)) {
      if (v) params.set(k, v)
    }
    try {
      const r = await apiFetch<Page<T>>(`${path}?${params}`)
      if (mine !== seq.current) return // a newer request superseded this one
      setData({ items: r.items ?? [], total: r.total ?? 0 })
      setError(null)
    } catch (e) {
      if (mine !== seq.current) return
      setError(e)
    } finally {
      if (mine === seq.current) setLoading(false)
    }
  }, [path, offset, debounced, filterKey])

  useEffect(() => {
    void load()
  }, [load])

  return { q, setQ, offset, setOffset, data, error, loading, reload: load, pageSize: ADMIN_PAGE_SIZE }
}

/** A single admin resource (GET), with reload after a mutation. */
export function useAdminResource<T>(path: string | null) {
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState<unknown>(null)

  const load = useCallback(async () => {
    if (!path) return
    try {
      setData(await apiFetch<T>(path))
      setError(null)
    } catch (e) {
      setError(e)
    }
  }, [path])

  useEffect(() => {
    void load()
  }, [load])

  return { data, error, loading: data === null && error === null, reload: load }
}

// ---- Shapes of /api/admin/* (internal/api/admin_platform.go) -------------------------------------

export type AdminOverview = {
  users: number
  platform_admins: number
  teams: number
  servers: { total: number; online: number; offline: number }
  apps: number
  deployments_last_24h: { total: number; failed: number }
  operations_last_24h: { total: number; failed: number }
  databases: number
  recent_signups: { id: string; email: string; created_at: string }[]
}

export type AdminUser = {
  id: string
  email: string
  platform_admin: boolean
  disabled: boolean
  disabled_at: string | null
  created_at: string
  team_count: number
}

export type AdminUserDetail = AdminUser & {
  teams: { id: string; slug: string; name: string; role: string }[]
  mcp_token_count: number
  admin_mcp_token_count: number
}

export type AdminTeam = {
  id: string
  slug: string
  name: string
  created_at: string
  members: number
  servers: number
  apps: number
  suspended: boolean
  suspended_at: string | null
}

export type AdminTeamDetail = {
  team: AdminTeam
  members: { user_id: string; email: string; role: string }[]
  servers: { id: string; name: string; provider: string; kind: string; status: string; online: boolean; last_seen_at: string | null }[]
  apps: { id: string; name: string; server_id: string; server_name: string }[]
  counts: { members: number; servers: number; apps: number; databases: number }
}

export type AdminServer = {
  id: string
  name: string
  team_id: string
  team_slug: string
  team_name: string
  provider: string
  kind: string
  status: string
  online: boolean
  ip: string | null
  hostname: string | null
  agent_version: string | null
  last_seen_at: string | null
  created_at: string
}

export type AdminOperation = {
  id: string
  team_id: string
  team_slug: string
  team_name: string
  server_id: string
  server_name: string
  kind: string
  executor: string
  status: string
  exit_code: number | null
  error: string
  created_at: string
  finished_at: string | null
}

export type AdminAuditEntry = {
  id: string
  actor_id: string
  actor_email: string
  action: string
  target_type: string
  target_id: string
  details: Record<string, unknown> | null
  ip: string | null
  created_at: string
}

export type SignupMode = "open" | "invite_only" | "closed"

export type PlatformSettings = {
  signup_mode: SignupMode
  announcement: string
  announcement_level: "info" | "warning"
  support_email: string
  max_teams_per_user: number
  max_servers_per_team: number
  updated_by: string | null
  updated_at: string
}
