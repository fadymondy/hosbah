"use client"

import Link from "next/link"
import { useEffect, useMemo, useState, type ReactNode } from "react"
import {
  ArrowUpRightIcon,
  BoxIcon,
  CheckCircle2Icon,
  CogIcon,
  ContainerIcon,
  GlobeIcon,
  HardDriveIcon,
  MemoryStickIcon,
  MonitorIcon,
  NetworkIcon,
  PlugIcon,
  SearchIcon,
  XIcon,
} from "lucide-react"

import { useErrorText } from "@/components/form-error"
import { ErrorBand, SectionTitle } from "@/components/states"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { apiFetch } from "@/lib/api"
import type { App } from "@/lib/apps"
import { useI18n } from "@/lib/i18n"
import { formatBytes, type DiscoveredService } from "@/lib/servers"
import { useTeam } from "@/lib/team"
import { cn } from "@/lib/utils"

type Kind = DiscoveredService["kind"]
const KINDS: Kind[] = ["container", "guest", "systemd"]
const KIND_ICON: Record<Kind, ReactNode> = {
  container: <ContainerIcon />,
  guest: <MonitorIcon />,
  systemd: <CogIcon />,
}

/** The host port a container publishes ("0.0.0.0:8080->80/tcp" → 8080), if any. */
function publishedPort(ports?: string[]): number | null {
  for (const p of ports ?? []) {
    const m = /:(\d+)->/.exec(p)
    if (m) return Number(m[1])
  }
  return null
}

const running = (s: string) => /^(running|active|up)/i.test(s)

/**
 * Everything the agent found on the server: containers, Proxmox guests and system services,
 * grouped by kind with counts, searchable. Workloads already imported link to their record;
 * a container that publishes a port can be exposed behind a hostname (a proxy host).
 */
export function Discovered({ services, serverId, manage }: { services: DiscoveredService[]; serverId: string; manage: boolean }) {
  const { t, locale } = useI18n()
  const { team } = useTeam()
  const base = `/${locale}/t/${team.slug}`
  const [q, setQ] = useState("")
  const [kind, setKind] = useState<Kind | "">("")
  const [apps, setApps] = useState<App[]>([])

  useEffect(() => {
    apiFetch<{ apps: App[] }>(`/api/teams/${team.slug}/apps`)
      .then((r) => setApps((r.apps ?? []).filter((a) => a.server_id === serverId)))
      .catch(() => setApps([]))
  }, [team.slug, serverId])
  const byOrigin = useMemo(() => new Map(apps.filter((a) => a.origin_key).map((a) => [a.origin_key as string, a])), [apps])

  const counts = useMemo(() => {
    const c: Record<Kind, { all: number; up: number }> = { container: { all: 0, up: 0 }, guest: { all: 0, up: 0 }, systemd: { all: 0, up: 0 } }
    for (const s of services) {
      c[s.kind].all++
      if (running(s.status)) c[s.kind].up++
    }
    return c
  }, [services])

  const needle = q.trim().toLowerCase()
  const shown = services.filter(
    (s) => (!kind || s.kind === kind) && (!needle || [s.name, s.image, s.address, ...(s.ports ?? [])].some((v) => v?.toLowerCase().includes(needle))),
  )

  return (
    <section aria-labelledby="found-h" data-testid="discovered">
      <SectionTitle id="found-h" title={t("servers.discovered.title")} hint={t("servers.discovered.hint")} />

      <div className="grid grid-cols-1 gap-px border-b border-line bg-line sm:grid-cols-3">
        {KINDS.map((k) => (
          <button
            key={k}
            type="button"
            aria-pressed={kind === k}
            onClick={() => setKind(kind === k ? "" : k)}
            className={cn("flex items-center gap-4 bg-grid-bg px-6 py-4 text-start hover:bg-grid-soft", kind === k && "bg-grid-soft")}
          >
            <span className="flex size-10 shrink-0 items-center justify-center border border-line bg-background text-grid-brand [&_svg]:size-5">
              {KIND_ICON[k]}
            </span>
            <span className="flex flex-col">
              <span className="text-2xl font-medium tabular-nums text-grid-fg">{counts[k].all}</span>
              <span className="text-xs text-grid-muted">
                {t(`servers.discovered.plural.${k}`)} · {t("servers.discovered.running", { n: counts[k].up })}
              </span>
            </span>
          </button>
        ))}
      </div>

      {services.length === 0 ? (
        <p className="flex items-center gap-2 border-b border-line px-6 py-8 text-sm text-grid-muted">
          <BoxIcon className="size-4" />
          {t("servers.discovered.none")}
        </p>
      ) : (
        <>
          <div className="flex flex-wrap items-center gap-2 border-b border-line px-6 py-3">
            <div className="relative w-full max-w-xs">
              <SearchIcon className="pointer-events-none absolute start-2.5 top-1/2 size-4 -translate-y-1/2 text-grid-muted" />
              <Input
                type="search"
                value={q}
                onChange={(e) => setQ(e.target.value)}
                placeholder={t("servers.discovered.search")}
                aria-label={t("servers.discovered.search")}
                className="ps-8"
              />
            </div>
            {kind ? (
              <Button variant="ghost" size="sm" onClick={() => setKind("")}>
                <XIcon />
                {t(`servers.discovered.plural.${kind}`)}
              </Button>
            ) : null}
          </div>
          {shown.length === 0 ? (
            <p className="border-b border-line px-6 py-8 text-center text-sm text-grid-muted">{t("home.noMatch")}</p>
          ) : (
            <ul className="grid gap-4 border-b border-line px-6 py-6 md:grid-cols-2 xl:grid-cols-3">
              {shown.map((s) => (
                <Card
                  key={`${s.kind}:${s.id ?? s.name}:${s.image ?? ""}`}
                  s={s}
                  serverId={serverId}
                  manage={manage}
                  record={byOrigin.get(s.kind === "guest" ? `guest:${s.id}` : s.name) ?? null}
                  base={base}
                />
              ))}
            </ul>
          )}
        </>
      )}
    </section>
  )
}

function Card({
  s,
  serverId,
  manage,
  record,
  base,
}: {
  s: DiscoveredService
  serverId: string
  manage: boolean
  record: App | null
  base: string
}) {
  const { t } = useI18n()
  const { team } = useTeam()
  const errorText = useErrorText()
  const [exposing, setExposing] = useState(false)
  const [hostname, setHostname] = useState("")
  const [done, setDone] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const port = s.kind === "container" ? publishedPort(s.ports) : null
  const up = running(s.status)

  async function expose() {
    setError(null)
    try {
      await apiFetch(`/api/teams/${team.slug}/proxy-hosts`, {
        method: "POST",
        body: { server: serverId, hostname: hostname.trim(), upstream: `127.0.0.1:${port}`, tls: "auto" },
      })
      setDone(hostname.trim())
      setExposing(false)
    } catch (err) {
      setError(errorText(err))
    }
  }

  return (
    <li className="flex min-w-0 flex-col border border-line bg-grid-card">
      <div className="flex items-start gap-3 px-4 pt-4 pb-3">
        <span className="flex size-9 shrink-0 items-center justify-center border border-line bg-background text-grid-brand [&_svg]:size-4">
          {KIND_ICON[s.kind]}
        </span>
        <span className="min-w-0 flex-1">
          <span dir="ltr" className="block truncate text-start font-medium text-grid-fg" title={s.name}>
            {s.name}
          </span>
          <span className="flex items-center gap-1.5 text-xs text-grid-muted">
            <span aria-hidden className={cn("size-2", up ? "bg-[var(--grid-ok,#3fb950)]" : "bg-grid-muted opacity-60")} />
            {t(`servers.discovered.kinds.${s.kind}`)}
            {s.kind === "guest" && s.type ? ` · ${s.type.toUpperCase()} ${s.id ?? ""}` : ""} · {s.status}
          </span>
        </span>
        {record ? (
          <Link
            href={`${base}/apps/${record.name}`}
            className="inline-flex shrink-0 items-center gap-1 border border-line px-2 py-0.5 text-xs text-grid-brand hover:bg-grid-soft"
          >
            <CheckCircle2Icon className="size-3" />
            {t("servers.discovered.managed")}
          </Link>
        ) : null}
      </div>

      <div className="flex flex-col gap-2 border-t border-line px-4 py-3 text-xs">
        {s.image ? (
          <Row icon={<BoxIcon />}>
            <span dir="ltr" className="truncate font-mono text-grid-muted" title={s.image}>
              {s.image}
            </span>
          </Row>
        ) : null}
        {s.address ? (
          <Row icon={<GlobeIcon />}>
            <span dir="ltr" className="font-mono text-grid-fg">
              {s.address}
            </span>
          </Row>
        ) : null}
        {s.cpus || s.mem_bytes || s.disk_bytes ? (
          <Row icon={<MemoryStickIcon />}>
            <span dir="ltr" className="font-mono text-grid-fg">
              {[s.cpus ? `${s.cpus} vCPU` : "", s.mem_bytes ? formatBytes(s.mem_bytes) : ""].filter(Boolean).join(" · ")}
            </span>
            {s.disk_bytes ? (
              <span dir="ltr" className="ms-2 inline-flex items-center gap-1 font-mono text-grid-fg">
                <HardDriveIcon className="size-3 text-grid-muted" />
                {formatBytes(s.disk_bytes)}
              </span>
            ) : null}
          </Row>
        ) : null}
        {s.ports?.length ? (
          <Row icon={<PlugIcon />}>
            <span className="flex flex-wrap gap-1">
              {s.ports.map((p) => (
                <span key={p} dir="ltr" className="border border-line bg-background px-1.5 py-0.5 font-mono text-[11px] text-grid-fg">
                  {p}
                </span>
              ))}
            </span>
          </Row>
        ) : null}
      </div>

      {manage && port ? (
        <div className="mt-auto border-t border-line px-4 py-3">
          {error ? <ErrorBand message={error} /> : null}
          {done ? (
            <span dir="ltr" className="inline-flex items-center gap-1.5 text-xs text-grid-brand">
              <NetworkIcon className="size-3.5" />
              {done}
            </span>
          ) : exposing ? (
            <form
              className="flex flex-wrap gap-2"
              onSubmit={(e) => {
                e.preventDefault()
                void expose()
              }}
            >
              <Input
                dir="ltr"
                required
                autoFocus
                className="h-8 min-w-0 flex-1"
                placeholder="app.example.com"
                aria-label={t("proxy.hostname")}
                value={hostname}
                onChange={(e) => setHostname(e.target.value)}
              />
              <Button type="submit" size="sm" disabled={!hostname.trim()}>
                {t("servers.discovered.expose")}
              </Button>
              <Button type="button" size="sm" variant="ghost" onClick={() => setExposing(false)} aria-label={t("common.cancel")}>
                <XIcon />
              </Button>
            </form>
          ) : (
            <Button size="sm" variant="outline" onClick={() => setExposing(true)}>
              <ArrowUpRightIcon />
              {t("servers.discovered.exposePort", { port })}
            </Button>
          )}
        </div>
      ) : null}
    </li>
  )
}

function Row({ icon, children }: { icon: ReactNode; children: ReactNode }) {
  return (
    <div className="flex min-w-0 items-center gap-2 [&>svg]:size-3.5 [&>svg]:shrink-0 [&>svg]:text-grid-muted">
      {icon}
      {children}
    </div>
  )
}
