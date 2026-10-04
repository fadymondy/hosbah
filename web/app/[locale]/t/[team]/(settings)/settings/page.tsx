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

/** The sections of General, in page order; each is an anchor the jump row links to. */
const SECTIONS = ["general", "quotas", "notifications", "status-pages", "log-drains", "github"] as const

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
      <nav aria-label={t("settings2.onThisPage")} className="sticky top-0 z-10 flex gap-1 overflow-x-auto border-b border-line bg-grid-bg/95 px-6 py-2 backdrop-blur">
        {SECTIONS.map((id) => (
          <a key={id} href={`#${id}`} className="shrink-0 rounded-full px-3 py-1 text-xs text-grid-muted hover:bg-grid-soft hover:text-grid-fg">
            {t(`settings2.sections.${id}`)}
          </a>
        ))}
      </nav>
      <div id="general" className="scroll-mt-14">
        <SectionTitle title={t("settings.general")} hint={t("settings.renameHint")} />
      </div>
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
      <div id="quotas" className="scroll-mt-14">
        <TeamQuotasCard team={team.slug} />
      </div>
      <div id="notifications" className="scroll-mt-14">
        <NotificationChannelsSection team={team.slug} manage={manage} />
      </div>
      <div id="status-pages" className="scroll-mt-14">
        <StatusPagesSection team={team.slug} manage={manage} />
      </div>
      <div id="log-drains" className="scroll-mt-14">
        <LogDrainsCard team={team.slug} manage={manage} />
      </div>
      <div id="github" className="scroll-mt-14">
        <GitHubAppSection team={team.slug} />
      </div>
    </PageBody>
  )
}
