"use client"

import type { StatusTone } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useMemo, useState } from "react"

import { useErrorText } from "@/components/form-error"
import { apiFetch } from "@/lib/api"
import type { DNSZone } from "@/lib/dns-zones"
import { useI18n } from "@/lib/i18n"
import type { ProxyHost, Server } from "@/lib/servers"

interface TeamDomain {
  id: string
  created_at: string
  verified_at: string | null
  hostname: string
  tls: "auto" | "internal" | "off"
  verified: boolean
  verify_name: string
  verify_value: string
  app_id: string
  app_name: string
  server_id: string
}

export interface Cert {
  hostname: string
  ok: boolean
  days_left?: number
  error?: string
}

/** One hostname, whether it routes to an app or is a proxy host. */
export interface Host {
  key: string
  kind: "app" | "proxy"
  hostname: string
  tls: "auto" | "internal" | "off"
  verified: boolean
  verifyName: string
  verifyValue: string
  enabled: boolean
  target: string
  targetHref: string
  serverId: string
  verifyPath: string
  manageHref: string
  activeAt: string
}

export type Health = "attention" | "ok" | "off"

/** A main domain: the hostnames under it and the Cloudflare zone holding its DNS, if the team has one. */
export interface Domain {
  name: string
  hosts: Host[]
  zone: DNSZone | null
  health: Health
  activeAt: string
}

/** Second-level labels under a country code that are part of the registrable domain (example.com.eg). */
const SECOND_LEVEL = new Set(["com", "co", "net", "org", "gov", "edu", "ac", "sch", "me", "ltd", "plc"])

/** The main (registrable) domain of a hostname: api.eu.example.com → example.com. */
export function apexOf(hostname: string) {
  const parts = hostname.replace(/^\*\./, "").toLowerCase().split(".")
  const n = parts.length >= 3 && parts.at(-1)!.length === 2 && SECOND_LEVEL.has(parts.at(-2)!) ? 3 : 2
  return parts.slice(-n).join(".")
}

/**
 * Every domain of the team: the app domains and proxy hosts grouped by main domain, joined with the
 * Cloudflare zones the team's credentials see (a zone with no hostname yet is still a domain).
 */
export function useTeamDomains(team: string, base: string) {
  const errorText = useErrorText()
  const [hosts, setHosts] = useState<Host[] | null>(null)
  const [zones, setZones] = useState<DNSZone[]>([])
  const [certs, setCerts] = useState<Map<string, Cert>>(new Map())
  const [servers, setServers] = useState<Server[]>([])
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      const [d, p, s, c, z] = await Promise.all([
        apiFetch<{ domains: TeamDomain[] }>(`/api/teams/${team}/domains`),
        apiFetch<{ proxy_hosts: ProxyHost[] }>(`/api/teams/${team}/proxy-hosts`).catch(() => ({ proxy_hosts: [] as ProxyHost[] })),
        apiFetch<{ servers: Server[] }>(`/api/teams/${team}/servers`).catch(() => ({ servers: [] as Server[] })),
        apiFetch<{ certificates: Cert[] }>(`/api/teams/${team}/certificates`).catch(() => ({ certificates: [] as Cert[] })),
        apiFetch<{ zones: DNSZone[] }>(`/api/teams/${team}/dns/zones`).catch(() => ({ zones: [] as DNSZone[] })),
      ])
      const app = (name: string) => `${base}/apps/${encodeURIComponent(name)}`
      setHosts([
        ...(d.domains ?? []).map<Host>((x) => ({
          key: `a-${x.id}`,
          kind: "app",
          hostname: x.hostname,
          tls: x.tls,
          verified: x.verified,
          verifyName: x.verify_name,
          verifyValue: x.verify_value,
          enabled: true,
          target: x.app_name,
          targetHref: app(x.app_name),
          serverId: x.server_id,
          verifyPath: `/api/teams/${team}/apps/${x.app_id}/domains/${x.id}/verify`,
          manageHref: `${app(x.app_name)}/dns`,
          activeAt: x.verified_at ?? x.created_at,
        })),
        ...(p.proxy_hosts ?? []).map<Host>((x) => ({
          key: `p-${x.id}`,
          kind: "proxy",
          hostname: x.hostname,
          tls: x.tls as Host["tls"],
          verified: x.verified,
          verifyName: x.verify_name,
          verifyValue: x.verify_value,
          enabled: x.enabled,
          target: x.upstream,
          targetHref: "",
          serverId: x.server_id,
          verifyPath: `/api/teams/${team}/proxy-hosts/${x.id}/verify`,
          manageHref: `${base}/servers/${x.server_id}/proxy`,
          activeAt: x.updated_at ?? x.created_at,
        })),
      ])
      setServers(s.servers ?? [])
      setCerts(new Map((c.certificates ?? []).map((x) => [x.hostname, x])))
      setZones(z.zones ?? [])
      setError(null)
    } catch (err) {
      setError(errorText(err))
      setHosts((x) => x ?? [])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [team, base])

  useEffect(() => {
    void load()
    const id = setInterval(load, 60_000)
    return () => clearInterval(id)
  }, [load])

  const health = useCallback(
    (h: Host): Health => {
      if (!h.enabled) return "off"
      const c = certs.get(h.hostname)
      if (!h.verified && h.tls === "auto") return "attention"
      if (c && (!c.ok || (c.days_left !== undefined && c.days_left < 14))) return "attention"
      return "ok"
    },
    [certs],
  )

  const domains = useMemo<Domain[] | null>(() => {
    if (hosts === null) return null
    const by = new Map<string, Host[]>()
    for (const z of zones) by.set(z.name.toLowerCase(), [])
    for (const h of hosts) {
      const a = apexOf(h.hostname)
      by.set(a, [...(by.get(a) ?? []), h])
    }
    return [...by].map(([name, hs]) => {
      hs.sort(
        (x, y) => x.hostname.replace(/^\*\./, "").length - y.hostname.replace(/^\*\./, "").length || x.hostname.localeCompare(y.hostname),
      )
      const states = hs.map(health)
      return {
        name,
        hosts: hs,
        zone: zones.find((z) => z.name.toLowerCase() === name) ?? null,
        health: states.includes("attention") ? "attention" : hs.length && states.every((s) => s === "off") ? "off" : "ok",
        activeAt: hs.reduce((m, h) => (h.activeAt > m ? h.activeAt : m), ""),
      }
    })
  }, [hosts, zones, health])

  return { domains, servers, certs, error, setError, load, health }
}

/** The certificate state of a hostname, as a Nasaq status. */
export function useTlsOf(certs: Map<string, Cert>) {
  const { t } = useI18n()
  return useCallback(
    (h: Host): [StatusTone, string] => {
      if (!h.enabled) return ["neutral", t("proxy.disabled")]
      const c = certs.get(h.hostname)
      if (h.tls !== "auto") return ["neutral", t(`proxy.tlsModes.${h.tls}`)]
      if (!h.verified) return ["warning", t("domains2.awaitingVerify")]
      if (!c) return ["info", t("domains2.certPending")]
      if (!c.ok) return ["danger", t("domains2.certFailing")]
      if (c.days_left !== undefined && c.days_left < 14) return ["danger", t("domains2.certExpires", { n: c.days_left })]
      return ["success", t("domains2.certOk", { n: c.days_left ?? "—" })]
    },
    [certs, t],
  )
}
