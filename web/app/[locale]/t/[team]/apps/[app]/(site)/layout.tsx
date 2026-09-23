"use client"

import Link from "next/link"
import { useParams, usePathname } from "next/navigation"
import { useState, type ReactNode } from "react"
import { AlertCircleIcon, ExternalLinkIcon, RocketIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { AppStatusBadge } from "@/components/app-status"
import { useErrorText } from "@/components/form-error"
import { ImportedBadge, originSummary } from "@/components/imported"
import { ServerMark } from "@/components/server-mark"
import { ServerProvider } from "@/components/server-tabs/context"
import { ServerHeader } from "@/components/server-tabs/header"
import { SiteProvider, useSite } from "@/components/site/context"
import { sectionsFor, SITE_GROUPS } from "@/components/site/sections"
import { ErrorBand, LoadingRows, Notice } from "@/components/states"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { apiFetch } from "@/lib/api"
import { isImported, type Deployment } from "@/lib/apps"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import { canOperate } from "@/lib/servers"
import { useTeam } from "@/lib/team"
import { cn } from "@/lib/utils"

/** A site: its header, a sub-sidebar with one item per section, and the section's own page. */
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
  const pathname = usePathname() ?? ""
  const errorText = useErrorText()
  const operate = canOperate(team.role)
  const { detail, missing, error, setError, connected, path, pushDeployment } = useSite()
  const [busy, setBusy] = useState(false)
  const app = detail?.app
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
  const root = `${base}/apps/${encodeURIComponent(app.name)}`
  const sections = sectionsFor(imported)
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

  // The server's header and tabs stay on top of every site page (the site is one of its apps).
  return (
    <ServerProvider serverId={app.server_id}>
    <ServerHeader serverId={app.server_id} active="apps" />
    <PageBody
      eyebrow={
        <Link href={`${base}/servers/${app.server_id}/apps`} className="hover:underline">
          {t("nav.apps")}
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
          <span className="text-xs text-grid-muted" aria-live="polite">
            {connected ? t("servers.detail.live") : t("servers.detail.reconnecting")}
          </span>
          {imported ? <ImportedBadge item={app} /> : <AppStatusBadge status={app.status} />}
          {host ? (
            <Button variant="outline" nativeButton={false} render={<a href={`${host.tls === "off" ? "http" : "https"}://${host.hostname}`} target="_blank" rel="noreferrer" />}>
              <ExternalLinkIcon />
              {t("site.visit")}
            </Button>
          ) : null}
          {operate && !imported ? (
            <Button disabled={busy} onClick={() => void deploy()}>
              <RocketIcon />
              {t("apps.actions.deploy")}
            </Button>
          ) : null}
        </>
      }
      bleed
    >
      <div className="grid min-h-[60vh] md:grid-cols-[13rem_minmax(0,1fr)]">
        <nav
          aria-label={t("site.nav.label")}
          data-testid="site-nav"
          className="flex gap-1 overflow-x-auto border-b border-line px-3 py-2 [scrollbar-width:none] md:flex-col md:gap-0 md:overflow-visible md:border-e md:border-b-0 md:px-0 md:py-3 [&::-webkit-scrollbar]:hidden"
        >
          {SITE_GROUPS.map((g) => {
            const items = sections.filter((s) => s.group === g)
            if (!items.length) return null
            return (
              <div key={g} className="flex shrink-0 gap-1 md:flex-col md:gap-0">
                {g !== "site" ? <p className="grid-micro hidden px-4 pt-4 pb-1 md:block">{t(`site.groups.${g}`)}</p> : null}
                {items.map((s) => {
                  const href = s.key ? `${root}/${s.key}` : root
                  const on = s.key ? pathname === href || pathname.startsWith(href + "/") : pathname === root
                  return (
                    <Link
                      key={s.key || "overview"}
                      href={href}
                      aria-current={on ? "page" : undefined}
                      className={cn(
                        "flex shrink-0 items-center gap-2.5 border-b-2 px-3 py-2 text-sm whitespace-nowrap md:border-s-2 md:border-b-0 md:px-4",
                        on ? "border-[var(--grid-brand)] bg-grid-soft font-medium text-grid-fg" : "border-transparent text-grid-body hover:bg-grid-soft hover:text-grid-fg",
                      )}
                    >
                      <s.icon className="size-4 shrink-0" />
                      <span className="flex-1">{t(`site.nav.${s.key || "overview"}`)}</span>
                      {!s.ready ? <span className="hidden text-xs text-grid-muted uppercase md:inline">{t("site.soon")}</span> : null}
                    </Link>
                  )
                })}
              </div>
            )
          })}
        </nav>
        <div className="min-w-0">
          <ErrorBand message={error} />
          {children}
        </div>
      </div>
    </PageBody>
    </ServerProvider>
  )
}
