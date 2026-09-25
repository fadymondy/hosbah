"use client"

import { PageBody } from "@/components/app-shell"
import { TeamScripts } from "@/components/scripts"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

/** The team's shared script library (MH-576 D5), its built-in host-software catalogue (D1), and
 * run history (D4) — one script, run as root on one or many servers. */
export default function ScriptsPage() {
  const { t } = useI18n()
  const { team } = useTeam()
  useDocumentTitle(`${t("nav.scripts")} — ${team.name}`)

  return (
    <PageBody title={t("nav.scripts")} description={t("scripts.hint")} bleed>
      <TeamScripts />
    </PageBody>
  )
}
