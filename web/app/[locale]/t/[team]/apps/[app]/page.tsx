"use client"

import Link from "next/link"
import { useParams } from "next/navigation"
import { useEffect, useState } from "react"
import { AlertCircleIcon, RocketIcon, RotateCcwIcon, RotateCwIcon, SquareIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { AppStatusBadge, DeploymentStatusBadge } from "@/components/app-status"
import { DomainsPanel } from "@/components/apps/domains"
import { EnvPanel } from "@/components/apps/env"
import { ReleasesPanel } from "@/components/apps/releases"
import { RuntimeLogsPanel } from "@/components/apps/runtime-logs"
import { SettingsPanel } from "@/components/apps/settings"
import { ConfirmButton } from "@/components/confirm-button"
import { DeploymentLog } from "@/components/deployment-log"
import { FormError, useErrorText } from "@/components/form-error"
import { Alert, AlertDescription } from "@/components/ui/alert"
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
      <PageBody title={t("nav.apps")}>
        <Alert>
          <AlertCircleIcon />
          <AlertDescription>{t("errors.notFound")}</AlertDescription>
        </Alert>
      </PageBody>
    )
  }
  if (!detail || !app) {
    return (
      <PageBody title={<Skeleton className="h-8 w-48" />}>
        <FormError message={error} />
        <Skeleton className="h-24 w-full" />
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
    >
      <FormError message={error} />
      {!operate ? <p className="text-sm text-muted-foreground">{t("apps.viewerHint")}</p> : null}
      {operate && !prev ? <p className="text-xs text-muted-foreground">{t("apps.actions.noPrevious")}</p> : null}

      <dl className="grid-cells grid sm:grid-cols-2 lg:grid-cols-5" data-testid="app-summary">
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

      <Tabs value={tab} onValueChange={(v) => setTab(v as string)}>
        <div className="overflow-x-auto">
          <TabsList variant="line">
            <TabsTrigger value="deployments">{t("apps.tabs.deployments")}</TabsTrigger>
            <TabsTrigger value="logs">{t("apps.tabs.logs")}</TabsTrigger>
            <TabsTrigger value="env">{t("apps.tabs.env")}</TabsTrigger>
            <TabsTrigger value="domains">{t("apps.tabs.domains")}</TabsTrigger>
            <TabsTrigger value="releases">{t("apps.tabs.releases")}</TabsTrigger>
            <TabsTrigger value="settings">{t("apps.tabs.settings")}</TabsTrigger>
          </TabsList>
        </div>

        <TabsContent value="deployments">
          <div className="grid gap-0 border lg:grid-cols-[minmax(0,2fr)_minmax(0,3fr)]" data-testid="deployments">
            <div className="min-w-0 lg:border-e">
              {deps.length === 0 ? (
                <p className="p-4 text-sm text-muted-foreground">{t("apps.deploy.empty")}</p>
              ) : (
                <ol className="flex max-h-[32rem] flex-col divide-y overflow-y-auto">
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
                          "flex w-full flex-col gap-1 px-4 py-3 text-start hover:bg-muted/50",
                          d.id === selected && "bg-muted",
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
              <p className="p-4 text-sm text-muted-foreground">{t("apps.deploy.select")}</p>
            )}
          </div>
          {anyActive ? <p className="pt-2 text-xs text-muted-foreground">{t("apps.deploy.following")}</p> : null}
        </TabsContent>

        <TabsContent value="logs">
          <RuntimeLogsPanel path={path} team={team.slug} app={app} processes={detail.processes} operate={operate} live={!!live} />
        </TabsContent>
        <TabsContent value="env">
          <EnvPanel path={path} manage={manage} />
        </TabsContent>
        <TabsContent value="domains">
          <DomainsPanel team={team.slug} path={path} manage={manage} />
        </TabsContent>
        <TabsContent value="releases">
          <ReleasesPanel path={path} operate={operate} liveId={app.current_release_id} onRollback={pushDeployment} />
        </TabsContent>
        <TabsContent value="settings">
          <SettingsPanel team={team.slug} path={path} detail={detail} manage={manage} onSaved={(a) => setDetail((d) => (d ? { ...d, app: a } : d))} />
        </TabsContent>
      </Tabs>
    </PageBody>
  )
}

function Stat({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-2 p-4">
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
