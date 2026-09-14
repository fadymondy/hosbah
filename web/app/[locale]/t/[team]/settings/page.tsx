"use client"

import { useRouter } from "next/navigation"
import { useState, type FormEvent } from "react"
import { toast } from "sonner"

import { PageBody } from "@/components/app-shell"
import { FormError, useErrorText } from "@/components/form-error"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
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
    <PageBody title={t("nav.settings")} description={manage ? t("settings.description") : t("common.readOnly")}>
      <Card className="max-w-xl">
        <CardHeader>
          <CardTitle>{t("settings.general")}</CardTitle>
          <CardDescription>{t("settings.renameHint")}</CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={submit}>
            <FieldGroup>
              <FormError message={error} />
              <Field>
                <FieldLabel htmlFor="team-name">{t("onboarding.teamName")}</FieldLabel>
                <Input
                  id="team-name"
                  required
                  maxLength={80}
                  value={name}
                  readOnly={!manage}
                  disabled={!manage}
                  onChange={(e) => setName(e.target.value)}
                />
                {!manage ? <FieldDescription>{t("settings.viewerNote")}</FieldDescription> : null}
              </Field>
              <Field>
                <FieldLabel htmlFor="team-slug">{t("overview.slug")}</FieldLabel>
                <Input id="team-slug" dir="ltr" className="font-mono" value={team.slug} readOnly disabled />
              </Field>
              {manage ? (
                <Field orientation="horizontal">
                  <Button type="submit" disabled={busy || !name.trim() || name.trim() === team.name}>
                    {busy ? t("common.working") : t("common.save")}
                  </Button>
                </Field>
              ) : null}
            </FieldGroup>
          </form>
        </CardContent>
      </Card>
    </PageBody>
  )
}
