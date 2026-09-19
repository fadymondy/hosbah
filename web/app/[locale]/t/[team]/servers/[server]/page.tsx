"use client"

import Link from "next/link"
import { useParams, useRouter } from "next/navigation"
import { useEffect, useState } from "react"
import { AlertCircleIcon, LoaderIcon, PlayIcon, PlusIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { AppStatusBadge } from "@/components/app-status"
import { ServerTunnels } from "@/components/server-tunnels"
import { ConfirmButton } from "@/components/confirm-button"
import { CopyField } from "@/components/copy-field"
import { useErrorText } from "@/components/form-error"
import { ErrorBand, LoadingRows, Notice, SectionTitle } from "@/components/states"
import { OperationLog } from "@/components/op-log"
import { CheckBadge, OpStatusBadge, PresenceBadge } from "@/components/server-status"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Progress } from "@/components/ui/progress"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { apiFetch, ApiError } from "@/lib/api"
import { useDocumentTitle, useFormatDate, useI18n } from "@/lib/i18n"
import {
  canOperate,
  opKey,
  formatBytes,
  formatUptime,
  percent,
  platformOf,
  PROVISION_STEPS,
  useEventStream,
  type DiscoveredService,
  type ProxyHost,
  type Install,
  type Operation,
  type Server,
} from "@/lib/servers"
import type { App } from "@/lib/apps"
import { useTeam } from "@/lib/team"
import { cn } from "@/lib/utils"

export default function ServerPage() {
  const { server: serverId } = useParams<{ server: string }>()
  const { t, locale } = useI18n()
  const { team, manage } = useTeam()
  const router = useRouter()
  const fmt = useFormatDate()
  const errorText = useErrorText()
  const operate = canOperate(team.role)

  const [server, setServer] = useState<Server | null>(null)
  const [missing, setMissing] = useState(false)
  const [ops, setOps] = useState<Operation[]>([])
  const [selected, setSelected] = useState<string | null>(null)
  const [step, setStep] = useState<string>("")
  const [available, setAvailable] = useState<string | null>(null)
  const [install, setInstall] = useState<Install | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  useDocumentTitle(`${server?.name ?? t("nav.servers")} — ${team.name}`)

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
    operations: (d) => {
      const list = d as Operation[]
      setOps(list)
      setSelected((cur) => cur ?? list[0]?.id ?? null)
    },
    gone: () => setMissing(true),
  })

  async function run(body: Record<string, unknown>) {
    setBusy(true)
    setError(null)
    try {
      const op = await apiFetch<Operation>(`${path}/operations`, { method: "POST", body })
      setOps((prev) => (prev.some((o) => o.id === op.id) ? prev : [op, ...prev]))
      setSelected(op.id)
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  async function newInstall() {
    setError(null)
    try {
      const inst = await apiFetch<Install>(`${path}/enrollment`, { method: "POST" })
      setInstall(inst)
      setServer((s) => (s ? { ...s, enrollment: { expires_at: inst.expires_at } } : s))
    } catch (err) {
      setError(errorText(err))
    }
  }

  if (missing) {
    return (
      <PageBody title={t("nav.servers")} bleed>
        <Notice icon={<AlertCircleIcon />} title={t("errors.notFound")} />
      </PageBody>
    )
  }
  if (!server) {
    return (
      <PageBody title={<Skeleton className="h-8 w-48" />} bleed>
        <ErrorBand message={error} />
        <LoadingRows rows={4} />
      </PageBody>
    )
  }

  const enrolled = server.status === "enrolled"
  const m = server.metrics
  const selectedOp = ops.find((o) => o.id === selected) ?? null
  const docker = server.facts?.docker

  return (
    <PageBody
      title={<span dir="ltr">{server.name}</span>}
      description={`${t(`servers.providers.${server.provider}`)} · ${t(`servers.kinds.${server.kind}`)}`}
      actions={
        <>
          <span className="text-xs text-grid-muted" aria-live="polite">
            {connected ? t("servers.detail.live") : t("servers.detail.reconnecting")}
          </span>
          <PresenceBadge server={server} />
        </>
      }
      bleed
    >
      <ErrorBand message={error} />

      {server.status === "pending" ? (
        <Notice data-testid="pending" icon={<LoaderIcon className="animate-spin" />} title={t("servers.detail.pendingTitle")}>
          <span>{t("servers.detail.pendingHint")}</span>
          <span>
            {server.enrollment
              ? t("servers.detail.pendingExpires", { when: fmt(server.enrollment.expires_at) })
              : t("servers.detail.pendingExpired")}
          </span>
          {install ? (
            <div className="flex max-w-3xl flex-col gap-3">
              <CopyField value={install.command} />
              <CopyField value={install.command_root} />
            </div>
          ) : null}
          {manage ? (
            <div>
              <Button size="sm" variant="outline" onClick={newInstall}>
                {t("servers.detail.newInstall")}
              </Button>
            </div>
          ) : null}
        </Notice>
      ) : null}
      {server.status === "provisioning" ? (
        <Notice icon={<LoaderIcon className="animate-spin" />} title={t("servers.detail.provisioningTitle")}>
          {t("servers.detail.provisioningHint")}
        </Notice>
      ) : null}
      {server.status === "failed" ? (
        <Notice tone="danger" icon={<AlertCircleIcon />} title={t("servers.detail.failedTitle")}>
          <span dir="auto">{server.error}</span>
        </Notice>
      ) : null}

      {enrolled ? (
        <section aria-labelledby="metrics-h">
          <SectionTitle id="metrics-h" title={t("servers.detail.metrics")} />
          {m ? (
            <dl className="grid grid-cols-1 gap-px border-b border-line bg-line sm:grid-cols-3 lg:grid-cols-6" data-testid="metrics">
              <Meter label={t("servers.cpu")} value={Math.round(m.cpu_percent)} text={`${m.cpu_percent.toFixed(1)}%`} />
              <Meter
                label={t("servers.memory")}
                value={percent(m.mem_used_bytes, m.mem_total_bytes)}
                text={`${formatBytes(m.mem_used_bytes)} / ${formatBytes(m.mem_total_bytes)}`}
              />
              <Meter
                label={t("servers.disk")}
                value={percent(m.disk_used_bytes, m.disk_total_bytes)}
                text={`${formatBytes(m.disk_used_bytes)} / ${formatBytes(m.disk_total_bytes)}`}
              />
              <Stat label={t("servers.load")} value={m.load1.toFixed(2)} />
              <Stat label={t("servers.uptime")} value={formatUptime(m.uptime_seconds)} />
              <Stat label={t("servers.lastSeen")} value={server.last_seen_at ? fmt(server.last_seen_at) : t("servers.never")} small />
            </dl>
          ) : (
            <p className="border-b border-line px-6 py-4 text-sm text-grid-muted">{t("servers.detail.noMetrics")}</p>
          )}
        </section>
      ) : null}

      {server.status === "enrolled" ? (
        <div className="border-b border-line px-6 py-6 empty:hidden">
          <ServerTunnels serverId={server.id} manage={manage} />
        </div>
      ) : null}
      {server.status === "enrolled" ? (
        <Sites
          serverId={server.id}
          base={`/${locale}/t/${team.slug}`}
          manage={manage}
          imported={(server.facts?.services ?? []).filter((s) => s.kind === "guest" || s.kind === "container")}
        />
      ) : null}

      <div className="grid gap-px border-b border-line bg-line lg:grid-cols-2">
        <section aria-labelledby="facts-h" className="min-w-0 bg-grid-bg">
          <h2 id="facts-h" className="px-6 pt-5 pb-3 text-base font-medium text-grid-fg">
            {t("servers.detail.facts")}
          </h2>
          <dl className="grid grid-cols-[auto_1fr] gap-x-6 gap-y-2 border-t border-line px-6 py-4 text-sm">
            <Fact
              label={t("servers.discovered.platform")}
              value={
                platformOf(server.facts)
                  ? t(`servers.discovered.platforms.${platformOf(server.facts)}`) +
                    (server.facts?.proxmox?.version ? ` ${server.facts.proxmox.version}` : "")
                  : null
              }
            />
            <Fact label={t("servers.detail.hostname")} value={server.hostname} />
            <Fact label={t("servers.address")} value={server.ip_address} />
            <Fact label={t("servers.os")} value={server.facts?.os_pretty || (server.os ? `${server.os} ${server.os_version ?? ""}` : null)} />
            <Fact label={t("servers.detail.kernel")} value={server.kernel} />
            <Fact label={t("servers.detail.arch")} value={server.arch} />
            <Fact label={t("servers.agent")} value={server.agent_version} />
            <Fact
              label={t("servers.detail.docker")}
              value={
                !docker
                  ? null
                  : !docker.installed
                    ? t("servers.detail.dockerMissing")
                    : !docker.running
                      ? t("servers.detail.dockerStopped")
                      : `${docker.version} (${docker.storage_driver})`
              }
            />
          </dl>
        </section>
        <section aria-labelledby="checks-h" className="min-w-0 bg-grid-bg">
          <h2 id="checks-h" className="px-6 pt-5 pb-3 text-base font-medium text-grid-fg">
            {t("servers.detail.checks")}
          </h2>
          {server.checks.length === 0 ? (
            <p className="border-t border-line px-6 py-4 text-sm text-grid-muted">{t("servers.detail.checksEmpty")}</p>
          ) : (
            <ul className="flex flex-col divide-y divide-line border-t border-line text-sm" data-testid="checks">
              {server.checks.map((c) => (
                <li key={c.name} className="flex flex-wrap items-center gap-2 px-6 py-2.5">
                  <CheckBadge status={c.status} />
                  <span className="font-medium">{t(`servers.check.names.${c.name}`, undefined, c.name)}</span>
                  <span dir="auto" className="text-grid-muted">
                    {c.detail}
                  </span>
                </li>
              ))}
            </ul>
          )}
        </section>
      </div>

      {server.facts ? <Discovered services={server.facts.services ?? []} serverId={server.id} manage={manage} /> : null}

      <section aria-labelledby="ops-h">
        <SectionTitle id="ops-h" title={t("servers.ops.title")} hint={t("servers.ops.description")} />

        {!enrolled ? (
          server.status !== "provisioning" ? (
            <p className="border-b border-line px-6 py-3 text-sm text-grid-muted">{t("servers.ops.notEnrolled")}</p>
          ) : null
        ) : !operate ? (
          <p className="border-b border-line px-6 py-3 text-sm text-grid-muted">{t("servers.ops.readOnly")}</p>
        ) : (
          <div className="flex flex-wrap items-center gap-2 border-b border-line px-6 py-3" data-testid="op-actions">
            <Button size="sm" disabled={busy} onClick={() => run({ kind: "system.info" })}>
              <PlayIcon />
              {t("servers.ops.systemInfo")}
            </Button>
            <Button size="sm" variant="outline" disabled={busy} onClick={() => run({ kind: "docker.check" })}>
              <PlayIcon />
              {t("servers.ops.dockerCheck")}
            </Button>
            {manage ? (
              <>
                <Select value={step} onValueChange={(v) => setStep((v as string) ?? "")}>
                  <SelectTrigger className="w-48" aria-label={t("servers.ops.provision")}>
                    <SelectValue>{(v: string) => (v ? t(`servers.ops.steps.${opKey(v)}`) : t("servers.ops.chooseStep"))}</SelectValue>
                  </SelectTrigger>
                  <SelectContent>
                    {PROVISION_STEPS.map((s) => (
                      <SelectItem key={s} value={s}>
                        {t(`servers.ops.steps.${opKey(s)}`)}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <Button size="sm" variant="outline" disabled={busy || !step} onClick={() => run({ kind: "provision.step", step })}>
                  {t("servers.ops.run")}
                </Button>
                {available && server.agent_version && available !== server.agent_version ? (
                  <Button size="sm" variant="outline" disabled={busy} onClick={() => run({ kind: "agent.update" })}>
                    {t("servers.ops.update", { version: available })}
                  </Button>
                ) : null}
              </>
            ) : null}
          </div>
        )}
        {enrolled && !server.online ? (
          <p className="border-b border-line px-6 py-3 text-sm text-grid-muted">{t("servers.ops.offlineHint")}</p>
        ) : null}

        <div className="grid border-b border-line lg:grid-cols-[minmax(0,2fr)_minmax(0,3fr)]">
          <div className="min-w-0 border-b border-line lg:border-e lg:border-b-0 [&_tr>*:first-child]:ps-6">
            {ops.length === 0 ? (
              <p className="px-6 py-4 text-sm text-grid-muted">{t("servers.ops.empty")}</p>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t("servers.ops.kind")}</TableHead>
                    <TableHead>{t("servers.ops.status")}</TableHead>
                    <TableHead>{t("servers.ops.started")}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {ops.map((o) => (
                    <TableRow
                      key={o.id}
                      data-op={o.id}
                      aria-selected={o.id === selected}
                      className={cn("cursor-pointer", o.id === selected && "bg-grid-soft")}
                      onClick={() => setSelected(o.id)}
                    >
                      <TableCell>
                        <button type="button" className="text-start font-medium" onClick={() => setSelected(o.id)}>
                          {t(`servers.ops.kinds.${opKey(o.kind)}`, undefined, o.kind)}
                          {o.kind === "provision.step" && typeof o.params.step === "string" ? (
                            <span className="block text-xs font-normal text-grid-muted">
                              {t(`servers.ops.steps.${opKey(o.params.step)}`, undefined, o.params.step)}
                            </span>
                          ) : null}
                        </button>
                      </TableCell>
                      <TableCell>
                        <OpStatusBadge status={o.status} />
                      </TableCell>
                      <TableCell dir="ltr" className="text-start text-xs">
                        {fmt(o.created_at)}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </div>
          <OperationLog team={team.slug} serverId={server.id} op={selectedOp} />
        </div>
      </section>

      {manage ? (
        <div className="flex justify-end border-b border-line px-6 py-5">
          <ConfirmButton
            label={t("servers.detail.delete")}
            title={t("servers.detail.deleteTitle")}
            description={t(server.provider === "proxmox" ? "servers.detail.deleteHintProxmox" : "servers.detail.deleteHintByo", {
              name: server.name,
            })}
            confirmLabel={t("servers.detail.delete")}
            onConfirm={async () => {
              try {
                await apiFetch(path, { method: "DELETE" })
              } catch (err) {
                throw new Error(errorText(err))
              }
              router.push(`/${locale}/t/${team.slug}/servers`)
            }}
          />
        </div>
      ) : null}
    </PageBody>
  )
}

function Meter({ label, value, text }: { label: string; value: number; text: string }) {
  return (
    <div className="flex flex-col gap-2 bg-grid-bg px-6 py-4">
      <dt className="grid-micro">{label}</dt>
      <dd className="flex flex-col gap-2">
        <span dir="ltr" className="text-start font-mono text-sm tabular-nums">
          {text}
        </span>
        <Progress value={value} aria-label={label} />
      </dd>
    </div>
  )
}

function Stat({ label, value, small }: { label: string; value: string; small?: boolean }) {
  return (
    <div className="flex flex-col gap-2 bg-grid-bg px-6 py-4">
      <dt className="grid-micro">{label}</dt>
      <dd dir="ltr" className={cn("text-start font-mono tabular-nums", small ? "text-xs" : "text-sm")}>
        {value}
      </dd>
    </div>
  )
}

function Fact({ label, value }: { label: string; value: string | null | undefined }) {
  return (
    <>
      <dt className="text-grid-muted">{label}</dt>
      <dd dir="ltr" className="min-w-0 text-start font-mono text-xs leading-5 break-all">
        {value || "—"}
      </dd>
    </>
  )
}

/** The host port a container publishes ("0.0.0.0:8080->80/tcp" → 8080), if any. */
function publishedPort(ports?: string[]): number | null {
  for (const p of ports ?? []) {
    const m = /:(\d+)->/.exec(p)
    if (m) return Number(m[1])
  }
  return null
}

function Discovered({ services, serverId, manage }: { services: DiscoveredService[]; serverId: string; manage: boolean }) {
  const { t } = useI18n()
  const { team } = useTeam()
  const errorText = useErrorText()
  const [exposing, setExposing] = useState<string | null>(null)
  const [hostname, setHostname] = useState("")
  const [done, setDone] = useState<Record<string, string>>({})
  const [error, setError] = useState<string | null>(null)

  async function expose(name: string, port: number) {
    setError(null)
    try {
      await apiFetch(`/api/teams/${team.slug}/proxy-hosts`, {
        method: "POST",
        body: { server: serverId, hostname: hostname.trim(), upstream: `127.0.0.1:${port}`, tls: "auto" },
      })
      setDone((d) => ({ ...d, [name]: hostname.trim() }))
      setExposing(null)
      setHostname("")
    } catch (err) {
      setError(errorText(err))
    }
  }
  return (
    <section aria-labelledby="found-h" data-testid="discovered">
      <SectionTitle id="found-h" title={t("servers.discovered.title")} hint={t("servers.discovered.hint")} />
      <ErrorBand message={error} />
      {services.length === 0 ? (
        <p className="border-b border-line px-6 py-4 text-sm text-grid-muted">{t("servers.discovered.none")}</p>
      ) : (
        <ul className="flex flex-col divide-y divide-line border-b border-line text-sm">
          {services.map((s) => (
            <li key={`${s.kind}:${s.name}:${s.image ?? ""}`} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-6 py-2.5">
              <span className="grid-micro text-grid-muted">{t(`servers.discovered.kinds.${s.kind}`)}</span>
              <span dir="ltr" className="font-medium">
                {s.name}
              </span>
              {s.image ? (
                <span dir="ltr" className="font-mono text-xs text-grid-muted">
                  {s.image}
                </span>
              ) : null}
              <span className="ms-auto text-grid-muted">{s.status}</span>
              {s.ports?.length ? (
                <span dir="ltr" className="w-full text-start font-mono text-xs text-grid-muted">
                  {s.ports.join(", ")}
                </span>
              ) : null}
              {manage && s.kind === "container" && publishedPort(s.ports) ? (
                done[s.name] ? (
                  <span dir="ltr" className="w-full text-start text-xs text-grid-brand">
                    → {done[s.name]}
                  </span>
                ) : exposing === s.name ? (
                  <form
                    className="flex w-full flex-wrap gap-2"
                    onSubmit={(e) => {
                      e.preventDefault()
                      void expose(s.name, publishedPort(s.ports)!)
                    }}
                  >
                    <Input
                      dir="ltr"
                      required
                      autoFocus
                      className="h-8 max-w-xs"
                      placeholder="app.example.com"
                      aria-label={t("proxy.hostname")}
                      value={hostname}
                      onChange={(e) => setHostname(e.target.value)}
                    />
                    <Button type="submit" size="sm" disabled={!hostname.trim()}>
                      {t("servers.discovered.expose")}
                    </Button>
                    <Button type="button" size="sm" variant="outline" onClick={() => setExposing(null)}>
                      {t("common.cancel")}
                    </Button>
                  </form>
                ) : (
                  <Button size="sm" variant="outline" onClick={() => setExposing(s.name)}>
                    {t("servers.discovered.expose")}
                  </Button>
                )
              ) : null}
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}

/** The server's sites (apps) and proxy hosts: the way into each site's canvas. */
function Sites({
  serverId,
  base,
  manage,
  imported,
}: {
  serverId: string
  base: string
  manage: boolean
  imported: DiscoveredService[]
}) {
  const { t } = useI18n()
  const { team } = useTeam()
  const [apps, setApps] = useState<App[] | null>(null)
  const [proxies, setProxies] = useState<ProxyHost[]>([])

  useEffect(() => {
    apiFetch<{ apps: App[] }>(`/api/teams/${team.slug}/apps`)
      .then((r) => setApps((r.apps ?? []).filter((a) => a.server_id === serverId)))
      .catch(() => setApps([]))
    apiFetch<{ proxy_hosts: ProxyHost[] }>(`/api/teams/${team.slug}/proxy-hosts?server=${serverId}`)
      .then((r) => setProxies(r.proxy_hosts ?? []))
      .catch(() => setProxies([]))
  }, [team.slug, serverId])

  return (
    <section aria-labelledby="sites-h" data-testid="server-sites">
      <SectionTitle
        id="sites-h"
        title={t("sites.title")}
        hint={t("sites.hint")}
        action={
          manage ? (
            <Button size="sm" nativeButton={false} render={<Link href={`${base}/apps/new?server=${serverId}`} />}>
              <PlusIcon />
              {t("sites.new")}
            </Button>
          ) : null
        }
      />
      {apps === null ? (
        <LoadingRows rows={2} />
      ) : apps.length === 0 && proxies.length === 0 && imported.length === 0 ? (
        <p className="border-b border-line px-6 py-4 text-sm text-grid-muted">{t("sites.empty")}</p>
      ) : (
        <ul className="grid gap-px border-b border-line bg-line sm:grid-cols-2 lg:grid-cols-3">
          {apps.map((a) => (
            <li key={a.id} className="bg-grid-bg">
              <Link
                href={`${base}/servers/${serverId}/sites/${a.id}`}
                className="flex h-full flex-col gap-2 px-6 py-4 hover:bg-grid-soft"
              >
                <span className="flex items-center justify-between gap-2">
                  <span dir="ltr" className="font-medium text-grid-fg">
                    {a.name}
                  </span>
                  <AppStatusBadge status={a.status} />
                </span>
                <span dir="ltr" className="truncate text-start font-mono text-xs text-grid-muted">
                  {a.domains?.[0]?.hostname ?? a.repo_url}
                </span>
              </Link>
            </li>
          ))}
          {imported.map((s) => (
            <li key={`${s.kind}:${s.id ?? s.name}`} data-testid="imported-site" className="bg-grid-bg">
              <div className="flex h-full flex-col gap-2 bg-grid-soft/50 px-6 py-4">
                <span className="flex items-center justify-between gap-2">
                  <span dir="ltr" className="font-medium text-grid-fg">
                    {s.name}
                  </span>
                  <span className="grid-micro text-grid-muted">
                    {s.kind === "guest" ? `${(s.type ?? "").toUpperCase()} ${s.id ?? ""}` : t("servers.discovered.kinds.container")}
                  </span>
                </span>
                <span dir="ltr" className="text-start font-mono text-xs text-grid-muted">
                  {[s.address, s.cpus ? `${s.cpus} vCPU` : "", s.mem_bytes ? formatBytes(s.mem_bytes) : "", s.disk_bytes ? formatBytes(s.disk_bytes) : "", s.image && s.kind === "container" ? s.image : ""]
                    .filter(Boolean)
                    .join(" · ")}
                </span>
                <span className="text-xs text-grid-muted">{s.status}</span>
              </div>
            </li>
          ))}
          {proxies.map((p) => (
            <li key={p.id} className="bg-grid-bg">
              <Link href={`${base}/proxy-hosts`} className="flex h-full flex-col gap-2 px-6 py-4 hover:bg-grid-soft">
                <span className="flex items-center justify-between gap-2">
                  <span dir="ltr" className="font-medium text-grid-fg">
                    {p.hostname}
                  </span>
                  <span className="grid-micro text-grid-muted">{t("sites.proxyHost")}</span>
                </span>
                <span dir="ltr" className="truncate text-start font-mono text-xs text-grid-muted">
                  → {p.upstream}
                </span>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
