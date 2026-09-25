"use client"

import Link from "next/link"

import { ServerMark } from "@/components/server-mark"
import { useServerPage } from "@/components/server-tabs/context"
import { PresenceBadge } from "@/components/server-status"
import { Skeleton } from "@/components/ui/skeleton"
import { useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

/** Each section is its own page under /servers/{id}; "" is the overview. */
export const SERVER_TABS = [
  "",
  "metrics",
  "apps",
  "databases",
  "guests",
  "proxy",
  "tunnels",
  "github",
  "discovered",
  "import",
  "files",
  "sftp",
  "terminal",
  "operations",
  "services",
  "security",
  "settings",
] as const
export type ServerTab = (typeof SERVER_TABS)[number]

/**
 * The server's header on every server page. Its sections are the shell's sidebar while a server is
 * open (components/shell/scope-nav.tsx), and the breadcrumb switches to another server.
 */
export function ServerHeader({ serverId }: { serverId: string }) {
  const { t, locale } = useI18n()
  const { team } = useTeam()
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

  return (
    <div className="border-b border-line" data-testid="server-header">
      <div className="flex flex-wrap items-end justify-between gap-4 px-6 py-5">
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
        {server.parent_server_id ? (
          <Link
            href={`${root.replace(/\/servers\/[^/]+$/, "")}/servers/${server.parent_server_id}`}
            className="inline-flex shrink-0 items-center gap-1.5 border border-line bg-grid-soft px-2 py-1 text-xs text-grid-muted hover:text-grid-fg"
          >
            {t("servers.guests.parentLink", { node: server.parent_server_name ?? server.parent_server_id })}
          </Link>
        ) : null}
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-xs text-grid-muted" aria-live="polite">
            {connected ? t("servers.detail.live") : t("servers.detail.reconnecting")}
          </span>
          <PresenceBadge server={server} />
        </div>
      </div>
    </div>
  )
}
