"use client"

import type { ReactNode } from "react"
import { usePathname } from "next/navigation"

import { SECURITY_PAGES, SubNav } from "@/components/shell/sub-nav"
import { useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

/**
 * Settings: General (with notifications), Security (MCP tokens, SSH keys, credentials,
 * certificates), Audit log and Billing, as one area with its own row of tabs. The URLs are the
 * old ones (/tokens, /audit, /costs…), so links and bookmarks keep working.
 */
export default function SettingsLayout({ children }: { children: ReactNode }) {
  const { t, locale } = useI18n()
  const { team, manage } = useTeam()
  const pathname = usePathname() ?? ""
  const base = `/${locale}/t/${team.slug}`
  const security = SECURITY_PAGES.map((p) => `${base}/${p}`)
  const inSecurity = security.some((p) => pathname === p || pathname.startsWith(p + "/"))

  return (
    <div className="flex flex-col">
      <SubNav
        label={t("nav.settings")}
        items={[
          { href: `${base}/settings`, label: t("nav.general") },
          { href: `${base}/tokens`, label: t("nav.security"), also: security },
          { href: `${base}/settings#notifications`, label: t("nav.notifications"), anchor: true },
          { href: `${base}/audit`, label: t("nav.audit") },
          { href: `${base}/costs`, label: t("nav.billing") },
        ]}
      />
      {inSecurity ? (
        <SubNav
          label={t("nav.security")}
          className="bg-grid-soft/40"
          items={[
            { href: `${base}/tokens`, label: t("nav.tokens") },
            { href: `${base}/ssh-keys`, label: t("nav.sshKeys") },
            // Credentials are owner/admin only (the API answers 403 to anyone else).
            ...(manage ? [{ href: `${base}/credentials`, label: t("nav.credentials") }] : []),
            { href: `${base}/certificates`, label: t("nav.certificates") },
          ]}
        />
      ) : null}
      {children}
    </div>
  )
}
