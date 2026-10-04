"use client"

import type { ReactNode } from "react"

import { SubNav } from "@/components/shell/sub-nav"
import { useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

/** Team → Members, with the pending invitations as its second tab (both URLs are unchanged). */
export default function PeopleLayout({ children }: { children: ReactNode }) {
  const { t, locale } = useI18n()
  const { team } = useTeam()
  const base = `/${locale}/t/${team.slug}`
  return (
    <div className="flex flex-col">
      <SubNav
        label={t("nav.team")}
        items={[
          { href: `${base}/members`, label: t("nav.members") },
          { href: `${base}/invitations`, label: t("nav.invitations") },
        ]}
      />
      {children}
    </div>
  )
}
