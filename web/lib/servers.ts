"use client"

import { useEffect, useRef, useState } from "react"

import type { Role } from "@/lib/api"

/** Contract types for servers, operations and credentials (internal/api servers.go, operations.go, credentials.go). */

export type CheckStatus = "pass" | "warn" | "fail"
export type Check = { name: string; status: CheckStatus; detail: string }

export type Metrics = {
  cpu_percent: number
  mem_used_bytes: number
  mem_total_bytes: number
  disk_used_bytes: number
  disk_total_bytes: number
  uptime_seconds: number
  load1: number
  at: string
}

export type DockerFacts = {
  installed: boolean
  running: boolean
  version?: string
  storage_driver?: string
  backing_fs?: string
  error?: string
}

export type Facts = {
  hostname: string
  os_id: string
  os_version: string
  os_pretty: string
  arch: string
  kernel: string
  cpus: number
  mem_total_bytes: number
  systemd: boolean
  virtualization: string
  docker: DockerFacts
  proxmox?: { installed: boolean; version?: string }
  services?: DiscoveredService[]
}

export type DiscoveredService = {
  kind: "container" | "systemd" | "guest"
  name: string
  image?: string
  status: string
  ports?: string[]
  id?: string
  type?: string
  address?: string
  cpus?: number
  mem_bytes?: number
  disk_bytes?: number
}

/** What Hosbah manages the host as — mirrors agentproto.Facts.Platform. */
export function platformOf(f: Facts | undefined): "proxmox" | "docker" | "linux" | null {
  if (!f) return null
  if (f.proxmox?.installed) return "proxmox"
  if (f.docker?.running) return "docker"
  return "linux"
}

export type ServerStatus = "pending" | "provisioning" | "enrolled" | "failed"

export type Server = {
  id: string
  name: string
  provider: "byo" | "proxmox"
  kind: "byo" | "vm" | "lxc"
  status: ServerStatus
  online: boolean
  hostname: string | null
  ip_address: string | null
  os: string | null
  os_version: string | null
  arch: string | null
  kernel: string | null
  agent_version: string | null
  /** Appearance set in the server settings (all optional). */
  color?: string | null
  icon?: string | null
  logo_url?: string | null
  tunnel?: { kind: string; status: string; address?: string | null } | null
  error: string | null
  metrics: Metrics | null
  checks: Check[]
  facts?: Facts
  last_seen_at: string | null
  enrolled_at: string | null
  created_at: string
  enrollment?: { expires_at: string }
  /** Proxmox node/guest hierarchy (MH-574 A1): a guest's link to its node, and, on the node, its
   * discovered guests. */
  parent_server_id?: string | null
  parent_server_name?: string | null
  guest_vmid?: number | null
  guest_type?: "lxc" | "qemu" | null
  guests?: Guest[]
}

export type Guest = {
  vmid: string
  name: string
  type: "lxc" | "qemu"
  status: string
  address?: string
  cpus?: number
  mem_bytes?: number
  disk_bytes?: number
  server_id?: string | null
}

export type Install = { url: string; command: string; command_root: string; expires_at: string }

export type OperationStatus = "pending" | "sent" | "running" | "ok" | "failed" | "timeout"

export type Operation = {
  id: string
  server_id: string
  kind: string
  executor: "agent" | "control_plane"
  status: OperationStatus
  params: Record<string, unknown>
  result?: unknown
  error: string | null
  exit_code: number | null
  created_at: string
  started_at: string | null
  finished_at: string | null
}

export type LogLine = {
  seq: number
  event: "log" | "progress" | "status"
  stream?: string
  line?: string
  step?: number
  total?: number
  status?: string
  exit_code?: number
  error?: string
  at: string
}

export type ProviderOption = {
  key: "byo" | "proxmox"
  available: boolean
  reason?: string
  credentials: { id: string; name: string }[]
}

export type Credential = {
  id: string
  provider: "proxmox"
  name: string
  config: Record<string, string>
  last_validated_at: string | null
  created_at: string
}

export const PROVISION_STEPS = ["packages.base", "docker.install", "swap.ensure"] as const

/** Owners and admins manage servers; deployers may also run read-only operations. */
export function canOperate(role: Role | undefined): boolean {
  return role === "owner" || role === "admin" || role === "deployer"
}

export type Presence = "online" | "offline" | ServerStatus

export function presenceOf(s: Pick<Server, "status" | "online">): Presence {
  if (s.status !== "enrolled") return s.status
  return s.online ? "online" : "offline"
}

export function isTerminal(status: OperationStatus): boolean {
  return status === "ok" || status === "failed" || status === "timeout"
}

export function formatBytes(n: number | null | undefined): string {
  if (!n) return "0 B"
  const units = ["B", "KiB", "MiB", "GiB", "TiB"]
  let i = 0
  let v = n
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(i === 0 ? 0 : 1)} ${units[i]}`
}

export function formatUptime(seconds: number | null | undefined): string {
  if (!seconds) return "—"
  const d = Math.floor(seconds / 86400)
  const h = Math.floor((seconds % 86400) / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  return d > 0 ? `${d}d ${h}h` : h > 0 ? `${h}h ${m}m` : `${m}m`
}

export function percent(used: number, total: number): number {
  return total > 0 ? Math.min(100, Math.round((used / total) * 100)) : 0
}

/**
 * A Server-Sent Events subscription (same-origin, so the session cookie travels). Handlers are
 * read through a ref, so re-renders do not reconnect; changing `url` does. `null` url = off.
 */
export function useEventStream(url: string | null, handlers: Record<string, (data: unknown) => void>) {
  const ref = useRef(handlers)
  ref.current = handlers
  const [connected, setConnected] = useState(false)

  useEffect(() => {
    if (!url) return
    let es: EventSource | null = null
    let timer: ReturnType<typeof setTimeout> | undefined
    let finished = false
    const connect = () => {
      const src = new EventSource(url, { withCredentials: true })
      es = src
      src.onopen = () => setConnected(true)
      src.onerror = () => {
        setConnected(false)
        // The browser retries a dropped stream by itself, but a non-200 answer (the API was down and
        // the proxy said 502/500) closes it for good. Reconnect ourselves; handlers de-duplicate.
        if (src.readyState === EventSource.CLOSED && !finished) {
          timer = setTimeout(connect, 3000)
        }
      }
      for (const name of Object.keys(ref.current)) {
        src.addEventListener(name, ((e: MessageEvent) => {
          try {
            ref.current[name]?.(JSON.parse(e.data))
          } catch {
            // A malformed frame is skipped; the next one carries the full state again.
          }
          if (name === "done" || name === "gone") {
            finished = true
            src.close()
          }
        }) as EventListener)
      }
    }
    connect()
    return () => {
      finished = true
      clearTimeout(timer)
      es?.close()
      setConnected(false)
    }
    // Handler names are fixed per call site.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [url])

  return connected
}

/** i18n keys are dot-paths, so a kind or step ("docker.install") is looked up as "docker_install". */
export function opKey(k: string): string {
  return k.replace(/\./g, "_")
}

/** A proxy host of the Caddy proxy manager (GET /api/teams/{team}/proxy-hosts). */
export type ProxyHost = {
  id: string
  server_id: string
  server_name?: string
  hostname: string
  upstream: string
  tls: "auto" | "internal" | "off"
  enabled: boolean
  websockets: boolean
  verified: boolean
  verified_at: string | null
  verify_name: string
  verify_value: string
  created_at: string
  updated_at: string
}
