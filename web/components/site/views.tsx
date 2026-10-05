"use client"

import { Sheet, SheetBody, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@fadymondy/nasaq/web"
import { usePathname, useRouter, useSearchParams } from "next/navigation"
import { type ComponentType } from "react"

import { SiteAnalytics } from "@/components/site/analytics"
import { SiteApm } from "@/components/site/apm"
import { SiteBrowsers } from "@/components/site/browsers"
import { useLoadedSiteOptional } from "@/components/site/context"
import { SiteCache } from "@/components/site/cache"
import { SiteDeployConfig } from "@/components/site/deploy-config"
import { SiteDeployments } from "@/components/site/deployments"
import { SiteDNS } from "@/components/site/dns"
import { SiteMonitors } from "@/components/site/monitors"
import { SiteDatabases, SiteProxy, SiteWorkers } from "@/components/site/panels"
import { SiteQueues } from "@/components/site/queues"
import { SiteRealtime } from "@/components/site/realtime"
import { SiteReleases } from "@/components/site/releases"
import { SiteRepository } from "@/components/site/repository"
import { SiteRules } from "@/components/site/rules"
import { SiteSchedules } from "@/components/site/schedules"
import { SITE_SECTIONS } from "@/components/site/sections"
import { SiteSecurity } from "@/components/site/security"
import { SiteStacks } from "@/components/site/stacks"
import { SiteStorage } from "@/components/site/storage"
import { SiteWebVitals } from "@/components/site/web-vitals"
import { useI18n } from "@/lib/i18n"
import { cn } from "@/lib/utils"

/** The section pages that live inside a merged tab (as a view) or the architecture's side sheet. */
export const SECTION_VIEWS: Record<string, ComponentType> = {
  analytics: SiteAnalytics,
  "web-vitals": SiteWebVitals,
  apm: SiteApm,
  deployments: SiteDeployments,
  releases: SiteReleases,
  repository: SiteRepository,
  "deploy-script": SiteDeployConfig,
  proxy: SiteProxy,
  dns: SiteDNS,
  cache: SiteCache,
  rules: SiteRules,
  monitors: SiteMonitors,
  security: SiteSecurity,
  databases: SiteDatabases,
  storage: SiteStorage,
  workers: SiteWorkers,
  queues: SiteQueues,
  schedules: SiteSchedules,
  realtime: SiteRealtime,
  browsers: SiteBrowsers,
  stacks: SiteStacks,
}

/**
 * A merged tab's own pages, one at a time behind a row of counter tiles (?view=…, so a view can be linked
 * to and survives a reload). The first view is the default.
 */
export function SiteViews({ views }: { views: readonly string[] }) {
  const { t } = useI18n()
  const router = useRouter()
  const pathname = usePathname() ?? ""
  const params = useSearchParams()
  const asked = params.get("view") ?? ""
  const current = views.includes(asked) ? asked : views[0]
  const View = SECTION_VIEWS[current]
  const go = (v: string) => {
    const next = new URLSearchParams(params.toString())
    if (v === views[0]) next.delete("view")
    else next.set("view", v)
    const q = next.toString()
    router.replace(q ? `${pathname}?${q}` : pathname, { scroll: false })
  }
  // The counter on each tile, from what the site already loaded (no extra requests).
  const site = useLoadedSiteOptional()
  const values: Record<string, string | number | null | undefined> = site
    ? {
        deployments: site.deployments[0] ? `#${site.deployments[0].number}` : 0,
        releases: site.detail.live_release ? `v${site.detail.live_release.version}` : null,
        repository: site.detail.app.branch,
        "deploy-script": site.detail.app.runtime,
        proxy: site.detail.domains.length,
        workers: site.detail.processes.length,
      }
    : {}

  return (
    <div className="flex flex-col gap-4">
      <div role="tablist" aria-orientation="horizontal" className={cn("grid grid-cols-2 gap-3", views.length >= 4 ? "lg:grid-cols-4" : "sm:grid-cols-3")}>
        {views.map((v) => {
          const Icon = SITE_SECTIONS.find((s) => s.key === v)?.icon
          const on = v === current
          const value = values[v]
          return (
            <button
              key={v}
              type="button"
              role="tab"
              aria-selected={on}
              data-view={v}
              onClick={() => go(v)}
              className={cn(
                "flex min-w-0 items-center gap-3 rounded-xl border bg-card px-4 py-3 text-start transition-colors outline-none focus-visible:outline-2 focus-visible:outline-nq-focus",
                on ? "border-nq-brand bg-nq-brand/5 shadow-xs" : "border-border hover:bg-secondary",
              )}
            >
              <span
                className={cn(
                  "flex size-9 shrink-0 items-center justify-center rounded-lg border [&_svg]:size-4",
                  on ? "border-nq-brand/30 bg-nq-brand/10 text-nq-brand" : "border-border bg-background text-muted-foreground",
                )}
              >
                {Icon ? <Icon /> : null}
              </span>
              <span className="flex min-w-0 flex-1 flex-col">
                <span className={cn("truncate text-sm font-medium", on ? "text-foreground" : "text-nq-fg-body")}>{t(`site.nav.${v}`)}</span>
                <span className="truncate text-xs text-muted-foreground">{t(`site.viewCaption.${v}`, undefined, "")}</span>
              </span>
              {value != null && value !== "" ? (
                <span dir="ltr" className="max-w-24 shrink-0 truncate text-lg font-semibold tabular-nums text-foreground">
                  {value}
                </span>
              ) : null}
            </button>
          )
        })}
      </div>
      {View ? <View key={current} /> : null}
    </div>
  )
}

/** One section in a side sheet over the architecture (a node opened from the flow). */
export function SiteSectionSheet({ section, onClose }: { section: string | null; onClose: () => void }) {
  const { t } = useI18n()
  const View = section ? SECTION_VIEWS[section] : undefined
  return (
    <Sheet open={!!View} onOpenChange={(open) => (open ? undefined : onClose())}>
      <SheetContent side="end" className="w-full gap-0 p-0 sm:max-w-3xl">
        {/* The section's own title and hint head the sheet; the sheet's are for assistive tech only. */}
        <SheetHeader className="sr-only">
          <SheetTitle>{section ? t(`site.nav.${section}`) : ""}</SheetTitle>
          <SheetDescription>{t("site.architecture.sheetHint")}</SheetDescription>
        </SheetHeader>
        <SheetBody data-cards className="overflow-y-auto px-4 pt-8 pb-6 [&_section>*+*]:mt-3">{View ? <View /> : null}</SheetBody>
      </SheetContent>
    </Sheet>
  )
}
