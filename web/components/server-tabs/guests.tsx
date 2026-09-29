"use client"

import Link from "next/link"
import { BoxIcon, CpuIcon, ExternalLinkIcon, HardDriveIcon, MemoryStickIcon, MonitorIcon } from "lucide-react"

import { SectionTitle } from "@/components/states"
import { Button } from "@/components/ui/button"
import { formatBytes, type Guest } from "@/lib/servers"
import { useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

const running = (s: string) => /^(running|active|up)/i.test(s)

/**
 * Every Proxmox guest a node discovered (facts.services, kind "guest"), whether or not it is also
 * enrolled as a server of the team, plus the guests Hosbah is still creating on it (or failed to):
 * status, type, vmid, address and resources, and either "Open" (a server of the team) or "Not
 * enrolled". Guests live here, not as top-level servers on the home page.
 */
export function Guests({ guests }: { guests: Guest[] }) {
  const { t, locale } = useI18n()
  const { team } = useTeam()
  const base = `/${locale}/t/${team.slug}`

  if (guests.length === 0) {
    return (
      <div className="px-6 py-4">
        <SectionTitle title={t("servers.guests.title")} hint={t("servers.guests.description")} />
        <p className="border-y border-line py-8 text-center text-sm text-grid-muted">{t("servers.guests.empty")}</p>
      </div>
    )
  }

  return (
    <div className="px-6 py-4">
      <SectionTitle title={t("servers.guests.title")} hint={t("servers.guests.description")} />
      <div className="grid gap-3 py-4 sm:grid-cols-2 lg:grid-cols-3">
        {guests.map((g) => (
          <article key={g.server_id ?? g.vmid} className="flex flex-col gap-2 border border-line bg-grid-card p-4" data-testid="guest-card">
            <div className="flex items-center gap-2">
              <MonitorIcon className="size-4 text-grid-muted" />
              <span dir="ltr" className="min-w-0 flex-1 truncate font-medium text-grid-fg">
                {g.name || `${g.type}-${g.vmid}`}
              </span>
              <span
                aria-hidden
                className={running(g.status) ? "size-2 shrink-0 bg-[var(--grid-ok,#3fb950)]" : "size-2 shrink-0 bg-grid-muted opacity-60"}
              />
            </div>
            <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-grid-muted">
              <span className="inline-flex items-center gap-1">
                <BoxIcon className="size-3.5" />
                {t(`servers.guests.types.${g.type}`, undefined, g.type)}
                {g.vmid ? ` · ${t("servers.guests.vmid")} ${g.vmid}` : null}
              </span>
              {!running(g.status) ? <span>{t(`servers.presence.${g.status}`, undefined, g.status)}</span> : null}
              {g.address ? (
                <span dir="ltr" className="font-mono">
                  {g.address}
                </span>
              ) : null}
            </div>
            {g.cpus || g.mem_bytes || g.disk_bytes ? (
              <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-grid-muted">
                {g.cpus ? (
                  <span className="inline-flex items-center gap-1">
                    <CpuIcon className="size-3.5" />
                    {g.cpus}
                  </span>
                ) : null}
                {g.mem_bytes ? (
                  <span className="inline-flex items-center gap-1">
                    <MemoryStickIcon className="size-3.5" />
                    {formatBytes(g.mem_bytes)}
                  </span>
                ) : null}
                {g.disk_bytes ? (
                  <span className="inline-flex items-center gap-1">
                    <HardDriveIcon className="size-3.5" />
                    {formatBytes(g.disk_bytes)}
                  </span>
                ) : null}
              </div>
            ) : null}
            <div className="mt-auto pt-2">
              {g.server_id ? (
                <Button size="sm" variant="outline" nativeButton={false} render={<Link href={`${base}/servers/${g.server_id}`} />}>
                  <ExternalLinkIcon />
                  {t("servers.guests.open")}
                </Button>
              ) : (
                <span className="text-xs text-grid-muted">{t("servers.guests.notEnrolled")}</span>
              )}
            </div>
          </article>
        ))}
      </div>
    </div>
  )
}
