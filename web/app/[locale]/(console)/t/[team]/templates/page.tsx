"use client"

import { PageBody } from "@/components/app-shell"
import { TeamSiteTemplates } from "@/components/site-templates"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

/** MH-576 B11: the team's YAML site templates — built-in stack templates plus whatever the team
 * has saved — preview a template's render, or create an app from it in one step. */
export default function TemplatesPage() {
  const { t } = useI18n()
  const { team } = useTeam()
  useDocumentTitle(`${t("nav.templates")} — ${team.name}`)

  return (
    <PageBody title={t("nav.templates")} description={t("siteTemplates.hint")} bleed>
      <TeamSiteTemplates />
    </PageBody>
  )
}
