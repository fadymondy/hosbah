"use client"

import { PageBody } from "@/components/app-shell"
import { StatusPagesSection } from "@/components/status-pages"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

export default function StatusPagesPage() {
  const { t } = useI18n()
  const { team, manage } = useTeam()
  useDocumentTitle(`${t("nav.statusPages")} — ${team.name}`)
  return (
    <PageBody title={t("nav.statusPages")} description={t("settings.statusPagesHint")} bleed divider={false}>
      <StatusPagesSection team={team.slug} manage={manage} />
    </PageBody>
  )
}
