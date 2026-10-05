"use client"

import { Button, Skeleton } from "@fadymondy/nasaq/web"
import Link from "next/link"
import { useParams } from "next/navigation"
import { type ReactNode } from "react"
import { AlertCircleIcon, ExternalLinkIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { AppStatusBadge } from "@/components/app-status"
import { ImportedBadge, originSummary } from "@/components/imported"
import { usePanelViewer } from "@/components/panel/shell"
import { ServerMark } from "@/components/server-mark"
import { SiteCover } from "@/components/site/cover"
import { useProvideSiteScope } from "@/components/shell/scope"
import { SiteProvider, useSite } from "@/components/site/context"
import { DeployButton } from "@/components/site/deploy-dialog"
import { CardPieces, ErrorBand, LoadingRows, Notice } from "@/components/states"
import { isImported } from "@/lib/apps"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import { canOperate } from "@/lib/servers"
import { useInPanel, useServerHref, useTeam, useTeamBase } from "@/lib/team"

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
  const { t } = useI18n()
  const { team } = useTeam()
  const operate = canOperate(team.role)
  const { detail, missing, error, connected } = useSite()
  const serverHref = useServerHref()
  const inPanel = useInPanel()
  // A customer from an app link has this one app: no way back to a list they cannot see.
  const appOnly = usePanelViewer()?.kind === "app"
  const base = useTeamBase()
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
  const host = detail.domains[0]

  const description = app.description || (imported ? originSummary(app, t) : <span dir="ltr" className="font-mono text-xs">{app.repo_url} · {app.branch}</span>)
  const actions = (
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
      {operate && !imported ? <DeployButton /> : null}
    </>
  )

  // A panel opens an app on its own cover with a tab per section (the console keeps them in the sidebar).
  if (inPanel) {
    return (
      <>
        <SiteCover
          app={app}
          imported={imported}
          root={`${base}/apps/${encodeURIComponent(app.name)}`}
          back={appOnly ? undefined : { href: `${base}/apps`, label: t("shell.scope.allApps") }}
          description={description}
          actions={actions}
        />
        <CardPieces>
          <ErrorBand message={error} />
          {children}
        </CardPieces>
      </>
    )
  }

  return (
    <PageBody
      eyebrow={
        <Link href={serverHref(app.server_id, "apps")} className="hover:underline">
          <span dir="ltr">{app.server_name || t("nav.apps")}</span>
        </Link>
      }
      title={
        <span className="flex items-center gap-3">
          <ServerMark server={{ color: app.color, icon: app.icon ?? "globe", logo_url: app.logo_url }} size={10} />
          <span dir="auto">{app.title || app.name}</span>
        </span>
      }
      description={description}
      actions={actions}
      bleed
    >
      <CardPieces>
        <ErrorBand message={error} />
        {children}
      </CardPieces>
    </PageBody>
  )
}
