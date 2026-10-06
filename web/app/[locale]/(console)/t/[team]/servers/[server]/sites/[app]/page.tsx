"use client"

import { Badge, Button, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, Field, FieldLabel, Input } from "@fadymondy/nasaq/web"
import Link from "next/link"
import { useParams, useRouter } from "next/navigation"
import { useCallback, useEffect, useState, type FormEvent, type ReactNode } from "react"
import {
  ArchiveIcon,
  CloudUploadIcon,
  CpuIcon,
  DatabaseIcon,
  GitBranchIcon,
  GlobeIcon,
  LayersIcon,
  MapPinIcon,
  NetworkIcon,
  PlusIcon,
  ServerIcon,
  ShieldCheckIcon,
  ZapIcon,
} from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { AppStatusBadge } from "@/components/app-status"
import { FormError, useErrorText } from "@/components/form-error"
import { ErrorBand, LoadingRows } from "@/components/states"
import { PresenceBadge } from "@/components/server-status"





import { apiFetch } from "@/lib/api"
import type { AppDetail, Deployment } from "@/lib/apps"
import type { DataService, DataServiceKind } from "@/lib/databases"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import { formatBytes, type Server } from "@/lib/servers"
import { useTeam } from "@/lib/team"

/**
 * A site's canvas (Laravel Cloud style): the network in front of it (edge + domains), the server it
 * runs on with its processes, and the resources it can attach — all on one board. Every action is a
 * call to the same REST API the MCP tools and SDKs use.
 */
export default function SiteCanvasPage() {
  const { t, locale } = useI18n()
  const { team, manage } = useTeam()
  const params = useParams<{ server: string; app: string }>()
  const router = useRouter()
  const errorText = useErrorText()
  const base = `/${locale}/t/${team.slug}`
  const appPath = `/api/teams/${team.slug}/apps/${params.app}`

  const [detail, setDetail] = useState<AppDetail | null>(null)
  const [server, setServer] = useState<Server | null>(null)
  const [services, setServices] = useState<DataService[]>([])
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [adding, setAdding] = useState<DataServiceKind | null>(null)
  useDocumentTitle(`${detail?.app.name ?? t("sites.site")} — ${team.name}`)

  const load = useCallback(async () => {
    try {
      const [d, s, ds] = await Promise.all([
        apiFetch<AppDetail>(appPath),
        apiFetch<Server>(`/api/teams/${team.slug}/servers/${params.server}`),
        apiFetch<{ services: DataService[] }>(`/api/teams/${team.slug}/data-services`),
      ])
      setDetail(d)
      setServer(s)
      setServices((ds.services ?? []).filter((x) => x.server_id === s.id))
    } catch (err) {
      setError(errorText(err))
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [appPath, team.slug, params.server])

  useEffect(() => {
    void load()
  }, [load])

  async function deploy() {
    setBusy(true)
    setError(null)
    try {
      await apiFetch<Deployment>(`${appPath}/deployments`, { method: "POST", body: {} })
      router.push(`${base}/apps/${params.app}`)
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  if (!detail || !server) {
    return (
      <PageBody title={t("sites.site")} bleed>
        <ErrorBand message={error} />
        <LoadingRows rows={4} />
      </PageBody>
    )
  }

  const app = detail.app
  const cpus = server.facts?.cpus
  const mem = server.facts?.mem_total_bytes
  const processes = detail.processes.length > 0 ? detail.processes : [{ name: "web", command: null }]
  const web = processes.filter((p) => p.name === "web")
  const workers = processes.filter((p) => p.name !== "web")

  return (
    <PageBody
      title={
        <span className="flex flex-wrap items-center gap-2">
          <span className="grid size-8 place-items-center border border-border bg-secondary font-mono text-sm uppercase text-nq-brand">
            {app.name.slice(0, 1)}
          </span>
          <span dir="ltr">{app.name}</span>
          <span className="text-muted-foreground">·</span>
          <span dir="ltr" className="font-normal text-muted-foreground">
            {app.branch}
          </span>
          <AppStatusBadge status={app.status} />
        </span>
      }
      description={
        <span dir="ltr" className="flex items-center gap-2 font-mono text-xs">
          <GitBranchIcon className="size-3.5" aria-hidden />
          {app.repo_url}
        </span>
      }
      actions={
        manage ? (
          <Button variant="primary" disabled={busy} onClick={deploy}>
            <CloudUploadIcon />
            {busy ? t("common.working") : t("sites.deploy")}
          </Button>
        ) : null
      }
      bleed
    >
      <ErrorBand message={error} />
      <div
        data-testid="site-canvas"
        className="relative overflow-x-auto border-b border-border bg-background p-6 [background-image:radial-gradient(var(--color-line)_1px,transparent_1px)] [background-size:20px_20px]"
      >
        <div className="grid grid-cols-1 items-start gap-6 lg:grid-cols-3 lg:gap-10">
          {/* Network: edge and domains */}
          <Group icon={<NetworkIcon />} label={t("sites.network")}>
            <Card title={t("sites.edge")}>
              <Row icon={<ShieldCheckIcon />} label={t("sites.tls")} value={t("sites.tlsByCaddy")} />
              <Row icon={<ZapIcon />} label={t("sites.proxy")} value="Caddy" />
            </Card>
            <Card title={t("sites.domains")} testId="canvas-domains">
              {detail.domains.length === 0 ? (
                <p className="px-4 py-3 text-sm text-muted-foreground">{t("sites.noDomains")}</p>
              ) : (
                detail.domains.map((d) => (
                  <Row
                    key={d.id}
                    icon={<GlobeIcon />}
                    label={
                      <span dir="ltr" className="truncate">
                        {d.hostname}
                      </span>
                    }
                    value={
                      <Dot
                        ok={d.verified || d.tls !== "auto"}
                        text={d.verified ? t("sites.verified") : d.tls === "auto" ? t("sites.pending") : d.tls}
                      />
                    }
                  />
                ))
              )}
              {manage ? (
                <CardLink href={`${base}/apps/${app.name}/proxy`} icon={<PlusIcon />}>
                  {t("sites.addDomain")}
                </CardLink>
              ) : null}
            </Card>
          </Group>

          {/* The server the site runs on, with its processes */}
          <Group
            icon={<MapPinIcon />}
            label={
              <span className="flex items-center gap-2">
                <span dir="ltr">{server.name}</span>
                <PresenceBadge server={server} />
              </span>
            }
          >
            {web.map((p) => (
              <Card
                key={p.name}
                title={
                  <span className="flex items-center gap-2">
                    {t("sites.app")}
                    <Badge variant="outline" className="gap-1">
                      <LayersIcon className="size-3" aria-hidden />
                      {t("sites.web")}
                    </Badge>
                  </span>
                }
                testId="canvas-web"
              >
                <Row
                  icon={<CpuIcon />}
                  label={t("sites.size")}
                  value={
                    <span dir="ltr">
                      {cpus ? t("sites.vcpu", { n: cpus }) : "—"}
                      {mem ? ` · ${formatBytes(mem)}` : ""}
                    </span>
                  }
                />
                <Row icon={<ServerIcon />} label={t("sites.address")} value={<span dir="ltr">{server.ip_address ?? "—"}</span>} />
              </Card>
            ))}
            {workers.map((p) => (
              <Card key={p.name} title={<span dir="ltr">{p.name}</span>}>
                <Row
                  icon={<LayersIcon />}
                  label={t("sites.worker")}
                  value={
                    <code dir="ltr" className="truncate text-xs">
                      {p.command ?? "—"}
                    </code>
                  }
                />
              </Card>
            ))}
            <p className="px-1 text-xs break-words text-muted-foreground">{t("sites.workersHint")}</p>
          </Group>

          {/* Resources: attached on the same server */}
          <div className="flex min-w-0 flex-col gap-3">
            {services.map((s) => (
              <Link
                key={s.id}
                href={`${base}/databases/${s.id}`}
                className="flex items-start gap-3 border border-border bg-card p-4 hover:bg-secondary"
              >
                <DatabaseIcon className="mt-0.5 size-4 shrink-0 text-nq-brand" aria-hidden />
                <span className="flex min-w-0 flex-col">
                  <span dir="ltr" className="font-medium">
                    {s.name}
                  </span>
                  <span className="text-sm text-muted-foreground">
                    {s.kind} {s.version} · {s.status}
                  </span>
                </span>
              </Link>
            ))}
            {manage ? (
              <>
                <AddCard
                  icon={<DatabaseIcon />}
                  title={t("sites.addDatabase")}
                  hint={t("sites.addDatabaseHint")}
                  onClick={() => setAdding("postgres")}
                />
                <AddCard icon={<ZapIcon />} title={t("sites.addCache")} hint={t("sites.addCacheHint")} onClick={() => setAdding("redis")} />
                <AddCard
                  icon={<ArchiveIcon />}
                  title={t("sites.addBucket")}
                  hint={t("sites.addBucketHint")}
                  onClick={() => setAdding("minio")}
                />
              </>
            ) : null}
          </div>
        </div>
      </div>
      {adding ? (
        <AddServiceDialog
          kind={adding}
          server={server}
          appName={app.name}
          onClose={() => setAdding(null)}
          onCreated={(id) => router.push(`${base}/databases/${id}`)}
        />
      ) : null}
    </PageBody>
  )
}

function Group({ icon, label, children }: { icon: ReactNode; label: ReactNode; children: ReactNode }) {
  return (
    <section className="flex min-w-0 flex-col gap-3 border border-dashed border-border p-3">
      <div className="flex items-center gap-2 px-1 text-sm text-muted-foreground [&_svg]:size-4">
        {icon}
        {label}
      </div>
      {children}
    </section>
  )
}

function Card({ title, children, testId }: { title: ReactNode; children: ReactNode; testId?: string }) {
  return (
    <div className="flex flex-col border border-border bg-card" data-testid={testId}>
      <div className="border-b border-border px-4 py-3 text-sm font-medium text-foreground">{title}</div>
      <div className="flex flex-col divide-y divide-border">{children}</div>
    </div>
  )
}

function Row({ icon, label, value }: { icon: ReactNode; label: ReactNode; value: ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-3 px-4 py-2 text-sm">
      <span className="flex min-w-0 items-center gap-2 text-muted-foreground [&_svg]:size-4 [&_svg]:shrink-0">
        {icon}
        {label}
      </span>
      <span className="shrink-0 text-end">{value}</span>
    </div>
  )
}

function Dot({ ok, text }: { ok: boolean; text: string }) {
  return (
    <span className="flex items-center gap-1.5 text-xs text-muted-foreground">
      <span className={`size-1.5 rounded-full ${ok ? "bg-nq-brand" : "bg-nq-fg-muted/60"}`} aria-hidden />
      {text}
    </span>
  )
}

function CardLink({ href, icon, children }: { href: string; icon: ReactNode; children: ReactNode }) {
  return (
    <Link href={href} className="flex items-center gap-2 px-4 py-3 text-sm text-nq-brand hover:bg-secondary [&_svg]:size-4">
      {icon}
      {children}
    </Link>
  )
}

function AddCard({
  icon,
  title,
  hint,
  onClick,
  disabled,
}: {
  icon: ReactNode
  title: string
  hint: string
  onClick?: () => void
  disabled?: boolean
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      className="flex items-start gap-3 border border-dashed border-border bg-card p-4 text-start hover:border-solid hover:bg-secondary disabled:cursor-not-allowed disabled:opacity-60 [&_svg]:mt-0.5 [&_svg]:size-4 [&_svg]:shrink-0 [&_svg]:text-nq-brand"
    >
      {icon}
      <span className="flex flex-col">
        <span className="font-medium">{title}</span>
        <span className="text-sm text-muted-foreground">{hint}</span>
      </span>
    </button>
  )
}

function AddServiceDialog({
  kind: initial,
  server,
  appName,
  onClose,
  onCreated,
}: {
  kind: DataServiceKind
  server: Server
  appName: string
  onClose: () => void
  onCreated: (id: string) => void
}) {
  const { t } = useI18n()
  const { team } = useTeam()
  const errorText = useErrorText()
  const [kind, setKind] = useState<DataServiceKind>(initial)
  const [name, setName] = useState(`${appName}-${initial === "redis" ? "cache" : initial === "minio" ? "files" : "db"}`.slice(0, 40))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const s = await apiFetch<DataService>(`/api/teams/${team.slug}/data-services`, {
        method: "POST",
        body: { name, server: server.id, kind },
      })
      onCreated(s.id)
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  const kinds: DataServiceKind[] = initial === "redis" || initial === "minio" ? [initial] : ["postgres", "mysql"]
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>
              {initial === "redis" ? t("sites.addCache") : initial === "minio" ? t("sites.addBucket") : t("sites.addDatabase")}
            </DialogTitle>
            <DialogDescription>{t("sites.addServiceHint", { server: server.name })}</DialogDescription>
          </DialogHeader>
          <div className="flex w-full flex-col gap-4">
            {kinds.length > 1 ? (
              <div className="flex gap-2" role="radiogroup" aria-label={t("databases.kind")}>
                {kinds.map((k) => (
                  <Button
                    key={k}
                    type="button"
                    size="sm"
                    variant={k === kind ? "primary" : "secondary"}
                    aria-pressed={k === kind}
                    onClick={() => setKind(k)}
                  >
                    {k === "postgres" ? "PostgreSQL" : "MySQL"}
                  </Button>
                ))}
              </div>
            ) : null}
            <Field>
              <FieldLabel htmlFor="svc-name">{t("databases.name")}</FieldLabel>
              <Input id="svc-name" dir="ltr" required maxLength={40} value={name} onChange={(e) => setName(e.target.value)} />
            </Field>
          </div>
          <FormError message={error} />
          <DialogFooter>
            <Button type="button" variant="secondary" onClick={onClose}>
              {t("common.cancel")}
            </Button>
            <Button variant="primary" type="submit" disabled={busy || !name}>
              {busy ? t("common.working") : t("sites.create")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
