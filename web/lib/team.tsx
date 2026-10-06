"use client"

import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from "react"

import { apiFetch, ApiError, canManage, type Team } from "@/lib/api"
import { useI18n } from "@/lib/i18n"

type TeamCtx = { team: Team; manage: boolean; refresh: () => Promise<void> }

const Ctx = createContext<TeamCtx | null>(null)

/** Loads /api/teams/{slug}. A team you are not in answers 404 — rendered as not found. */
export function TeamGate({
  slug,
  children,
  loading,
  notFound,
  failed,
}: {
  slug: string
  children: ReactNode
  loading: ReactNode
  notFound: ReactNode
  failed: (message: string, retry: () => void) => ReactNode
}) {
  const [team, setTeam] = useState<Team | null>(null)
  const [state, setState] = useState<"loading" | "ok" | "missing" | "error">("loading")
  const [message, setMessage] = useState("")

  const load = useCallback(async () => {
    try {
      setTeam(await apiFetch<Team>(`/api/teams/${encodeURIComponent(slug)}`))
      setState("ok")
    } catch (e) {
      if (e instanceof ApiError && (e.status === 404 || e.status === 403)) setState("missing")
      else {
        setMessage(e instanceof Error ? e.message : String(e))
        setState("error")
      }
    }
  }, [slug])

  useEffect(() => {
    setState("loading")
    void load()
  }, [load])

  if (state === "missing") return <>{notFound}</>
  if (state === "error") return <>{failed(message, load)}</>
  if (state === "loading" || !team) return <>{loading}</>
  return <Ctx.Provider value={{ team, manage: canManage(team.role), refresh: load }}>{children}</Ctx.Provider>
}

export function useTeam(): TeamCtx {
  const ctx = useContext(Ctx)
  if (!ctx) throw new Error("useTeam outside TeamGate")
  return ctx
}

/** The team if the current page is inside one (the shell renders on /account and /admin too). */
export function useOptionalTeam(): TeamCtx | null {
  return useContext(Ctx)
}

const BaseCtx = createContext<string | null>(null)

/** Moves the team's app pages under another root (a server user's panel, /{locale}/panel/{id}). */
export function TeamBase({ base, children }: { base: string; children: ReactNode }) {
  return <BaseCtx.Provider value={base}>{children}</BaseCtx.Provider>
}

/** Where the team's pages live: /{locale}/t/{slug}, or the panel's root inside a panel. */
export function useTeamBase(): string {
  const override = useContext(BaseCtx)
  const { team } = useTeam()
  const { locale } = useI18n()
  return override ?? `/${locale}/t/${team.slug}`
}

/** True inside a server user's panel. */
export function useInPanel(): boolean {
  return useContext(BaseCtx) !== null
}

/** A server's tab: /servers/{id}/{tab} in the console; inside a panel, the panel's own tab. */
export function useServerHref(): (serverId: string, tab?: string) => string {
  const base = useTeamBase()
  const panel = useInPanel()
  return (serverId, tab = "") => (panel ? `${base}${tab ? `/${tab}` : ""}` : `${base}/servers/${serverId}${tab ? `/${tab}` : ""}`)
}

/**
 * Like TeamGate, but for the console's persistent shell: the shell stays mounted while a team loads
 * (or when you move between teams, /account and /admin) and only the page body is swapped for the
 * loading, not-found or failed state. While the next team loads, the sidebar keeps the previous
 * one; the body never renders a page against the wrong team.
 */
export function TeamScope({
  slug,
  shell,
  children,
  loading,
  notFound,
  failed,
}: {
  slug: string | null
  shell: (body: ReactNode) => ReactNode
  children: ReactNode
  loading: ReactNode
  notFound: ReactNode
  failed: (message: string, retry: () => void) => ReactNode
}) {
  const [team, setTeam] = useState<Team | null>(null)
  const [loadedFor, setLoadedFor] = useState<string | null>(null)
  const [state, setState] = useState<"loading" | "ok" | "missing" | "error">("loading")
  const [message, setMessage] = useState("")

  const load = useCallback(async () => {
    if (!slug) return
    try {
      const next = await apiFetch<Team>(`/api/teams/${encodeURIComponent(slug)}`)
      setTeam(next)
      setLoadedFor(slug)
      setState("ok")
    } catch (e) {
      setLoadedFor(slug)
      if (e instanceof ApiError && (e.status === 404 || e.status === 403)) setState("missing")
      else {
        setMessage(e instanceof Error ? e.message : String(e))
        setState("error")
      }
    }
  }, [slug])

  useEffect(() => {
    void load()
  }, [load])

  const ready = !!slug && loadedFor === slug && state === "ok" && !!team
  let body: ReactNode = children
  if (slug && !ready) {
    if (loadedFor === slug && state === "missing") body = notFound
    else if (loadedFor === slug && state === "error") body = failed(message, load)
    else body = loading
  }
  const value = slug && team ? { team, manage: canManage(team.role), refresh: load } : null
  return <Ctx.Provider value={value}>{shell(body)}</Ctx.Provider>
}
