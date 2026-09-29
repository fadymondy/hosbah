"use client"

import Link from "next/link"
import { useCallback, useEffect, useState } from "react"
import { BoxesIcon, PlusIcon, Trash2Icon } from "lucide-react"

import { ServerMark } from "@/components/server-mark"
import { ConfirmButton } from "@/components/confirm-button"
import { EditDialog, ListSearch, matches } from "@/components/server-tabs/list-tools"
import { Checkbox } from "@/components/ui/checkbox"
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"

import { AppStatusBadge, DeploymentStatusBadge } from "@/components/app-status"
import { DomainsSummary } from "@/components/apps/domains"
import { ImportedBadge, originSummary } from "@/components/imported"
import { useErrorText } from "@/components/form-error"
import { ErrorBand, LoadingRows, SectionTitle, TableBand } from "@/components/states"
import { Button } from "@/components/ui/button"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { apiFetch } from "@/lib/api"
import { isImported, runtimeLabel, shortSha, type App } from "@/lib/apps"
import { useFormatDate, useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

/** The apps (sites) on one server. */
export function ServerApps({ serverId }: { serverId: string }) {
  const { t, locale } = useI18n()
  const { team, manage } = useTeam()
  const fmt = useFormatDate()
  const errorText = useErrorText()
  const [apps, setApps] = useState<App[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [q, setQ] = useState("")
  const base = `/${locale}/t/${team.slug}`

  const load = useCallback(async () => {
    try {
      const res = await apiFetch<{ apps: App[] }>(`/api/teams/${team.slug}/apps`)
      setApps((res.apps ?? []).filter((a) => a.server_id === serverId))
      setError(null)
    } catch (err) {
      setError(errorText(err))
      setApps((a) => a ?? [])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [team.slug, serverId])

  useEffect(() => {
    void load()
    const id = setInterval(load, 10_000) // the detail page is live; the list refreshes
    return () => clearInterval(id)
  }, [load])

  const shown = (apps ?? []).filter((a) =>
    matches(q, a.name, a.title, a.branch, a.repo_url, a.runtime, a.status, ...(a.domains ?? []).map((d) => d.hostname)),
  )

  const add = manage ? (
    <Button nativeButton={false} render={<Link href={`${base}/apps/new?server=${serverId}`} />}>
      <PlusIcon />
      {t("apps.add")}
    </Button>
  ) : null

  return (
    <section aria-labelledby="tab-h">
      <SectionTitle id="tab-h" title={t("nav.apps")} hint={t("apps.description")} action={add} />
      <ErrorBand message={error} />
      {apps === null ? (
        <LoadingRows />
      ) : apps.length === 0 ? (
        <Empty className="border-b">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <BoxesIcon />
            </EmptyMedia>
            <EmptyTitle>{t("apps.emptyTitle")}</EmptyTitle>
            <EmptyDescription>{manage ? t("apps.emptyHint") : t("common.readOnly")}</EmptyDescription>
          </EmptyHeader>
          {add}
        </Empty>
      ) : (
        <>
        <ListSearch value={q} onChange={setQ} placeholder={t("lists.searchApps")} shown={shown.length} total={apps.length} />
        {shown.length === 0 ? <p className="border-b border-line px-6 py-8 text-center text-sm text-grid-muted">{t("home.noMatch")}</p> : null}
        <TableBand>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("apps.name")}</TableHead>
                <TableHead>{t("apps.status")}</TableHead>
                <TableHead>{t("apps.server")}</TableHead>
                <TableHead>{t("apps.runtime")}</TableHead>
                <TableHead>{t("apps.latestDeploy")}</TableHead>
                <TableHead>{t("apps.domainsLabel")}</TableHead>
                {manage ? <TableHead className="text-end">{t("common.actions")}</TableHead> : null}
              </TableRow>
            </TableHeader>
            <TableBody>
              {shown.map((a) => {
                const d = a.latest_deployment
                return (
                  <TableRow key={a.id} data-app={a.name}>
                    <TableCell>
                      <Link href={`${base}/apps/${a.name}`} className="flex items-center gap-2.5 font-medium underline-offset-4 hover:underline">
                        <ServerMark server={{ color: a.color, icon: a.icon ?? "globe", logo_url: a.logo_url }} size={6} />
                        <span dir="auto">{a.title || a.name}</span>
                      </Link>
                      <div dir="ltr" className="max-w-64 truncate text-start font-mono text-xs text-muted-foreground">
                        {isImported(a) ? originSummary(a, t) : a.branch}
                      </div>
                    </TableCell>
                    <TableCell>
                      {isImported(a) ? <ImportedBadge item={a} /> : <AppStatusBadge status={a.status} />}
                    </TableCell>
                    <TableCell dir="ltr" className="text-start font-mono text-xs">
                      {a.server_name ? (
                        <Link href={`${base}/servers/${a.server_id}`} className="underline-offset-4 hover:underline">
                          {a.server_name}
                        </Link>
                      ) : (
                        "—"
                      )}
                    </TableCell>
                    <TableCell dir="ltr" className="text-start font-mono text-xs">
                      {runtimeLabel(a.runtime) ?? "—"}
                    </TableCell>
                    <TableCell>
                      {isImported(a) ? (
                        <span className="text-xs text-muted-foreground">{t("imported.notDeployed")}</span>
                      ) : d ? (
                        <div className="flex flex-col items-start gap-1">
                          <div className="flex items-center gap-2">
                            <span dir="ltr" className="font-mono text-xs">
                              #{d.number} · {shortSha(d.commit_sha)}
                            </span>
                            <DeploymentStatusBadge status={d.status} />
                          </div>
                          <span dir="ltr" className="text-xs text-muted-foreground">
                            {fmt(d.created_at)}
                          </span>
                        </div>
                      ) : (
                        <span className="text-xs text-muted-foreground">{t("apps.neverDeployed")}</span>
                      )}
                    </TableCell>
                    <TableCell dir="ltr" className="text-start font-mono text-xs">
                      <DomainsSummary domains={a.domains ?? []} />
                    </TableCell>
                    {manage ? (
                      <TableCell>
                        <div className="flex flex-wrap justify-end gap-2">
                          {isImported(a) ? (
                            <Button size="sm" variant="outline" nativeButton={false} render={<Link href={`${base}/apps/${a.name}`} />}>
                              {t("lists.manage")}
                            </Button>
                          ) : (
                            <>
                              <EditApp app={a} team={team.slug} onSaved={load} />
                              <ConfirmButton
                                icon={<Trash2Icon />}
                                label={t("common.delete")}
                                title={t("lists.deleteApp", { name: a.name })}
                                description={t("lists.deleteAppBody")}
                                confirmLabel={t("common.delete")}
                                onConfirm={async () => {
                                  try {
                                    await apiFetch(`/api/teams/${team.slug}/apps/${a.name}`, { method: "DELETE" })
                                  } catch (err) {
                                    throw new Error(errorText(err))
                                  }
                                  await load()
                                }}
                              />
                            </>
                          )}
                        </div>
                      </TableCell>
                    ) : null}
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        </TableBand>
        </>
      )}
    </section>
  )
}

/** Edit what an app deploys with: branch, port, health check, auto-deploy (applies from the next deploy). */
function EditApp({ app, team, onSaved }: { app: App; team: string; onSaved: () => Promise<void> }) {
  const { t } = useI18n()
  const [branch, setBranch] = useState("")
  const [port, setPort] = useState("")
  const [health, setHealth] = useState("")
  const [paused, setPaused] = useState(false)
  return (
    <EditDialog
      title={t("lists.editApp", { name: app.name })}
      description={t("lists.editAppHint")}
      onOpen={() => {
        setBranch(app.branch ?? "")
        setPort(String(app.port))
        setHealth(app.healthcheck_path)
        setPaused(app.autodeploy_paused)
      }}
      onSave={async () => {
        await apiFetch(`/api/teams/${team}/apps/${app.name}`, {
          method: "PATCH",
          body: { branch: branch.trim(), port: Number(port), healthcheck_path: health.trim(), autodeploy_paused: paused },
        })
        await onSaved()
      }}
    >
      <FieldGroup>
        <Field>
          <FieldLabel htmlFor="ea-branch">{t("lists.branch")}</FieldLabel>
          <Input id="ea-branch" dir="ltr" value={branch} onChange={(e) => setBranch(e.target.value)} />
        </Field>
        <Field>
          <FieldLabel htmlFor="ea-port">{t("lists.port")}</FieldLabel>
          <Input id="ea-port" dir="ltr" type="number" min={1} max={65535} required value={port} onChange={(e) => setPort(e.target.value)} />
        </Field>
        <Field>
          <FieldLabel htmlFor="ea-health">{t("lists.healthPath")}</FieldLabel>
          <Input id="ea-health" dir="ltr" value={health} onChange={(e) => setHealth(e.target.value)} />
        </Field>
        <label className="flex items-center gap-2 text-sm">
          <Checkbox checked={paused} onCheckedChange={(v) => setPaused(v === true)} />
          {t("lists.pauseAutodeploy")}
        </label>
      </FieldGroup>
    </EditDialog>
  )
}
