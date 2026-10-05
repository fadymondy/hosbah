"use client"

import { PageBody } from "@/components/app-shell"
import { NotificationChannelsSection } from "@/components/notification-channels"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

export default function NotificationsPage() {
  const { t } = useI18n()
  const { team, manage } = useTeam()
  useDocumentTitle(`${t("nav.notifications")} — ${team.name}`)
  return (
    <PageBody title={t("nav.notifications")} description={t("settings.alertsHint")} bleed divider={false}>
      <NotificationChannelsSection team={team.slug} manage={manage} />
    </PageBody>
  )
}
