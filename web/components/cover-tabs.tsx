"use client"

import { Button, DropdownMenu, DropdownMenuContent, DropdownMenuGroup, DropdownMenuItem, DropdownMenuTrigger, Tabs, TabsList, TabsTab } from "@fadymondy/nasaq/web"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { useLayoutEffect, useRef, useState } from "react"
import { EllipsisIcon, type LucideIcon } from "lucide-react"

import { useI18n } from "@/lib/i18n"

export type CoverTab = { key: string; href: string; label: string; icon: LucideIcon }

/** Width kept free for the "more" button when the tabs overflow. */
const MORE_WIDTH = 48

/**
 * The tab row under a cover (a server's or an app's): one tab per section. Tabs that do not fit move into
 * a "more" menu at the end of the row; the active tab always stays in the row.
 */
export function CoverTabs({ tabs, current, label, testId }: { tabs: CoverTab[]; current: string; label: string; testId?: string }) {
  const { t, locale } = useI18n()
  const router = useRouter()

  // Measure every tab in a hidden row, then show as many as fit beside the "more" button.
  const rowRef = useRef<HTMLDivElement>(null)
  const measureRef = useRef<HTMLUListElement>(null)
  const [fit, setFit] = useState(tabs.length)
  const tabKey = tabs.map((tab) => `${tab.key}:${tab.label}`).join(",")
  useLayoutEffect(() => {
    const row = rowRef.current
    const measure = measureRef.current
    if (!row || !measure) return
    const compute = () => {
      const widths = Array.from(measure.children).map((el) => (el as HTMLElement).offsetWidth + 4)
      const avail = row.clientWidth
      if (widths.reduce((a, b) => a + b, 0) <= avail) return setFit(widths.length)
      let used = MORE_WIDTH
      let n = 0
      while (n < widths.length && used + widths[n] <= avail) used += widths[n++]
      setFit(Math.max(1, n))
    }
    compute()
    const ro = new ResizeObserver(compute)
    ro.observe(row)
    return () => ro.disconnect()
  }, [tabKey, locale])

  let shown = tabs.slice(0, fit)
  const active = tabs.find((tab) => tab.key === current)
  if (active && !shown.includes(active)) shown = [...shown.slice(0, -1), active]
  const hidden = tabs.filter((tab) => !shown.includes(tab))

  return (
    // Shifted 1px over the header's bottom border so the active tab opens into the page.
    <nav aria-label={label} className="relative top-px mt-4" data-testid={testId}>
      <div ref={rowRef} className="relative min-w-0">
        <ul ref={measureRef} aria-hidden className="pointer-events-none invisible absolute flex gap-1">
          {tabs.map((tab) => (
            <li key={tab.key || "overview"} className="flex h-10 shrink-0 items-center gap-2 border border-transparent px-3.5 text-sm font-medium whitespace-nowrap [&_svg]:size-4">
              <tab.icon />
              {tab.label}
            </li>
          ))}
        </ul>
        <Tabs value={current} onValueChange={(v) => router.push(tabs.find((tab) => tab.key === v)?.href ?? "")}>
          {/* Open tabs: the active one takes the page background and drops its bottom edge, so it runs into the content below. */}
          <TabsList variant="underline" className="w-full gap-1 border-b-0">
            {shown.map((tab) => (
              <TabsTab
                key={tab.key || "overview"}
                value={tab.key}
                nativeButton={false}
                render={<Link href={tab.href} />}
                className="h-10 shrink-0 rounded-t-lg border border-b-0 border-transparent px-3.5 hover:bg-background/60 data-active:border-border data-active:bg-background"
                data-tab={tab.key || "overview"}
              >
                <tab.icon />
                {tab.label}
              </TabsTab>
            ))}
            {hidden.length ? (
              <DropdownMenu>
                <DropdownMenuTrigger render={<Button variant="ghost" size="icon" className="mb-1 shrink-0" aria-label={t("servers.tabs.more")} data-tab-more />}>
                  <EllipsisIcon />
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end" className="min-w-48">
                  <DropdownMenuGroup>
                    {hidden.map((tab) => (
                      <DropdownMenuItem key={tab.key || "overview"} render={<Link href={tab.href} />}>
                        <tab.icon />
                        {tab.label}
                      </DropdownMenuItem>
                    ))}
                  </DropdownMenuGroup>
                </DropdownMenuContent>
              </DropdownMenu>
            ) : null}
          </TabsList>
        </Tabs>
      </div>
    </nav>
  )
}
