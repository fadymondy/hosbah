"use client"

import type { LucideIcon } from "lucide-react"
import { CheckIcon, HammerIcon } from "lucide-react"

import { SectionTitle } from "@/components/states"
import { useI18n } from "@/lib/i18n"

/**
 * A section that is designed but not built yet. It says so plainly and lists what it will do,
 * so the place in the navigation is settled and nothing on the page pretends to work.
 */
export function Planned({ section, icon: Icon }: { section: string; icon: LucideIcon }) {
  const { t } = useI18n()
  const points = [1, 2, 3, 4].map((n) => t(`site.planned.${section}.p${n}`, undefined, "")).filter(Boolean)
  return (
    <section aria-labelledby="planned-h">
      <SectionTitle id="planned-h" title={t(`site.nav.${section}`)} hint={t(`site.planned.${section}.hint`)} />
      <div className="flex flex-col gap-5 border-b border-border px-6 py-8">
        <div className="flex items-center gap-3">
          <span className="flex size-10 items-center justify-center border border-border bg-card text-nq-brand">
            <Icon className="size-5" />
          </span>
          <span className="inline-flex items-center gap-1.5 border border-dashed border-border px-2 py-1 text-xs text-muted-foreground">
            <HammerIcon className="size-3.5" />
            {t("site.notBuilt")}
          </span>
        </div>
        <ul className="flex max-w-2xl flex-col gap-2 text-sm text-nq-fg-body">
          {points.map((p) => (
            <li key={p} className="flex items-start gap-2">
              <CheckIcon className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
              <span>{p}</span>
            </li>
          ))}
        </ul>
      </div>
    </section>
  )
}
