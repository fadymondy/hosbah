"use client"

import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from "react"

import { apiFetch, ApiError, canManage, type Team } from "@/lib/api"

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
