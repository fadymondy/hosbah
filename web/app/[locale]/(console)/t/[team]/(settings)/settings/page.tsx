"use client"

import { Alert, Button, Input, SettingRow, SettingsSection, SettingsSections, toast } from "@fadymondy/nasaq/web"
import { useRouter } from "next/navigation"
import { useState, type FormEvent } from "react"
import { GaugeIcon, GithubIcon, Settings2Icon } from "lucide-react"

import { useErrorText } from "@/components/form-error"
import { GitHubAppSection } from "@/components/github-app"
import { CardPieces } from "@/components/states"
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

  const general = (
    <form onSubmit={submit}>
      <SettingsSection
        title={t("settings.general")}
        description={t("settings.renameHint")}
        footer={!manage ? t("settings.viewerNote") : undefined}
        actions={
          manage ? (
            <Button variant="primary" type="submit" disabled={busy || !name.trim() || name.trim() === team.name}>
              {busy ? t("common.working") : t("common.save")}
            </Button>
          ) : undefined
        }
      >
        {error ? <Alert tone="danger">{error}</Alert> : null}
        <SettingRow id="team-name" label={t("onboarding.teamName")}>
          <Input
            id="team-name-input"
            aria-label={t("onboarding.teamName")}
            className="w-72 max-w-full"
            required
            maxLength={80}
            value={name}
            readOnly={!manage}
            disabled={!manage}
            onChange={(e) => setName(e.target.value)}
          />
        </SettingRow>
        <SettingRow id="team-slug" label={t("overview.slug")}>
          <Input aria-label={t("overview.slug")} dir="ltr" className="w-72 max-w-full font-mono" value={team.slug} readOnly disabled />
        </SettingRow>
      </SettingsSection>
    </form>
  )

  return (
    <div className="px-6 py-6">
      <SettingsSections
        className="max-w-none"
        title={t("nav.settings")}
        description={manage ? t("settings.description") : t("common.readOnly")}
        groups={[
          {
            id: "team",
            label: team.name,
            pages: [
              {
                id: "general",
                label: t("nav.general"),
                icon: <Settings2Icon />,
                entries: [
                  { id: "team-name", label: t("onboarding.teamName") },
                  { id: "team-slug", label: t("overview.slug") },
                ],
                content: general,
              },
              {
                id: "quotas",
                label: t("settings.quotasTitle"),
                icon: <GaugeIcon />,
                content: (
                  <CardPieces bare>
                    <TeamQuotasCard team={team.slug} />
                  </CardPieces>
                ),
              },
              {
                id: "github",
                label: "GitHub",
                icon: <GithubIcon />,
                content: (
                  <CardPieces bare>
                    <GitHubAppSection team={team.slug} />
                  </CardPieces>
                ),
              },
            ],
          },
        ]}
      />
    </div>
  )
}
