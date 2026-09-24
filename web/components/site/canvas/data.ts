"use client"

import { useCallback, useEffect, useRef, useState } from "react"

import type { Browser } from "@/components/site/browsers"
import type { Point } from "@/components/site/canvas/model"
import type { Monitor } from "@/components/site/monitors"
import type { RealtimeStatus } from "@/components/site/realtime"
import type { Schedule } from "@/components/site/schedules"
import type { Install } from "@/components/site/stacks"
import type { Bucket, Volume } from "@/components/site/storage"
import { apiFetch } from "@/lib/api"
import type { DataService } from "@/lib/databases"

export type Viewport = { x: number; y: number; zoom: number }
export type CanvasLayout = { nodes: Record<string, Point>; viewport?: Viewport | null }
type CanvasResponse = { layout: CanvasLayout; updated_at: string | null; can_save: boolean; connections: { service_id: string; variable: string }[] }

export type CanvasData = {
  /** The saved layout as first loaded (later saves do not replace it here: the canvas owns positions). */
  saved: CanvasLayout | null
  canSave: boolean
  connections: CanvasResponse["connections"]
  services: DataService[]
  volumes: Volume[]
  buckets: Bucket[]
  browsers: Browser[]
  installs: Install[]
  realtime: RealtimeStatus | null
  monitors: Monitor[]
  schedules: Schedule[]
}

/** How often the canvas looks again at what the page's own stream does not carry. */
const REFRESH_MS = 30_000

/**
 * What the overview canvas draws besides the page's own state (useLoadedSite: the app, its
 * domains, processes and deployments, kept live by the app's stream): the saved layout and the
 * app's connections, the server's data services, and each section's items. Loaded once, again
 * after every change made from the canvas, and every 30 s while the page is visible. A section
 * that fails to load keeps what it last showed (nothing, the first time) rather than failing the canvas.
 */
export function useCanvasData(team: string, path: string, serverId: string, imported: boolean) {
  const [data, setData] = useState<CanvasData | null>(null)
  const first = useRef(true)

  const load = useCallback(async () => {
    const get = <T,>(url: string) => apiFetch<T>(url).catch(() => null)
    const [canvas, ds, storage, browsers, installs, realtime, monitors, schedules] = await Promise.all([
      get<CanvasResponse>(`${path}/canvas`),
      imported ? null : get<{ services: DataService[] }>(`/api/teams/${team}/data-services`),
      imported ? null : get<{ volumes: Volume[]; buckets: Bucket[] }>(`${path}/storage`),
      imported ? null : get<{ browsers: Browser[] }>(`${path}/browsers`),
      imported ? null : get<{ installs: Install[] }>(`${path}/installs`),
      imported ? null : get<RealtimeStatus>(`${path}/realtime`),
      get<{ monitors: Monitor[] }>(`${path}/monitors`),
      imported ? null : get<{ schedules: Schedule[] }>(`${path}/schedules`),
    ])
    const isFirst = first.current
    first.current = false
    setData((prev) => ({
      saved: isFirst ? (canvas?.layout ?? null) : (prev?.saved ?? null),
      canSave: canvas?.can_save ?? false,
      connections: canvas?.connections ?? prev?.connections ?? [],
      services: ds ? (ds.services ?? []).filter((s) => s.server_id === serverId) : (prev?.services ?? []),
      volumes: storage ? (storage.volumes ?? []) : (prev?.volumes ?? []),
      buckets: storage ? (storage.buckets ?? []) : (prev?.buckets ?? []),
      browsers: browsers ? (browsers.browsers ?? []) : (prev?.browsers ?? []),
      installs: installs ? (installs.installs ?? []) : (prev?.installs ?? []),
      realtime: realtime ?? prev?.realtime ?? null,
      monitors: monitors ? (monitors.monitors ?? []) : (prev?.monitors ?? []),
      schedules: schedules ? (schedules.schedules ?? []) : (prev?.schedules ?? []),
    }))
  }, [team, path, serverId, imported])

  useEffect(() => {
    first.current = true
    void load()
    const id = setInterval(() => {
      if (document.visibilityState === "visible") void load()
    }, REFRESH_MS)
    return () => clearInterval(id)
  }, [load])

  return { data, reload: load }
}

/** Store where the boxes sit (owner or admin; the API refuses anyone else). */
export function saveCanvas(path: string, layout: CanvasLayout) {
  return apiFetch(`${path}/canvas`, { method: "PUT", body: { layout } })
}
