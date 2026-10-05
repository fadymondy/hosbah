"use client"

import { Skeleton } from "@fadymondy/nasaq/web"
import Link from "next/link"
import { usePathname } from "next/navigation"
import { type ReactNode } from "react"
import { ChevronLeftIcon, CpuIcon, GlobeIcon, NetworkIcon, ServerIcon } from "lucide-react"

import { CoverTabs } from "@/components/cover-tabs"
import { ServerMark } from "@/components/server-mark"
import { useServerPage } from "@/components/server-tabs/context"
import { PresenceBadge } from "@/components/server-status"
import { SERVER_ITEMS } from "@/components/shell/scope-nav"

import { useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"
import { cn } from "@/lib/utils"

/** Each section is its own page under /servers/{id}; "" is the overview. */
export const SERVER_TABS = [
  "",
  "metrics",
  "users",
  "apps",
  "databases",
  "proxy",
  "tunnels",
  "github",
  "files",
  "sftp",
  "ssh",
  "terminal",
  "operations",
  "services",
  "security",
  "settings",
] as const
export type ServerTab = (typeof SERVER_TABS)[number]

function Chip({ icon, children, className }: { icon?: ReactNode; children: ReactNode; className?: string }) {
  return (
    <span
      className={cn(
        "inline-flex h-7 items-center gap-1.5 rounded-full border border-border bg-background/60 px-2.5 text-caption whitespace-nowrap text-nq-fg-body [&_svg]:size-3.5 [&_svg]:text-muted-foreground",
        className,
      )}
    >
      {icon}
      {children}
    </span>
  )
}

/**
 * The server's cover on every server page: a full-width band in the server's colour, the mark over its
 * edge, name and key facts as chips, then one tab per section. Tabs that do not fit move into a "more" menu
 * at the end of the row; the active tab always stays in the row.
 */
export function ServerHeader({
  serverId,
  root: rootProp,
  only,
  back: backProp,
  end,
}: {
  serverId: string
  /** Where the tabs live (default: the server's console pages). A panel passes its own root. */
  root?: string
  /** Show only these tabs (a panel shows its own few). */
  only?: readonly ServerTab[]
  /** The link on the cover's corner; null hides it. */
  back?: { href: string; label: string } | null
  /** Extra controls on the cover's end corner (a panel's account menu). */
  end?: ReactNode
}) {
  const { t, locale } = useI18n()
  const { team } = useTeam()
  const { server, connected } = useServerPage()
  const pathname = usePathname() ?? ""
  const base = `/${locale}/t/${team.slug}`
  const root = rootProp ?? `${base}/servers/${serverId}`

  const enrolled = server?.status === "enrolled"
  const tabs = SERVER_TABS.filter(
    (tab) => (!only || only.includes(tab)) && (enrolled || tab === "" || tab === "operations" || tab === "settings"),
  )
  const current = SERVER_TABS.find((tab) => tab && (pathname === `${root}/${tab}` || pathname.startsWith(`${root}/${tab}/`))) ?? ""
  const tint = server?.color || "var(--nq-brand, #3B82F6)"
  const back =
    backProp !== undefined
      ? backProp
      : server?.parent_server_id
        ? { href: `${base}/servers/${server.parent_server_id}/users`, label: server.parent_server_name ?? t("nav.servers") }
        : { href: `${base}/servers`, label: t("nav.servers") }
  const href = (tab: ServerTab) => (tab ? `${root}/${tab}` : root)
  const label = (tab: ServerTab) => t(`servers.tabs.${tab || "overview"}`)

  return (
    <header className="border-b border-border bg-card md:-mx-page" data-testid="server-header">
      <div
        className="relative h-28 sm:h-36"
        style={{ background: `linear-gradient(135deg, color-mix(in oklab, ${tint} 22%, var(--card)), color-mix(in oklab, ${tint} 45%, var(--card)))` }}
        data-server-cover
      >
        {server?.logo_url ? (
          // eslint-disable-next-line @next/next/no-img-element
          <img src={server.logo_url} alt="" aria-hidden className="absolute inset-0 size-full scale-125 object-cover opacity-40 blur-2xl" />
        ) : null}
        <div aria-hidden className="absolute inset-0 bg-[radial-gradient(circle_at_1px_1px,var(--border)_1px,transparent_0)] bg-size-[14px_14px] opacity-60" />
        {back === null ? null : (
          <Link
            href={back.href}
            className="absolute start-4 top-4 inline-flex items-center gap-1 rounded-full bg-background/75 px-2.5 py-1 text-caption text-muted-foreground backdrop-blur-sm hover:text-foreground sm:start-6"
          >
            <ChevronLeftIcon className="size-3.5 rtl:rotate-180" />
            {back.label}
          </Link>
        )}
        {server ? (
          <div className="absolute end-4 top-4 flex items-center gap-1.5 sm:end-6 [&>*]:bg-background/75 [&>*]:backdrop-blur-sm">
            <PresenceBadge server={server} />
            {end}
          </div>
        ) : null}
      </div>

      <div className="px-4 sm:px-6">
        <div className="-mt-9 flex flex-wrap items-start gap-x-4 gap-y-3 sm:-mt-11">
          <span className="relative rounded-xl shadow-sm ring-4 ring-card">
            {server ? <ServerMark server={server} size={22} /> : <Skeleton className="size-18 rounded-xl sm:size-22" />}
          </span>
          <div className="mt-11 min-w-0 flex-1 basis-60 sm:mt-13">
            {server ? (
              <>
                <h1 dir="ltr" className="text-h2 min-w-0 text-start text-2xl break-words">
                  {server.name}
                </h1>
                <p className="text-sm font-medium text-nq-fg-body">
                  {server.parent_server_id
                    ? t("servers.panelOn", { type: server.guest_type === "qemu" ? "VM" : "LXC", node: server.parent_server_name ?? "" })
                    : `${t(`servers.providers.${server.provider}`)} · ${t(`servers.kinds.${server.kind}`)}`}
                </p>
              </>
            ) : (
              <Skeleton className="h-7 w-48" />
            )}
          </div>
        </div>

        {server ? (
          <div className="mt-3 flex flex-wrap items-center gap-2">
            {server.ip_address ? (
              <Chip icon={<GlobeIcon />}>
                <span dir="ltr" className="font-mono">
                  {server.ip_address}
                </span>
              </Chip>
            ) : null}
            {server.hostname ? (
              <Chip icon={<ServerIcon />}>
                <span dir="ltr" className="font-mono">
                  {server.hostname}
                </span>
              </Chip>
            ) : null}
            {server.os ? (
              <Chip icon={<CpuIcon />}>
                <span dir="ltr">{[server.os, server.os_version].filter(Boolean).join(" ")}</span>
              </Chip>
            ) : null}
            {server.parent_server_id && !rootProp ? (
              <Link href={`${base}/servers/${server.parent_server_id}`}>
                <Chip icon={<NetworkIcon />} className="hover:border-nq-action hover:text-foreground">
                  {server.parent_server_name ?? server.parent_server_id}
                </Chip>
              </Link>
            ) : null}
            <span className="ms-auto text-caption text-muted-foreground" aria-live="polite">
              {connected ? t("servers.detail.live") : t("servers.detail.reconnecting")}
            </span>
          </div>
        ) : null}

        <CoverTabs
          tabs={tabs.map((tab) => ({ key: tab, href: href(tab), label: label(tab), icon: SERVER_ITEMS[tab].icon }))}
          current={current}
          label={server?.name ?? t("nav.servers")}
          testId="server-tabs"
        />
      </div>
    </header>
  )
}
