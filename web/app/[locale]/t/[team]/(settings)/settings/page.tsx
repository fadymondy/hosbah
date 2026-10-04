"use client"

import { Button, Input, toast } from "@fadymondy/nasaq/web"
import { useRouter } from "next/navigation"
import { useState, type FormEvent } from "react"

import { PageBody } from "@/components/app-shell"
import { useErrorText } from "@/components/form-error"
import { GitHubAppSection } from "@/components/github-app"
import { NotificationChannelsSection } from "@/components/notification-channels"
import { LogDrainsCard } from "@/components/log-drains"
import { ErrorBand, SectionTitle, SettingsRow } from "@/components/states"
import { StatusPagesSection } from "@/components/status-pages"
import { TeamQuotasCard } from "@/components/team-quotas"


import { apiFetch, type Team } from "@/lib/api"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import { useSession } from "@/lib/session"
import { useTeam } from "@/lib/team"

export default function SettingsPage() {
  const { t, locale } = useI18n()
  const { team, manage, refresh } = useTeam()
  const session = useSession()
  const router = useRouter()
  useDocumentTitle(`${t("nav.settings")} — ${team.name}`)
  const errorText = useErrorText()
  const [name, setName] = useState(team.name)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const updated = await apiFetch<Team>(`/api/teams/${team.slug}`, { method: "PATCH", body: { name: name.trim() } })
      toast.success(t("settings.saved"))
      await session.refresh()
      if (updated?.slug && updated.slug !== team.slug) router.replace(`/${locale}/t/${updated.slug}/settings`)
      else await refresh()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <PageBody title={t("nav.settings")} description={manage ? t("settings.description") : t("common.readOnly")} bleed>
      <SectionTitle title={t("settings.general")} hint={t("settings.renameHint")} />
      <form onSubmit={submit}>
        <ErrorBand message={error} />
        <SettingsRow label={t("onboarding.teamName")} htmlFor="team-name" hint={!manage ? t("settings.viewerNote") : undefined}>
          <Input
            id="team-name"
            required
            maxLength={80}
            value={name}
            readOnly={!manage}
            disabled={!manage}
            onChange={(e) => setName(e.target.value)}
          />
        </SettingsRow>
        <SettingsRow label={t("overview.slug")} htmlFor="team-slug">
          <Input id="team-slug" dir="ltr" className="font-mono" value={team.slug} readOnly disabled />
        </SettingsRow>
        {manage ? (
          <div className="flex justify-end border-b border-border px-6 py-4">
            <Button variant="primary" type="submit" disabled={busy || !name.trim() || name.trim() === team.name}>
              {busy ? t("common.working") : t("common.save")}
            </Button>
          </div>
        ) : null}
      </form>
      <TeamQuotasCard team={team.slug} />
      <div id="notifications" className="scroll-mt-16">
        <NotificationChannelsSection team={team.slug} manage={manage} />
      </div>
      <StatusPagesSection team={team.slug} manage={manage} />
      <LogDrainsCard team={team.slug} manage={manage} />
      <GitHubAppSection team={team.slug} />
    </PageBody>
  )
}
