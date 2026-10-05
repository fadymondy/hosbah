"use client"

import { PageBody } from "@/components/app-shell"
import { LogDrainsCard } from "@/components/log-drains"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

export default function LogDrainsPage() {
  const { t } = useI18n()
  const { team, manage } = useTeam()
  useDocumentTitle(`${t("nav.logDrains")} — ${team.name}`)
  return (
    <PageBody title={t("nav.logDrains")} description={t("settings.logDrains.hint")} bleed divider={false}>
      <LogDrainsCard team={team.slug} manage={manage} />
    </PageBody>
  )
}
