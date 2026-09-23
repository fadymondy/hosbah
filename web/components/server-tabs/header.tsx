"use client"

import Link from "next/link"
import { usePathname } from "next/navigation"

import { ServerMark } from "@/components/server-mark"
import { useServerPage } from "@/components/server-tabs/context"
import { PresenceBadge } from "@/components/server-status"
import { Skeleton } from "@/components/ui/skeleton"
import { useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"
import { cn } from "@/lib/utils"

/** Each tab is its own page under /servers/{id}; "" is the overview. */
export const SERVER_TABS = ["", "apps", "databases", "proxy", "tunnels", "github", "discovered", "operations", "settings"] as const
export type ServerTab = (typeof SERVER_TABS)[number]

/**
 * The server's header and tabs: on every server page, and above every site on that server so the
 * way back to the server (and to its other sections) is always one click away. `active` forces a
 * tab (a site page highlights "apps"); otherwise it follows the URL.
 */
export function ServerHeader({ serverId, active }: { serverId: string; active?: ServerTab }) {
  const { t, locale } = useI18n()
  const { team } = useTeam()
  const pathname = usePathname() ?? ""
  const { server, connected } = useServerPage()
  const root = `/${locale}/t/${team.slug}/servers/${serverId}`

  if (!server) {
    return (
      <div className="flex items-center gap-3 border-b border-line px-6 py-4">
        <Skeleton className="size-10" />
        <Skeleton className="h-6 w-40" />
      </div>
    )
  }

  const enrolled = server.status === "enrolled"
  const tabs = SERVER_TABS.filter((tab) => enrolled || tab === "" || tab === "operations" || tab === "settings")
  const isOn = (tab: ServerTab) => {
    if (active !== undefined) return tab === active
    const href = tab ? `${root}/${tab}` : root
    return tab ? pathname === href || pathname.startsWith(href + "/") : pathname === root
  }

  return (
    <div className="border-b border-line" data-testid="server-header">
      <div className="flex flex-wrap items-end justify-between gap-4 px-6 pt-6 pb-4">
        <Link href={root} className="flex min-w-0 items-center gap-3 hover:opacity-90">
          <ServerMark server={server} size={10} />
          <span className="min-w-0">
            <span className="grid-micro block">{team.name}</span>
            <span dir="ltr" className="grid-title block truncate text-2xl">
              {server.name}
            </span>
            <span className="grid-body block text-sm">
              {t(`servers.providers.${server.provider}`)} · {t(`servers.kinds.${server.kind}`)}
            </span>
          </span>
        </Link>
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-xs text-grid-muted" aria-live="polite">
            {connected ? t("servers.detail.live") : t("servers.detail.reconnecting")}
          </span>
          <PresenceBadge server={server} />
        </div>
      </div>
      <nav
        aria-label={t("servers.tabs.label")}
        className="flex overflow-x-auto px-4 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
        data-testid="server-tabs"
      >
        {tabs.map((tab) => {
          const on = isOn(tab)
          return (
            <Link
              key={tab || "overview"}
              href={tab ? `${root}/${tab}` : root}
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
    </div>
  )
}
