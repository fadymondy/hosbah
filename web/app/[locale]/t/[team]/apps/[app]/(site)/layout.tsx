"use client"

import { Button, Skeleton } from "@fadymondy/nasaq/web"
import Link from "next/link"
import { useParams } from "next/navigation"
import { useState, type ReactNode } from "react"
import { AlertCircleIcon, ExternalLinkIcon, RocketIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { AppStatusBadge } from "@/components/app-status"
import { useErrorText } from "@/components/form-error"
import { ImportedBadge, originSummary } from "@/components/imported"
import { ServerMark } from "@/components/server-mark"
import { useProvideSiteScope } from "@/components/shell/scope"
import { SiteProvider, useSite } from "@/components/site/context"
import { ErrorBand, LoadingRows, Notice } from "@/components/states"


import { apiFetch } from "@/lib/api"
import { isImported, type Deployment } from "@/lib/apps"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import { canOperate } from "@/lib/servers"
import { useTeam } from "@/lib/team"

/** A site: its header and the section's own page. The sections are the shell's sidebar while a site is
 * open (components/shell/scope-nav.tsx), under the breadcrumb Team › Server › Site. */
export default function SiteLayout({ children }: { children: ReactNode }) {
  const { app } = useParams<{ app: string }>()
  return (
    <SiteProvider name={decodeURIComponent(app)}>
      <Frame>{children}</Frame>
    </SiteProvider>
  )
}

function Frame({ children }: { children: ReactNode }) {
  const { t, locale } = useI18n()
  const { team } = useTeam()
  const errorText = useErrorText()
  const operate = canOperate(team.role)
  const { detail, missing, error, setError, connected, path, pushDeployment } = useSite()
  const [busy, setBusy] = useState(false)
  const app = detail?.app
  useProvideSiteScope(
    app
      ? { name: app.name, title: app.title, color: app.color, icon: app.icon, logo_url: app.logo_url, imported: isImported(app), serverId: app.server_id }
      : null,
    app ? { id: app.server_id, name: app.server_name ?? "" } : null,
  )
  useDocumentTitle(`${app?.title || app?.name || t("nav.apps")} — ${team.name}`)

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

  const imported = isImported(app)
  const base = `/${locale}/t/${team.slug}`
  const host = detail.domains[0]

  async function deploy() {
    setBusy(true)
    setError(null)
    try {
      pushDeployment(await apiFetch<Deployment>(`${path}/deployments`, { method: "POST", body: {} }))
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <PageBody
      eyebrow={
        <Link href={`${base}/servers/${app.server_id}/apps`} className="hover:underline">
          <span dir="ltr">{app.server_name || t("nav.apps")}</span>
        </Link>
      }
      title={
        <span className="flex items-center gap-3">
          <ServerMark server={{ color: app.color, icon: app.icon ?? "globe", logo_url: app.logo_url }} size={10} />
          <span dir="auto">{app.title || app.name}</span>
        </span>
      }
      description={app.description || (imported ? originSummary(app, t) : <span dir="ltr" className="font-mono text-xs">{app.repo_url} · {app.branch}</span>)}
      actions={
        <>
          <span className="text-xs text-muted-foreground" aria-live="polite">
            {connected ? t("servers.detail.live") : t("servers.detail.reconnecting")}
          </span>
          {imported ? <ImportedBadge item={app} /> : <AppStatusBadge status={app.status} />}
          {host ? (
            <Button variant="secondary" nativeButton={false} render={<a href={`${host.tls === "off" ? "http" : "https"}://${host.hostname}`} target="_blank" rel="noreferrer" />}>
              <ExternalLinkIcon />
              {t("site.visit")}
            </Button>
          ) : null}
          {operate && !imported ? (
            <Button variant="primary" disabled={busy} onClick={() => void deploy()}>
              <RocketIcon />
              {t("apps.actions.deploy")}
            </Button>
          ) : null}
        </>
      }
      bleed
    >
      <ErrorBand message={error} />
      {children}
    </PageBody>
  )
}
