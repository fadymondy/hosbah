"use client"

import Link from "next/link"
import { useRouter } from "next/navigation"
import { useEffect, useRef, useState, type FormEvent } from "react"
import { ArchiveIcon, CogIcon, DatabaseIcon, LinkIcon, PlusIcon, Trash2Icon, UploadIcon } from "lucide-react"
import { toast } from "sonner"

import { DomainsPanel } from "@/components/apps/domains"
import { RuntimeLogsPanel } from "@/components/apps/runtime-logs"
import { SettingsPanel } from "@/components/apps/settings"
import { useErrorText } from "@/components/form-error"
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
import { SERVER_COLORS, SERVER_ICONS, ServerMark } from "@/components/server-mark"
import { ListSearch, matches } from "@/components/server-tabs/list-tools"
import { useLoadedSite } from "@/components/site/context"
import { ErrorBand, LoadingRows, SectionTitle, SettingsRow, TableBand } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Input } from "@/components/ui/input"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { apiFetch } from "@/lib/api"
import { isImported, type App, type EnvVar } from "@/lib/apps"
import type { DataService } from "@/lib/databases"
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
    apiFetch<{ env: EnvVar[] }>(`${path}/env`)
      .then((r) => setVars((r.env ?? []).map((e) => e.name)))
      .catch(() => setVars([]))
  }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(load, [team.slug, app.server_id, path])

  async function attach(s: DataService) {
    setBusy(s.id)
    setError(null)
    try {
      const r = await apiFetch<{ variable: string }>(`/api/teams/${team.slug}/data-services/${s.name}/attach`, { method: "POST", body: { app: app.name } })
      toast.success(t("site.attached", { variable: r.variable }))
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
            <Button size="sm" nativeButton={false} render={<Link href={`${base}/servers/${app.server_id}/databases`} />}>
              <PlusIcon />
              {t("site.newDatabase")}
            </Button>
          ) : null
        }
      />
      <ErrorBand message={error} />
      {injected.length ? (
        <p className="flex flex-wrap items-center gap-2 border-b border-line px-6 py-3 text-xs text-grid-muted">
          <LinkIcon className="size-3.5" />
          {t("site.injected")}
          {injected.map((v) => (
            <code key={v} dir="ltr" className="border border-line bg-grid-card px-1.5 py-0.5 font-mono text-grid-fg">
              {v}
            </code>
          ))}
        </p>
      ) : null}
      {services === null ? (
        <LoadingRows rows={2} />
      ) : services.length === 0 ? (
        <Empty className="border-b">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <DatabaseIcon />
            </EmptyMedia>
            <EmptyTitle>{t("databases.emptyTitle")}</EmptyTitle>
            <EmptyDescription>{t("site.databasesEmpty")}</EmptyDescription>
          </EmptyHeader>
        </Empty>
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
                      <Badge variant={s.status === "running" ? "default" : "outline"}>{t(`databases.statuses.${s.status}`, undefined, s.status)}</Badge>
                    </TableCell>
                    <TableCell>
                      <div className="flex flex-wrap justify-end gap-2">
                        <Button size="sm" variant="outline" nativeButton={false} render={<Link href={`${base}/databases/${s.name}`} />}>
                          {t("lists.manage")}
                        </Button>
                        {manage && !isImported(s) && s.kind !== "minio" ? (
                          <Button size="sm" disabled={busy === s.id || s.status !== "running"} onClick={() => void attach(s)}>
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
    </section>
  )
}

/** Workers: the processes the app runs besides web, as its hosbah.toml declares them. */
export function SiteWorkers() {
  const { t } = useI18n()
  const { detail } = useLoadedSite()
  return (
    <section aria-labelledby="wk-h">
      <SectionTitle id="wk-h" title={t("site.nav.workers")} hint={t("site.workersHint")} />
      {detail.processes.length === 0 ? (
        <Empty className="border-b">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <CogIcon />
            </EmptyMedia>
            <EmptyTitle>{t("site.workersEmpty")}</EmptyTitle>
            <EmptyDescription>{t("site.workersEmptyHint")}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <TableBand>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("site.process")}</TableHead>
                <TableHead>{t("site.command")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {detail.processes.map((p) => (
                <TableRow key={p.name}>
                  <TableCell dir="ltr" className="text-start font-medium">
                    {p.name}
                  </TableCell>
                  <TableCell dir="ltr" className="text-start font-mono text-xs text-grid-muted">
                    {p.command ?? "—"}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </TableBand>
      )}
      <p className="border-b border-line px-6 py-3 text-xs text-grid-muted">{t("site.workersNote")}</p>
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
        <p className="border-b border-line px-6 py-6 text-sm text-grid-muted">{t("site.logsGuest")}</p>
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
      <Empty className="border-b">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <ArchiveIcon />
          </EmptyMedia>
          <EmptyTitle>{t("site.backupsData")}</EmptyTitle>
          <EmptyDescription>{t("site.backupsDataHint")}</EmptyDescription>
        </EmptyHeader>
        <Button variant="outline" nativeButton={false} render={<Link href={`/${locale}/t/${team.slug}/servers/${app.server_id}/databases`} />}>
          <DatabaseIcon />
          {t("nav.databases")}
        </Button>
      </Empty>
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
        <SettingsPanel team={team.slug} path={path} detail={detail} manage={manage} onSaved={(a) => setDetail((d) => (d ? { ...d, app: a } : d))} />
      ) : (
        <>
          <ErrorBand message={error} />
          {operate && !missing ? (
            <section aria-labelledby="power-h">
              <SectionTitle id="power-h" title={t("controls.power")} hint={t("controls.powerHint")} />
              <div className="border-b border-line px-6 py-4">
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
            className="w-full border border-line bg-background px-3 py-2 text-sm text-grid-fg outline-none focus-visible:border-[var(--grid-brand)]"
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
              className={cn("size-7 border border-dashed border-line", !color && "ring-2 ring-[var(--grid-brand)] ring-offset-2 ring-offset-background")}
            />
            {SERVER_COLORS.map((c) => (
              <button
                key={c}
                type="button"
                role="radio"
                aria-checked={color === c}
                aria-label={c}
                onClick={() => setColor(c)}
                className={cn("size-7 border border-line", color === c && "ring-2 ring-[var(--grid-brand)] ring-offset-2 ring-offset-background")}
                style={{ background: c }}
              />
            ))}
            <input
              type="color"
              aria-label={t("servers.appearance.custom")}
              value={color || "#3B82F6"}
              onChange={(e) => setColor(e.target.value.toUpperCase())}
              className="h-7 w-10 cursor-pointer border border-line bg-transparent"
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
                  "flex size-8 items-center justify-center border border-line [&_svg]:size-4",
                  icon === key ? "bg-grid-soft text-grid-fg ring-1 ring-[var(--grid-brand)]" : "text-grid-muted hover:bg-grid-soft",
                )}
              >
                <Icon />
              </button>
            ))}
          </div>
        </SettingsRow>
        <SettingsRow label={t("servers.appearance.logo")} hint={t("servers.appearance.logoHint")}>
          <div
            className="flex flex-wrap items-center gap-3 border border-dashed border-line px-4 py-3"
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
            <Button type="button" variant="outline" size="sm" onClick={() => file.current?.click()}>
              <UploadIcon />
              {logo ? t("servers.appearance.logoReplace") : t("servers.appearance.logoUpload")}
            </Button>
            {logo ? (
              <Button type="button" variant="ghost" size="sm" onClick={() => void call(() => apiFetch<App>(`${path}/logo`, { method: "DELETE" }))}>
                <Trash2Icon />
                {t("servers.appearance.logoRemove")}
              </Button>
            ) : (
              <span className="text-xs text-grid-muted">{t("servers.appearance.logoDrop")}</span>
            )}
          </div>
        </SettingsRow>
        {manage ? (
          <div className="flex justify-end border-b border-line px-6 py-4">
            <Button type="submit" disabled={busy}>
              {busy ? t("common.working") : t("common.save")}
            </Button>
          </div>
        ) : null}
      </fieldset>
    </form>
  )
}
