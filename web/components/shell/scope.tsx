"use client"

import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from "react"

/*
Where the person is below the team: a server, or a site on a server. The shell (breadcrumb and
sidebar) sits above the pages, so the server and site layouts tell it what they loaded; the shell
reads it to show Team › Server › Site and the sidebar of the deepest level.
*/

export type ScopeServer = {
  id: string
  name: string
  status?: string
  online?: boolean
  color?: string | null
  icon?: string | null
  logo_url?: string | null
  facts?: { proxmox?: { installed: boolean } }
}
export type ScopeSite = {
  name: string
  title?: string | null
  color?: string | null
  icon?: string | null
  logo_url?: string | null
  imported: boolean
  serverId: string
}

type Scope = {
  server: ScopeServer | null
  site: ScopeSite | null
  setServer: (s: ScopeServer | null) => void
  setSite: (s: ScopeSite | null) => void
}

const Ctx = createContext<Scope | null>(null)

export function ShellScopeProvider({ children }: { children: ReactNode }) {
  const [server, setServer] = useState<ScopeServer | null>(null)
  const [site, setSite] = useState<ScopeSite | null>(null)
  const value = useMemo(() => ({ server, site, setServer, setSite }), [server, site])
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}

export function useShellScope() {
  return useContext(Ctx) ?? { server: null, site: null, setServer: () => {}, setSite: () => {} }
}

/** A server page tells the shell which server it shows (and forgets it on the way out). */
export function useProvideServerScope(server: ScopeServer | null) {
  const { setServer } = useShellScope()
  const key = server ? JSON.stringify(server) : ""
  useEffect(() => {
    setServer(server)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key])
  useEffect(() => () => setServer(null), [setServer])
}

/** A site page tells the shell which site (and its server) it shows. */
export function useProvideSiteScope(site: ScopeSite | null, server: ScopeServer | null) {
  const { setSite, setServer } = useShellScope()
  const key = JSON.stringify([site, server])
  useEffect(() => {
    setSite(site)
    if (server) setServer(server)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key])
  useEffect(
    () => () => {
      setSite(null)
      setServer(null)
    },
    [setSite, setServer],
  )
}

/** Which level the URL is at: /t/{team}/servers/{id}… is a server, /t/{team}/apps/{name}… a site. */
export function scopeOf(pathname: string, locale: string, teamSlug: string | undefined) {
  if (!teamSlug) return { kind: "team" as const }
  const base = `/${locale}/t/${teamSlug}`
  const rest = pathname.startsWith(base + "/") ? pathname.slice(base.length + 1).split("/") : []
  if (rest[0] === "servers" && rest[1] && rest[1] !== "new") return { kind: "server" as const, serverId: rest[1], section: rest[2] ?? "" }
  if (rest[0] === "apps" && rest[1] && rest[1] !== "new") return { kind: "site" as const, site: decodeURIComponent(rest[1]), section: rest[2] ?? "" }
  return { kind: "team" as const }
}
