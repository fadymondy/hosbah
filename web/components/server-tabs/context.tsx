"use client"

import { createContext, useContext, useEffect, useState, type Dispatch, type ReactNode, type SetStateAction } from "react"

import { useErrorText } from "@/components/form-error"
import { apiFetch, ApiError } from "@/lib/api"
import { useEventStream, type Operation, type Server } from "@/lib/servers"
import { useTeam } from "@/lib/team"

type ServerPage = {
  server: Server | null
  setServer: Dispatch<SetStateAction<Server | null>>
  missing: boolean
  error: string | null
  setError: (e: string | null) => void
  connected: boolean
  ops: Operation[]
  setOps: Dispatch<SetStateAction<Operation[]>>
  /** The agent version this control plane ships (for the update button). */
  available: string | null
  /** /api/teams/{team}/servers/{server} */
  path: string
}

const Ctx = createContext<ServerPage | null>(null)

/**
 * One server's live state, shared by every tab under /servers/{id}: loaded once, then kept
 * current by the server's stream (the server itself and its recent operations).
 */
export function ServerProvider({ serverId, children }: { serverId: string; children: ReactNode }) {
  const { team } = useTeam()
  const errorText = useErrorText()
  const [server, setServer] = useState<Server | null>(null)
  const [missing, setMissing] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [ops, setOps] = useState<Operation[]>([])
  const [available, setAvailable] = useState<string | null>(null)
  const path = `/api/teams/${team.slug}/servers/${serverId}`

  useEffect(() => {
    apiFetch<Server>(path)
      .then(setServer)
      .catch((err) => (err instanceof ApiError && err.status === 404 ? setMissing(true) : setError(errorText(err))))
    apiFetch<{ agent_version: string }>(`/api/teams/${team.slug}/servers`)
      .then((r) => setAvailable(r.agent_version))
      .catch(() => {})
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path])

  const connected = useEventStream(missing ? null : `${path}/stream`, {
    server: (d) => setServer(d as Server),
    operations: (d) => setOps(d as Operation[]),
    gone: () => setMissing(true),
  })

  return (
    <Ctx.Provider value={{ server, setServer, missing, error, setError, connected, ops, setOps, available, path }}>
      {children}
    </Ctx.Provider>
  )
}

export function useServerPage() {
  const v = useContext(Ctx)
  if (!v) throw new Error("useServerPage outside ServerProvider")
  return v
}

/** The loaded server, for tabs (the layout renders them only once it has loaded). */
export function useLoadedServer() {
  const v = useServerPage()
  return { ...v, server: v.server as Server }
}
