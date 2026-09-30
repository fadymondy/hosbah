"use client"

import { Badge, Button, EmptyState, Input, Table, TableBody, TableCell, TableHead, TableHeader, TableRow, toast } from "@fadymondy/nasaq/web"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { useCallback, useEffect, useRef, useState, type FormEvent } from "react"
import {
  ArchiveIcon,
  CogIcon,
  DatabaseIcon,
  LinkIcon,
  PlayIcon,
  PlusIcon,
  RefreshCwIcon,
  RotateCwIcon,
  ScrollTextIcon,
  SearchIcon,
  SquareIcon,
  Trash2Icon,
  UploadIcon,
} from "lucide-react"

import { DomainsPanel } from "@/components/apps/domains"
import { RuntimeLogsPanel } from "@/components/apps/runtime-logs"
import { SettingsPanel } from "@/components/apps/settings"
import { ConfirmButton } from "@/components/confirm-button"
import { useErrorText } from "@/components/form-error"
import { useTimeAgo } from "@/components/home/sparkline"
import {
  DangerZone,
  GuestBackup,
  GuestSnapshots,
  PowerControls,
  RecreateContainer,
  ResizeGuest,
  useWorkloadActions,
  WorkloadLogs,
} from "@/components/imported-controls"
import { ResourcesCard } from "@/components/resources-panel"
import { SERVER_COLORS, SERVER_ICONS, ServerMark } from "@/components/server-mark"
import { ListSearch, matches } from "@/components/server-tabs/list-tools"
import { useLoadedSite } from "@/components/site/context"
import { SiteBuildConfig } from "@/components/site/build-config"
import { SitePHPConfig } from "@/components/site/php-config"
import { ErrorBand, LoadingRows, SectionTitle, SettingsRow, TableBand } from "@/components/states"





import { apiFetch, ApiError } from "@/lib/api"
import { isImported, type App, type EnvVar } from "@/lib/apps"
import { humanBytes, type DataService } from "@/lib/databases"
import { useI18n } from "@/lib/i18n"
import { canOperate } from "@/lib/servers"
import { useTeam } from "@/lib/team"
import { cn } from "@/lib/utils"

/** The first published host port of a container ("0.0.0.0:8081->80/tcp" → 8081). */
function firstPort(ports?: string[]): number | undefined {
  for (const p of ports ?? []) {
    const m = /:(\d+)->/.exec(p)
    if (m) return Number(m[1])
  }
  return undefined
}

/**
 * Whether a data service can be attached to a site from here: one Hosbah runs (not imported),
 * running, and a database or cache (a MinIO service is reached through buckets instead).
 */
export function attachable(s: DataService) {
  return !isImported(s) && s.kind !== "minio" && s.status === "running"
}

/**
 * Attach a data service to an app: the API injects a sealed DATABASE_URL / REDIS_URL, applied on the
 * next deploy or restart. Returns the variable's name.
 */
export async function attachDataService(team: string, service: DataService, app: string) {
  const r = await apiFetch<{ variable: string }>(`/api/teams/${team}/data-services/${service.name}/attach`, { method: "POST", body: { app } })
  return r.variable
}

/** Connected databases: the data services on the site's server, attached with one click. */
export function SiteDatabases() {
  const { t, locale } = useI18n()
  const { team, manage } = useTeam()
  const errorText = useErrorText()
  const { app, path } = useLoadedSite()
  const base = `/${locale}/t/${team.slug}`
  const [services, setServices] = useState<DataService[] | null>(null)
  const [vars, setVars] = useState<string[]>([])
  const [q, setQ] = useState("")
  const [busy, setBusy] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  const load = () => {
    apiFetch<{ services: DataService[] }>(`/api/teams/${team.slug}/data-services`)
      .then((r) => setServices((r.services ?? []).filter((s) => s.server_id === app.server_id)))
      .catch(() => setServices([]))
    apiFetch<{ variables: EnvVar[] }>(`${path}/env`)
      .then((r) => setVars((r.variables ?? []).map((e) => e.name)))
      .catch(() => setVars([]))
  }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(load, [team.slug, app.server_id, path])

  async function attach(s: DataService) {
    setBusy(s.id)
    setError(null)
    try {
      toast.success(t("site.attached", { variable: await attachDataService(team.slug, s, app.name) }))
      load()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(null)
    }
  }

  const shown = (services ?? []).filter((s) => matches(q, s.name, s.kind, s.version, s.status))
  const injected = vars.filter((v) => /(DATABASE|REDIS|MYSQL|POSTGRES|S3|MINIO)_?URL$/.test(v))

  return (
    <section aria-labelledby="sdb-h">
      <SectionTitle
        id="sdb-h"
        title={t("site.nav.databases")}
        hint={t("site.databasesHint")}
        action={
          manage ? (
            <Button variant="primary" size="sm" nativeButton={false} render={<Link href={`${base}/servers/${app.server_id}/databases`} />}>
              <PlusIcon />
              {t("site.newDatabase")}
            </Button>
          ) : null
        }
      />
      <ErrorBand message={error} />
      {injected.length ? (
        <p className="flex flex-wrap items-center gap-2 border-b border-border px-6 py-3 text-xs text-muted-foreground">
          <LinkIcon className="size-3.5" />
          {t("site.injected")}
          {injected.map((v) => (
            <code key={v} dir="ltr" className="border border-border bg-card px-1.5 py-0.5 font-mono text-foreground">
              {v}
            </code>
          ))}
        </p>
      ) : null}
      {services === null ? (
        <LoadingRows rows={2} />
      ) : services.length === 0 ? (
        <EmptyState className="border-b" icon={DatabaseIcon} title={t("databases.emptyTitle")} description={t("site.databasesEmpty")} />
      ) : (
        <>
          <ListSearch value={q} onChange={setQ} placeholder={t("lists.searchDatabases")} shown={shown.length} total={services.length} />
          <TableBand>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("databases.name")}</TableHead>
                  <TableHead>{t("databases.kind")}</TableHead>
                  <TableHead>{t("databases.status")}</TableHead>
                  <TableHead className="text-end">{t("common.actions")}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {shown.map((s) => (
                  <TableRow key={s.id}>
                    <TableCell>
                      <Link href={`${base}/databases/${s.name}`} className="font-medium hover:underline" dir="ltr">
                        {s.name}
                      </Link>
                    </TableCell>
                    <TableCell dir="ltr">
                      {s.kind} {s.version}
                    </TableCell>
                    <TableCell>
                      <Badge variant={s.status === "running" ? "brand" : "outline"}>{t(`databases.statuses.${s.status}`, undefined, s.status)}</Badge>
                    </TableCell>
                    <TableCell>
                      <div className="flex flex-wrap justify-end gap-2">
                        <Button size="sm" variant="secondary" nativeButton={false} render={<Link href={`${base}/databases/${s.name}`} />}>
                          {t("lists.manage")}
                        </Button>
                        {manage && !isImported(s) && s.kind !== "minio" ? (
                          <Button variant="primary" size="sm" disabled={busy === s.id || !attachable(s)} onClick={() => void attach(s)}>
                            <LinkIcon />
                            {t("site.attach")}
                          </Button>
                        ) : null}
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </TableBand>
        </>
      )}
      <ConnectedServices />
    </section>
  )
}

type AppServiceLink = { variable: string; service_kind: "shared" | "data"; service_id: string; source: "auto" | "manual" }

/**
 * Connected services: what this site's env variables were detected pointing at — shared
 * infrastructure (internal/services) and data services alike, keyed by variable name (see
 * internal/dbsvc/links.go). "Detect" re-parses the env now instead of waiting for the next save.
 */
function ConnectedServices() {
  const { t } = useI18n()
  const { manage } = useTeam()
  const errorText = useErrorText()
  const { path } = useLoadedSite()
  const [links, setLinks] = useState<AppServiceLink[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const load = () => {
    apiFetch<{ links: AppServiceLink[] }>(`${path}/service-links`)
      .then((r) => setLinks(r.links ?? []))
      .catch(() => setLinks([]))
  }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(load, [path])

  async function detect() {
    setBusy(true)
    setError(null)
    try {
      const r = await apiFetch<{ links: AppServiceLink[] }>(`${path}/service-links/detect`, { method: "POST" })
      setLinks(r.links ?? [])
      toast.success(t("site.detectedServices", { count: r.links?.length ?? 0 }))
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  if (links !== null && links.length === 0 && !manage) return null

  return (
    <div className="border-t border-border px-6 py-4">
      <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
        <div>
          <h3 className="text-sm font-medium">{t("site.connectedServices")}</h3>
          <p className="text-xs text-muted-foreground">{t("site.connectedServicesHint")}</p>
        </div>
        {manage ? (
          <Button size="sm" variant="secondary" onClick={() => void detect()} disabled={busy}>
            <SearchIcon />
            {t("site.detectServices")}
          </Button>
        ) : null}
      </div>
      <ErrorBand message={error} />
      {links === null ? null : links.length === 0 ? (
        <p className="text-sm text-muted-foreground">{t("site.connectedServicesEmpty")}</p>
      ) : (
        <ul className="flex flex-wrap gap-2">
          {links.map((l) => (
            <li key={l.variable} className="flex items-center gap-2 rounded-md border border-border bg-card px-3 py-1.5 text-xs">
              <code dir="ltr" className="font-mono text-foreground">
                {l.variable}
              </code>
              <Badge variant={l.service_kind === "shared" ? "neutral" : "outline"}>{l.service_kind}</Badge>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

type ProcessStatus = {
  process: string
  container: string
  state: string
  status?: string
  started_at?: string
  restart_count: number
  cpu_percent?: number
  mem_bytes?: number
  mem_limit_bytes?: number
  image?: string
  deployment?: string
}
type ProcessesOut = { processes: ProcessStatus[]; operation: string }

const PROCESS_BADGE: Record<string, "success" | "neutral" | "outline"> = {
  running: "success",
  restarting: "neutral",
  created: "neutral",
  paused: "neutral",
}

/**
 * Workers: web and every worker process of the app's live release, with live container state,
 * uptime, restarts and resource use. Restart works on any process; stop and start are refused for
 * web (stopping it takes the site down) so they only show on workers.
 */
export function SiteWorkers() {
  const { t, locale } = useI18n()
  const { team } = useTeam()
  const operate = canOperate(team.role)
  const ago = useTimeAgo(locale)
  const errorText = useErrorText()
  const { detail, app, path } = useLoadedSite()
  const logsBase = `/${locale}/t/${team.slug}/apps/${encodeURIComponent(app.name)}/logs`
  const [items, setItems] = useState<ProcessStatus[] | null>(null)
  const [note, setNote] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const [busy, setBusy] = useState<string | null>(null)
  const commands = new Map(detail.processes.map((p) => [p.name, p.command]))

  const load = useCallback(
    async (quiet?: boolean) => {
      if (!operate || detail.processes.length === 0) return
      if (!quiet) setLoading(true)
      setNote(null)
      try {
        const r = await apiFetch<ProcessesOut>(`${path}/processes`)
        setItems(r.processes ?? [])
        setError(null)
      } catch (err) {
        if (err instanceof ApiError && err.status === 409) setNote(errorText(err))
        else setError(errorText(err))
        setItems((x) => x ?? [])
      } finally {
        setLoading(false)
      }
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [path, operate, detail.processes.length],
  )

  useEffect(() => {
    void load()
  }, [load])

  useEffect(() => {
    if (typeof document === "undefined") return
    const id = setInterval(() => {
      if (document.visibilityState === "visible") void load(true)
    }, 10_000)
    return () => clearInterval(id)
  }, [load])

  async function act(process: string, action: "restart" | "stop" | "start") {
    setBusy(`${process}:${action}`)
    setError(null)
    try {
      const r = await apiFetch<ProcessesOut>(`${path}/processes/action`, { method: "POST", body: { process, action } })
      setItems(r.processes ?? [])
      toast.success(t(`site.workers.${action}Done`))
    } catch (err) {
      setError(errorText(err))
      throw new Error(errorText(err))
    } finally {
      setBusy(null)
    }
  }

  if (detail.processes.length === 0)
    return (
      <section aria-labelledby="wk-h">
        <SectionTitle id="wk-h" title={t("site.nav.workers")} hint={t("site.workersHint")} />
        <EmptyState className="border-b" icon={CogIcon} title={t("site.workersEmpty")} description={t("site.workersEmptyHint")} />
        <p className="border-b border-border px-6 py-3 text-xs text-muted-foreground">{t("site.workersNote")}</p>
      </section>
    )

  if (!operate)
    return (
      <section aria-labelledby="wk-h">
        <SectionTitle id="wk-h" title={t("site.nav.workers")} hint={t("site.workersHint")} />
        <p className="border-b border-border px-6 py-6 text-sm text-muted-foreground">{t("site.workers.needsDeployer")}</p>
      </section>
    )

  return (
    <section aria-labelledby="wk-h">
      <SectionTitle
        id="wk-h"
        title={t("site.nav.workers")}
        hint={t("site.workersHint")}
        action={
          <Button variant="ghost" size="icon-sm" aria-label={t("github.refresh")} disabled={loading} onClick={() => void load()}>
            <RefreshCwIcon className={loading ? "animate-spin" : ""} />
          </Button>
        }
      />
      <ErrorBand message={error} />
      {note ? <p className="border-b border-border px-6 py-4 text-sm text-muted-foreground">{note}</p> : null}
      {items === null ? (
        note || error ? null : (
          <LoadingRows rows={detail.processes.length} />
        )
      ) : items.length === 0 ? (
        <p className="border-b border-border px-6 py-6 text-sm text-muted-foreground">{t("site.workers.notLive")}</p>
      ) : (
        <ul className="flex flex-col divide-y divide-border border-b border-border">
          {items.map((p) => {
            const isWeb = p.process === "web"
            const badge = PROCESS_BADGE[p.state] ?? "outline"
            const restarting = busy === `${p.process}:restart`
            const stopping = busy === `${p.process}:stop`
            const starting = busy === `${p.process}:start`
            const command = commands.get(p.process)
            return (
              <li key={p.process} className="flex flex-wrap items-center gap-x-4 gap-y-2 px-6 py-4">
                <span className="flex min-w-0 flex-1 flex-col gap-1">
                  <span className="flex flex-wrap items-center gap-2">
                    <span dir="ltr" className="text-sm font-medium text-foreground">
                      {p.process}
                    </span>
                    <Badge variant={badge}>{t(`site.workers.states.${p.state}`, undefined, p.state)}</Badge>
                  </span>
                  <span className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
                    {p.started_at ? <span dir="ltr">{t("site.workers.started", { time: ago(p.started_at) })}</span> : null}
                    <span>{t("site.workers.restarts", { count: p.restart_count })}</span>
                    {p.state === "running" ? (
                      <span dir="ltr">
                        {t("site.workers.cpu")} {(p.cpu_percent ?? 0).toFixed(1)}%
                      </span>
                    ) : null}
                    {p.state === "running" && p.mem_bytes ? (
                      <span dir="ltr">
                        {t("site.workers.mem")} {humanBytes(p.mem_bytes)}
                        {p.mem_limit_bytes ? ` / ${humanBytes(p.mem_limit_bytes)}` : ""}
                      </span>
                    ) : null}
                  </span>
                  {command ? (
                    <span dir="ltr" className="truncate font-mono text-xs text-muted-foreground" title={command}>
                      {command}
                    </span>
                  ) : null}
                </span>
                <span className="flex flex-wrap items-center gap-2">
                  <Link href={`${logsBase}?process=${encodeURIComponent(p.process)}`}>
                    <Button size="sm" variant="ghost">
                      <ScrollTextIcon />
                      {t("site.workers.viewLogs")}
                    </Button>
                  </Link>
                  <Button size="sm" variant="secondary" disabled={busy !== null} onClick={() => void act(p.process, "restart").catch(() => {})}>
                    <RotateCwIcon className={restarting ? "animate-spin" : ""} />
                    {t("site.workers.restart")}
                  </Button>
                  {!isWeb && p.state === "running" ? (
                    <ConfirmButton
                      icon={<SquareIcon className={stopping ? "animate-pulse" : ""} />}
                      label={t("site.workers.stop")}
                      title={t("site.workers.stopTitle", { name: p.process })}
                      description={t("site.workers.stopBody")}
                      confirmLabel={t("site.workers.stop")}
                      onConfirm={() => act(p.process, "stop")}
                    />
                  ) : null}
                  {!isWeb && p.state !== "running" ? (
                    <Button size="sm" variant="secondary" disabled={busy !== null} onClick={() => void act(p.process, "start").catch(() => {})}>
                      <PlayIcon className={starting ? "animate-pulse" : ""} />
                      {t("site.workers.start")}
                    </Button>
                  ) : null}
                </span>
              </li>
            )
          })}
        </ul>
      )}
      <p className="border-b border-border px-6 py-3 text-xs text-muted-foreground">{t("site.workersNote")}</p>
    </section>
  )
}

export function SiteLogs() {
  const { t } = useI18n()
  const { team } = useTeam()
  const operate = canOperate(team.role)
  const { detail, app, path } = useLoadedSite()
  const { run, busy, error, log } = useWorkloadActions(path, app.server_id)
  if (!isImported(app))
    return <RuntimeLogsPanel path={path} team={team.slug} app={app} processes={detail.processes} operate={operate} live={!!detail.live_release} />
  return (
    <>
      <ErrorBand message={error} />
      {operate && app.origin_kind === "container" && app.imported_state !== "missing" ? (
        <WorkloadLogs run={run} busy={busy} />
      ) : (
        <p className="border-b border-border px-6 py-6 text-sm text-muted-foreground">{t("site.logsGuest")}</p>
      )}
      {log}
    </>
  )
}

/** Proxy manager: the hostnames Caddy serves for this site, with HTTPS. */
export function SiteProxy() {
  const { t } = useI18n()
  const { team, manage } = useTeam()
  const { app, path } = useLoadedSite()
  const imported = isImported(app)
  return (
    <section aria-labelledby="px-h">
      <SectionTitle id="px-h" title={t("site.nav.proxy")} hint={t("site.proxyHint")} />
      <DomainsPanel
        team={team.slug}
        path={path}
        manage={manage}
        port={imported ? (app.origin_kind === "guest" ? { required: true } : { required: false, suggested: firstPort(app.origin?.ports) }) : undefined}
      />
    </section>
  )
}

export function SiteBackups() {
  const { t, locale } = useI18n()
  const { team, manage } = useTeam()
  const operate = canOperate(team.role)
  const { app, path } = useLoadedSite()
  const { run, busy, error, log } = useWorkloadActions(path, app.server_id)
  if (isImported(app) && app.origin_kind === "guest" && operate && app.imported_state !== "missing")
    return (
      <>
        <ErrorBand message={error} />
        <GuestSnapshots path={path} name={app.name} run={run} busy={busy} admin={manage} />
        <GuestBackup run={run} busy={busy} />
        {log}
      </>
    )
  return (
    <section aria-labelledby="bk-h">
      <SectionTitle id="bk-h" title={t("site.nav.backups")} hint={t("site.backupsHint")} />
      <EmptyState className="border-b" icon={ArchiveIcon} title={t("site.backupsData")} description={t("site.backupsDataHint")} actions={<><Button variant="secondary" nativeButton={false} render={<Link href={`/${locale}/t/${team.slug}/servers/${app.server_id}/databases`} />}>
          <DatabaseIcon />
          {t("nav.databases")}
        </Button></>} />
    </section>
  )
}

/** Settings: how the site looks, what it builds with, its resources and actions, and the danger zone. */
export function SiteSettings() {
  const { t, locale } = useI18n()
  const { team, manage } = useTeam()
  const router = useRouter()
  const errorText = useErrorText()
  const operate = canOperate(team.role)
  const { detail, app, setDetail, reload, path } = useLoadedSite()
  const { run, busy, error, log } = useWorkloadActions(path, app.server_id)
  const imported = isImported(app)
  const guest = app.origin_kind === "guest"
  const missing = app.imported_state === "missing"
  const home = `/${locale}/t/${team.slug}/servers/${app.server_id}/apps`

  return (
    <>
      <SiteAppearance key={app.id} manage={manage} />
      {!imported ? (
        <>
          <SettingsPanel team={team.slug} path={path} detail={detail} manage={manage} onSaved={(a) => setDetail((d) => (d ? { ...d, app: a } : d))} />
          <ResourcesCard path={`${path}/resources`} manage={manage} processes={detail.processes.map((p) => p.name)} />
          <SitePHPConfig />
          <SiteBuildConfig />
        </>
      ) : (
        <>
          <ErrorBand message={error} />
          {operate && !missing ? (
            <section aria-labelledby="power-h">
              <SectionTitle id="power-h" title={t("controls.power")} hint={t("controls.powerHint")} />
              <div className="border-b border-border px-6 py-4">
                <PowerControls item={app} run={(k, s, i) => run(k, s, i).then(reload)} busy={busy} guest={guest} />
              </div>
            </section>
          ) : null}
          {operate && guest && !missing ? <ResizeGuest run={run} busy={busy} item={app} /> : null}
          {operate && app.origin_kind === "container" && !missing ? <RecreateContainer run={run} /> : null}
          {log}
          {manage ? (
            <DangerZone
              name={app.name}
              admin={manage && !missing}
              guest={guest}
              onForget={async () => {
                try {
                  await apiFetch(path, { method: "DELETE", body: { confirm: app.name } })
                } catch (err) {
                  throw new Error(errorText(err))
                }
                router.replace(home)
              }}
              onDestroy={async (purge) => {
                try {
                  await run("destroy", "?destroy=true", { method: "DELETE", body: { confirm: app.name, purge } })
                } catch (err) {
                  throw new Error(errorText(err))
                }
              }}
            />
          ) : null}
        </>
      )}
    </>
  )
}

/** Title, description, color, icon and an uploaded logo: how the site shows up across Hosbah. */
function SiteAppearance({ manage }: { manage: boolean }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const { app, setDetail, path } = useLoadedSite()
  const [title, setTitle] = useState(app.title ?? "")
  const [description, setDescription] = useState(app.description ?? "")
  const [color, setColor] = useState(app.color ?? "")
  const [icon, setIcon] = useState(app.icon ?? "")
  const file = useRef<HTMLInputElement>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const logo = app.logo_url ?? null
  const apply = (a: App) => setDetail((d) => (d ? { ...d, app: { ...d.app, ...a, logo_url: a.logo_url ?? null } } : d))

  async function call(fn: () => Promise<App>, done?: string) {
    setBusy(true)
    setError(null)
    try {
      apply(await fn())
      if (done) toast.success(done)
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
      if (file.current) file.current.value = ""
    }
  }

  function upload(f: File | null | undefined) {
    if (!f) return
    if (!["image/png", "image/jpeg", "image/webp"].includes(f.type)) return setError(t("servers.appearance.logoType"))
    if (f.size > 512 * 1024) return setError(t("servers.appearance.logoSize"))
    void call(() => apiFetch<App>(`${path}/logo`, { method: "PUT", body: f }), t("site.saved"))
  }

  function save(e: FormEvent) {
    e.preventDefault()
    void call(() => apiFetch<App>(path, { method: "PATCH", body: { title: title.trim(), description: description.trim(), color, icon } }), t("site.saved"))
  }

  return (
    <form onSubmit={save}>
      <SectionTitle
        title={t("servers.appearance.title")}
        hint={t("site.appearanceHint")}
        action={<ServerMark server={{ color, icon: icon || "globe", logo_url: logo }} size={10} />}
      />
      <ErrorBand message={error} />
      <fieldset disabled={!manage || busy} className="contents">
        <SettingsRow label={t("site.title")} htmlFor="site-title" hint={t("site.titleHint", { name: app.name })}>
          <Input id="site-title" dir="auto" maxLength={80} placeholder={app.name} value={title} onChange={(e) => setTitle(e.target.value)} />
        </SettingsRow>
        <SettingsRow label={t("site.description")} htmlFor="site-desc">
          <textarea
            id="site-desc"
            dir="auto"
            rows={3}
            maxLength={280}
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            className="w-full border border-border bg-background px-3 py-2 text-sm text-foreground outline-none focus-visible:border-nq-brand"
          />
        </SettingsRow>
        <SettingsRow label={t("servers.appearance.color")}>
          <div className="flex flex-wrap items-center gap-2" role="radiogroup">
            <button
              type="button"
              role="radio"
              aria-checked={!color}
              aria-label={t("servers.appearance.none")}
              onClick={() => setColor("")}
              className={cn("size-7 border border-dashed border-border", !color && "ring-2 ring-nq-brand ring-offset-2 ring-offset-background")}
            />
            {SERVER_COLORS.map((c) => (
              <button
                key={c}
                type="button"
                role="radio"
                aria-checked={color === c}
                aria-label={c}
                onClick={() => setColor(c)}
                className={cn("size-7 border border-border", color === c && "ring-2 ring-nq-brand ring-offset-2 ring-offset-background")}
                style={{ background: c }}
              />
            ))}
            <input
              type="color"
              aria-label={t("servers.appearance.custom")}
              value={color || "#3B82F6"}
              onChange={(e) => setColor(e.target.value.toUpperCase())}
              className="h-7 w-10 cursor-pointer border border-border bg-transparent"
            />
          </div>
        </SettingsRow>
        <SettingsRow label={t("servers.appearance.icon")}>
          <div className="flex flex-wrap gap-1" role="radiogroup">
            {Object.entries(SERVER_ICONS).map(([key, Icon]) => (
              <button
                key={key}
                type="button"
                role="radio"
                aria-checked={icon === key}
                aria-label={key}
                title={key}
                onClick={() => setIcon(icon === key ? "" : key)}
                className={cn(
                  "flex size-8 items-center justify-center border border-border [&_svg]:size-4",
                  icon === key ? "bg-secondary text-foreground ring-1 ring-nq-brand" : "text-muted-foreground hover:bg-secondary",
                )}
              >
                <Icon />
              </button>
            ))}
          </div>
        </SettingsRow>
        <SettingsRow label={t("servers.appearance.logo")} hint={t("servers.appearance.logoHint")}>
          <div
            className="flex flex-wrap items-center gap-3 border border-dashed border-border px-4 py-3"
            onDragOver={(e) => e.preventDefault()}
            onDrop={(e) => {
              e.preventDefault()
              if (manage) upload(e.dataTransfer.files?.[0])
            }}
          >
            <ServerMark server={{ color, icon: icon || "globe", logo_url: logo }} size={10} />
            <input
              ref={file}
              type="file"
              accept="image/png,image/jpeg,image/webp"
              className="sr-only"
              aria-label={t("servers.appearance.logo")}
              onChange={(e) => upload(e.target.files?.[0])}
            />
            <Button type="button" variant="secondary" size="sm" onClick={() => file.current?.click()}>
              <UploadIcon />
              {logo ? t("servers.appearance.logoReplace") : t("servers.appearance.logoUpload")}
            </Button>
            {logo ? (
              <Button type="button" variant="ghost" size="sm" onClick={() => void call(() => apiFetch<App>(`${path}/logo`, { method: "DELETE" }))}>
                <Trash2Icon />
                {t("servers.appearance.logoRemove")}
              </Button>
            ) : (
              <span className="text-xs text-muted-foreground">{t("servers.appearance.logoDrop")}</span>
            )}
          </div>
        </SettingsRow>
        {manage ? (
          <div className="flex justify-end border-b border-border px-6 py-4">
            <Button variant="primary" type="submit" disabled={busy}>
              {busy ? t("common.working") : t("common.save")}
            </Button>
          </div>
        ) : null}
      </fieldset>
    </form>
  )
}
