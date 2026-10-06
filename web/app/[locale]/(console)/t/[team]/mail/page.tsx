"use client"

import { PageBody } from "@/components/app-shell"
import { TeamMail } from "@/components/mail"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

/** The team's mail servers (MH-577 I1-I3): install Stalwart on one of the team's own servers, add
 * domains, manage mailboxes and aliases, and check/publish each domain's DNS records. */
export default function MailPage() {
  const { t } = useI18n()
  const { team } = useTeam()
  useDocumentTitle(`${t("nav.mail")} — ${team.name}`)

  return (
    <PageBody title={t("nav.mail")} description={t("mail.hint")} bleed>
      <TeamMail />
    </PageBody>
  )
}
