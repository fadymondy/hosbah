"use client"

import Link from "next/link"
import { usePathname } from "next/navigation"
import { type ReactNode } from "react"
import { ChevronLeftIcon } from "lucide-react"

import { CoverTabs } from "@/components/cover-tabs"
import { ServerMark } from "@/components/server-mark"
import { tabOf, tabsFor } from "@/components/site/sections"
import { useI18n } from "@/lib/i18n"

type CoverApp = { name: string; title?: string | null; color?: string | null; icon?: string | null; logo_url?: string | null }

/**
 * An app's cover, the same shape as a server's: a band in the app's colour, the mark over its edge, the
 * name and a line about it, its status and actions on the end, then its tabs (related sections share one). A panel opens
 * apps this way; the console keeps the sections in its sidebar.
 */
export function SiteCover({
  app,
  imported,
  root,
  back,
  description,
  actions,
}: {
  app: CoverApp
  imported: boolean
  /** The app's own page; the tabs live under it. */
  root: string
  back?: { href: string; label: string }
  description?: ReactNode
  actions?: ReactNode
}) {
  const { t } = useI18n()
  const pathname = usePathname() ?? ""
  const sections = tabsFor(imported)
  // An old section route (forwarding to its tab) still lights its tab.
  const segment = pathname.startsWith(`${root}/`) ? pathname.slice(root.length + 1).split("/")[0] : ""
  const current = tabOf(segment)
  const tint = app.color || "var(--nq-brand, #3B82F6)"

  return (
    <header className="border-b border-border bg-card md:-mx-page" data-testid="site-cover">
      <div
        className="relative h-28 sm:h-36"
        style={{ background: `linear-gradient(135deg, color-mix(in oklab, ${tint} 22%, var(--card)), color-mix(in oklab, ${tint} 45%, var(--card)))` }}
      >
        {app.logo_url ? (
          // eslint-disable-next-line @next/next/no-img-element
          <img src={app.logo_url} alt="" aria-hidden className="absolute inset-0 size-full scale-125 object-cover opacity-40 blur-2xl" />
        ) : null}
        <div aria-hidden className="absolute inset-0 bg-[radial-gradient(circle_at_1px_1px,var(--border)_1px,transparent_0)] bg-size-[14px_14px] opacity-60" />
        {back ? (
          <Link
            href={back.href}
            className="absolute start-4 top-4 inline-flex items-center gap-1 rounded-full bg-background/75 px-2.5 py-1 text-caption text-muted-foreground backdrop-blur-sm hover:text-foreground sm:start-6"
          >
            <ChevronLeftIcon className="size-3.5 rtl:rotate-180" />
            {back.label}
          </Link>
        ) : null}
      </div>

      <div className="px-4 sm:px-6">
        <div className="-mt-9 flex flex-wrap items-start gap-x-4 gap-y-3 sm:-mt-11">
          <span className="relative rounded-xl shadow-sm ring-4 ring-card">
            <ServerMark server={{ color: app.color, icon: app.icon ?? "globe", logo_url: app.logo_url }} size={22} />
          </span>
          <div className="mt-11 min-w-0 flex-1 basis-60 sm:mt-13">
            <h1 dir="auto" className="text-h2 min-w-0 text-start text-2xl break-words">
              {app.title || app.name}
            </h1>
            {description ? <div className="text-sm font-medium text-nq-fg-body">{description}</div> : null}
          </div>
          {actions ? <div className="flex flex-wrap items-center gap-2 sm:mt-13">{actions}</div> : null}
        </div>

        <CoverTabs
          tabs={sections.map((s) => ({ key: s.key, href: s.key ? `${root}/${s.key}` : root, label: t(`site.nav.${s.key || "overview"}`), icon: s.icon }))}
          current={current}
          label={app.title || app.name}
          testId="site-tabs"
        />
      </div>
    </header>
  )
}
