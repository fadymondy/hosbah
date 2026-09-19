"use client"

import Link from "next/link"
import { useParams } from "next/navigation"
import { useEffect, useState } from "react"
import { AlertCircleIcon, LayoutGridIcon, RocketIcon, RotateCcwIcon, RotateCwIcon, SquareIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { AppStatusBadge, DeploymentStatusBadge } from "@/components/app-status"
import { DomainsPanel } from "@/components/apps/domains"
import { EnvPanel } from "@/components/apps/env"
import { ReleasesPanel } from "@/components/apps/releases"
import { RuntimeLogsPanel } from "@/components/apps/runtime-logs"
import { SettingsPanel } from "@/components/apps/settings"
import { ConfirmButton } from "@/components/confirm-button"
import { DeploymentLog } from "@/components/deployment-log"
import { useErrorText } from "@/components/form-error"
import { ErrorBand, LoadingRows, Notice } from "@/components/states"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { apiFetch, ApiError } from "@/lib/api"
import { appPath, isActive, runtimeLabel, shortSha, type AppDetail, type Deployment } from "@/lib/apps"
import { useDocumentTitle, useFormatDate, useI18n } from "@/lib/i18n"
import { canOperate, useEventStream } from "@/lib/servers"
import { useTeam } from "@/lib/team"
import { cn } from "@/lib/utils"

export default function AppPage() {
  const { app: appParam } = useParams<{ app: string }>()
  const { t, locale } = useI18n()
  const { team, manage } = useTeam()
  const fmt = useFormatDate()
  const errorText = useErrorText()
  const operate = canOperate(team.role)

  const [detail, setDetail] = useState<AppDetail | null>(null)
  const [deps, setDeps] = useState<Deployment[]>([])
  const [selected, setSelected] = useState<string | null>(null)
  const [pinned, setPinned] = useState(false)
  const [missing, setMissing] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [tab, setTab] = useState("deployments")
  // #domains, #env… (links from the site canvas) open that tab.
  useEffect(() => {
    const h = window.location.hash.slice(1)
    if (h) setTab(h)
  }, [])
  const app = detail?.app
  useDocumentTitle(`${app?.name ?? t("nav.apps")} — ${team.name}`)

  const path = appPath(team.slug, decodeURIComponent(appParam))
  const base = `/${locale}/t/${team.slug}`

  useEffect(() => {
    apiFetch<AppDetail>(path)
      .then(setDetail)
      .catch((err) => (err instanceof ApiError && err.status === 404 ? setMissing(true) : setError(errorText(err))))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path])

  const connected = useEventStream(missing ? null : `${path}/stream`, {
    app: (d) => setDetail(d as AppDetail),
    deployments: (d) => {
      const list = d as Deployment[]
      setDeps(list)
      // Follow the newest deployment unless the user picked an older one.
      if (list[0]) setSelected((cur) => (pinned && cur ? cur : list[0].id))
    },
    gone: () => setMissing(true),
  })

  function pushDeployment(dep: Deployment) {
    setDeps((prev) => (prev.some((p) => p.id === dep.id) ? prev : [dep, ...prev]))
    setSelected(dep.id)
    setPinned(false)
    setTab("deployments")
  }

  async function act(fn: () => Promise<Deployment>) {
    setBusy(true)
    setError(null)
    try {
      pushDeployment(await fn())
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  if (missing) {
    return (
      <PageBody title={t("nav.apps")} bleed>
        <Notice icon={<AlertCircleIcon />} title={t("errors.notFound")} />
      </PageBody>
    )
  }
  if (!detail || !app) {
    return (
      <PageBody title={<Skeleton className="h-8 w-48" />} bleed>
        <ErrorBand message={error} />
        <LoadingRows rows={4} />
      </PageBody>
    )
  }

  const live = detail.live_release
  const prev = detail.previous_release
  const selectedDep = deps.find((d) => d.id === selected) ?? null
  const anyActive = deps.some(isActive)

  const actions = (
    <>
      <span className="text-xs text-muted-foreground" aria-live="polite">
        {connected ? t("servers.detail.live") : t("servers.detail.reconnecting")}
      </span>
      <AppStatusBadge status={app.status} />
      <Button size="sm" variant="outline" nativeButton={false} render={<Link href={`/${locale}/t/${team.slug}/servers/${app.server_id}/sites/${app.id}`} />}>
        <LayoutGridIcon />
        {t("sites.canvas")}
      </Button>
      {operate ? (
        <>
          <Button size="sm" disabled={busy} onClick={() => act(() => apiFetch<Deployment>(`${path}/deployments`, { method: "POST", body: {} }))}>
            <RocketIcon />
            {t("apps.actions.deploy")}
          </Button>
          {prev ? (
            <ConfirmButton
              icon={<RotateCcwIcon />}
              label={t("apps.actions.rollbackTo", { version: prev.version })}
              title={t("apps.actions.rollbackTitle", { version: prev.version })}
              description={t("apps.actions.rollbackHint", { version: prev.version, name: app.name })}
              confirmLabel={t("apps.actions.rollback")}
              onConfirm={async () => {
                try {
                  const r = await apiFetch<{ deployment: Deployment }>(`${path}/rollback`, {
                    method: "POST",
                    body: { release: prev.id },
                  })
                  pushDeployment(r.deployment)
                } catch (err) {
                  throw new Error(errorText(err))
                }
              }}
            />
          ) : (
            <Button size="sm" variant="outline" disabled title={t("apps.actions.noPrevious")}>
              <RotateCcwIcon />
              {t("apps.actions.rollback")}
            </Button>
          )}
          <Button
            size="sm"
            variant="outline"
            disabled={busy || !live}
            title={live ? undefined : t("apps.actions.noLive")}
            onClick={() => act(() => apiFetch<Deployment>(`${path}/restart`, { method: "POST" }))}
          >
            <RotateCwIcon />
            {t("apps.actions.restart")}
          </Button>
        </>
      ) : null}
    </>
  )

  return (
    <PageBody
      title={<span dir="ltr">{app.name}</span>}
      description={
        <span dir="ltr" className="font-mono text-xs">
          {app.repo_url} · {app.branch}
        </span>
      }
      actions={actions}
      bleed
    >
      <ErrorBand message={error} />
      {!operate ? <p className="border-b border-line bg-grid-soft px-6 py-3 text-sm text-grid-body">{t("apps.viewerHint")}</p> : null}
      {operate && !prev ? <p className="border-b border-line px-6 py-2 text-xs text-grid-muted">{t("apps.actions.noPrevious")}</p> : null}

      <dl className="grid grid-cols-1 gap-px border-b border-line bg-line sm:grid-cols-2 lg:grid-cols-5" data-testid="app-summary">
        <Stat label={t("apps.liveRelease")}>
          {live ? (
            <span dir="ltr" className="font-mono text-sm">
              v{live.version} · {shortSha(live.commit_sha)}
            </span>
          ) : (
            <span className="text-sm text-muted-foreground">{t("apps.noLive")}</span>
          )}
        </Stat>
        <Stat label={t("apps.server")}>
          <Link dir="ltr" href={`${base}/servers/${app.server_id}`} className="font-mono text-sm underline-offset-4 hover:underline">
            {app.server_name || app.server_id}
          </Link>
        </Stat>
        <Stat label={t("apps.runtime")}>
          <span dir="ltr" className="font-mono text-sm">
            {runtimeLabel(app.runtime) ?? t("apps.runtimeUnknown")}
          </span>
        </Stat>
        <Stat label={t("apps.domainsLabel")}>
          {detail.domains.length ? (
            detail.domains.map((d) => (
              <a
                key={d.id}
                dir="ltr"
                href={`${d.tls === "off" ? "http" : "https"}://${d.hostname}`}
                target="_blank"
                rel="noreferrer"
                className="block truncate font-mono text-xs underline-offset-4 hover:underline"
              >
                {d.hostname}
              </a>
            ))
          ) : (
            <span className="text-sm text-muted-foreground">—</span>
          )}
        </Stat>
        <Stat label={t("apps.processes")}>
          {detail.processes.length ? (
            detail.processes.map((p) => (
              <span key={p.name} dir="ltr" className="block truncate font-mono text-xs" title={p.command ?? undefined}>
                {p.name}
              </span>
            ))
          ) : (
            <span className="text-sm text-muted-foreground">—</span>
          )}
        </Stat>
      </dl>

      <Tabs value={tab} onValueChange={(v) => setTab(v as string)} className="gap-0">
        <div className="overflow-x-auto border-b border-line px-6 py-3">
          <TabsList className="max-w-full justify-start overflow-x-auto">
            <TabsTrigger value="deployments">{t("apps.tabs.deployments")}</TabsTrigger>
            <TabsTrigger value="logs">{t("apps.tabs.logs")}</TabsTrigger>
            <TabsTrigger value="env">{t("apps.tabs.env")}</TabsTrigger>
            <TabsTrigger value="domains">{t("apps.tabs.domains")}</TabsTrigger>
            <TabsTrigger value="releases">{t("apps.tabs.releases")}</TabsTrigger>
            <TabsTrigger value="settings">{t("apps.tabs.settings")}</TabsTrigger>
          </TabsList>
        </div>

        <TabsContent value="deployments">
          <div className="grid border-b border-line lg:grid-cols-[minmax(0,2fr)_minmax(0,3fr)]" data-testid="deployments">
            <div className="min-w-0 border-b border-line lg:border-e lg:border-b-0">
              {deps.length === 0 ? (
                <p className="px-6 py-4 text-sm text-grid-muted">{t("apps.deploy.empty")}</p>
              ) : (
                <ol className="flex max-h-[32rem] flex-col divide-y divide-line overflow-y-auto">
                  {deps.map((d) => (
                    <li key={d.id}>
                      <button
                        type="button"
                        data-deployment={d.id}
                        aria-current={d.id === selected}
                        onClick={() => {
                          setSelected(d.id)
                          setPinned(d.id !== deps[0]?.id)
                        }}
                        className={cn(
                          "flex w-full flex-col gap-1 px-6 py-3 text-start hover:bg-grid-soft",
                          d.id === selected && "bg-grid-soft",
                        )}
                      >
                        <span className="flex flex-wrap items-center gap-2 text-sm">
                          <span dir="ltr" className="font-mono">
                            #{d.number}
                          </span>
                          <span className="font-medium">{t(`apps.deploy.kinds.${d.kind}`, undefined, d.kind)}</span>
                          <DeploymentStatusBadge status={d.status} />
                          <span className="ms-auto text-xs text-muted-foreground">
                            {t(`apps.deploy.triggers.${d.trigger}`, undefined, d.trigger)}
                          </span>
                        </span>
                        <span dir="ltr" className="truncate text-start font-mono text-xs text-muted-foreground">
                          {shortSha(d.commit_sha)} {d.commit_message ?? d.ref}
                        </span>
                        <span dir="ltr" className="text-start text-xs text-muted-foreground">
                          {fmt(d.created_at)}
                        </span>
                      </button>
                    </li>
                  ))}
                </ol>
              )}
            </div>
            {selectedDep ? (
              <DeploymentLog
                team={team.slug}
                app={app.id}
                deployment={selectedDep}
                actions={
                  operate && isActive(selectedDep) && !selectedDep.cancel_requested ? (
                    <CancelButton path={`${path}/deployments/${selectedDep.id}/cancel`} onError={setError} />
                  ) : null
                }
              />
            ) : (
              <p className="px-6 py-4 text-sm text-grid-muted">{t("apps.deploy.select")}</p>
            )}
          </div>
          {anyActive ? <p className="border-b border-line px-6 py-2 text-xs text-grid-muted">{t("apps.deploy.following")}</p> : null}
        </TabsContent>

        <TabsContent value="logs" className="px-6 py-6">
          <RuntimeLogsPanel path={path} team={team.slug} app={app} processes={detail.processes} operate={operate} live={!!live} />
        </TabsContent>
        <TabsContent value="env" className="px-6 py-6">
          <EnvPanel path={path} manage={manage} />
        </TabsContent>
        <TabsContent value="domains" className="px-6 py-6">
          <DomainsPanel team={team.slug} path={path} manage={manage} />
        </TabsContent>
        <TabsContent value="releases" className="px-6 py-6">
          <ReleasesPanel path={path} operate={operate} liveId={app.current_release_id} onRollback={pushDeployment} />
        </TabsContent>
        <TabsContent value="settings" className="px-6 py-6">
          <SettingsPanel team={team.slug} path={path} detail={detail} manage={manage} onSaved={(a) => setDetail((d) => (d ? { ...d, app: a } : d))} />
        </TabsContent>
      </Tabs>
    </PageBody>
  )
}

function Stat({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-2 bg-grid-bg px-6 py-4">
      <dt className="grid-micro">{label}</dt>
      <dd className="min-w-0 text-start">{children}</dd>
    </div>
  )
}

function CancelButton({ path, onError }: { path: string; onError: (m: string | null) => void }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [busy, setBusy] = useState(false)
  return (
    <Button
      size="sm"
      variant="outline"
      disabled={busy}
      onClick={async () => {
        setBusy(true)
        onError(null)
        try {
          await apiFetch(path, { method: "POST" })
        } catch (err) {
          onError(errorText(err))
        } finally {
          setBusy(false)
        }
      }}
    >
      <SquareIcon />
      {t("apps.deploy.cancel")}
    </Button>
  )
}
