"use client"

import { createContext, useCallback, useContext, useEffect, useState, type Dispatch, type ReactNode, type SetStateAction } from "react"

import { useErrorText } from "@/components/form-error"
import { apiFetch, ApiError } from "@/lib/api"
import { appPath, type App, type AppDetail, type Deployment } from "@/lib/apps"
import { useEventStream } from "@/lib/servers"
import { useTeam } from "@/lib/team"

type Site = {
  detail: AppDetail | null
  setDetail: Dispatch<SetStateAction<AppDetail | null>>
  deployments: Deployment[]
  /** Adds a deployment the page just started (the stream confirms it a moment later). */
  pushDeployment: (d: Deployment) => void
  reload: () => void
  missing: boolean
  connected: boolean
  error: string | null
  setError: (e: string | null) => void
  /** /api/teams/{team}/apps/{app} */
  path: string
}

const Ctx = createContext<Site | null>(null)

/**
 * One site's live state, shared by every section under /apps/{name}: loaded once, then kept
 * current by the app's stream (the app itself and its recent deployments).
 */
export function SiteProvider({ name, children }: { name: string; children: ReactNode }) {
  const { team } = useTeam()
  const errorText = useErrorText()
  const [detail, setDetail] = useState<AppDetail | null>(null)
  const [deployments, setDeployments] = useState<Deployment[]>([])
  const [missing, setMissing] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const path = appPath(team.slug, name)

  const reload = useCallback(() => {
    apiFetch<AppDetail>(path)
      .then(setDetail)
      .catch((err) => (err instanceof ApiError && err.status === 404 ? setMissing(true) : setError(errorText(err))))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path])
  useEffect(reload, [reload])

  const connected = useEventStream(missing ? null : `${path}/stream`, {
    app: (d) => setDetail(d as AppDetail),
    deployments: (d) => setDeployments(d as Deployment[]),
    gone: () => setMissing(true),
  })

  const pushDeployment = useCallback((d: Deployment) => {
    setDeployments((prev) => (prev.some((p) => p.id === d.id) ? prev : [d, ...prev]))
  }, [])

  return (
    <Ctx.Provider value={{ detail, setDetail, deployments, pushDeployment, reload, missing, connected, error, setError, path }}>
      {children}
    </Ctx.Provider>
  )
}

export function useSite() {
  const v = useContext(Ctx)
  if (!v) throw new Error("useSite outside SiteProvider")
  return v
}

/** The loaded site, for sections (the layout renders them only once it has loaded). */
export function useLoadedSite() {
  const v = useSite()
  const detail = v.detail as AppDetail
  return { ...v, detail, app: detail.app as App }
}
