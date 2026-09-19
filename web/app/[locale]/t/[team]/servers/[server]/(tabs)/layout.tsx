"use client"

import Link from "next/link"
import { useParams, usePathname } from "next/navigation"
import type { ReactNode } from "react"
import { AlertCircleIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { ServerProvider, useServerPage } from "@/components/server-tabs/context"
import { ServerMark } from "@/components/server-mark"
import { PresenceBadge } from "@/components/server-status"
import { ErrorBand, LoadingRows, Notice } from "@/components/states"
import { Skeleton } from "@/components/ui/skeleton"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"
import { cn } from "@/lib/utils"

/** Each tab is its own page under /servers/{id}; "" is the overview. */
const TABS = ["", "apps", "databases", "proxy", "tunnels", "github", "discovered", "operations", "settings"] as const

export default function ServerLayout({ children }: { children: ReactNode }) {
  const { server: serverId } = useParams<{ server: string }>()
  return (
    <ServerProvider serverId={serverId}>
      <Frame serverId={serverId}>{children}</Frame>
    </ServerProvider>
  )
}

function Frame({ serverId, children }: { serverId: string; children: ReactNode }) {
  const { t, locale } = useI18n()
  const { team } = useTeam()
  const pathname = usePathname()
  const { server, missing, error, connected } = useServerPage()
  useDocumentTitle(`${server?.name ?? t("nav.servers")} — ${team.name}`)
  const root = `/${locale}/t/${team.slug}/servers/${serverId}`

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
  const tabs = TABS.filter((tab) => enrolled || tab === "" || tab === "operations" || tab === "settings")

  return (
    <PageBody
      title={
        <span className="flex items-center gap-3">
          <ServerMark server={server} size={10} />
          <span dir="ltr">{server.name}</span>
        </span>
      }
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
      <nav aria-label={t("servers.tabs.label")} className="flex overflow-x-auto border-b border-line px-4 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden" data-testid="server-tabs">
        {tabs.map((tab) => {
          const href = tab ? `${root}/${tab}` : root
          const on = tab ? pathname.startsWith(href) : pathname === root
          return (
            <Link
              key={tab || "overview"}
              href={href}
              aria-current={on ? "page" : undefined}
              className={cn(
                "-mb-px shrink-0 border-b-2 px-3 py-2.5 text-sm whitespace-nowrap",
                on ? "border-[var(--grid-brand)] font-medium text-grid-fg" : "border-transparent text-grid-muted hover:text-grid-fg",
              )}
            >
              {t(`servers.tabs.${tab || "overview"}`)}
            </Link>
          )
        })}
      </nav>
      <ErrorBand message={error} />
      {children}
    </PageBody>
  )
}
