"use client"

import { useRouter } from "next/navigation"
import { useState, type FormEvent } from "react"

import { AuthFrame } from "@/components/auth-frame"
import { FormError, useErrorText } from "@/components/form-error"
import { PageLoading } from "@/components/page-loading"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { apiFetch, type Team } from "@/lib/api"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import { SessionGate, useSession } from "@/lib/session"

export default function OnboardingPage() {
  const { t } = useI18n()
  useDocumentTitle(t("onboarding.title"))
  return (
    <SessionGate fallback={<PageLoading />}>
      <CreateTeam />
    </SessionGate>
  )
}

/** Also the "Create team" target from the team switcher, so it is not only for first-timers. */
function CreateTeam() {
  const { t, locale } = useI18n()
  const { me } = useSession()
  const router = useRouter()
  const errorText = useErrorText()
  const [name, setName] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const first = me.teams.length === 0

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const team = await apiFetch<Team>("/api/teams", { method: "POST", body: { name: name.trim() } })
      router.replace(`/${locale}/t/${team.slug}`)
    } catch (err) {
      setError(errorText(err))
      setBusy(false)
    }
  }

  return (
    <AuthFrame
      title={first ? t("onboarding.title") : t("onboarding.newTitle")}
      description={first ? t("onboarding.description") : t("onboarding.newDescription")}
      footer={
        <>
          <span dir="ltr">{me.user.email}</span>
          {!first ? (
            <>
              {" · "}
              <button type="button" className="underline underline-offset-4" onClick={() => router.back()}>
                {t("common.cancel")}
              </button>
            </>
          ) : null}
        </>
      }
    >
      <form onSubmit={onSubmit}>
        <FieldGroup>
          <FormError message={error} />
          <Field>
            <FieldLabel htmlFor="team-name">{t("onboarding.teamName")}</FieldLabel>
            <Input
              id="team-name"
              required
              maxLength={80}
              autoFocus
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
            <FieldDescription>{t("onboarding.teamHint")}</FieldDescription>
          </Field>
          <Field>
            <Button type="submit" disabled={busy || !name.trim()}>
              {busy ? t("common.working") : t("onboarding.submit")}
            </Button>
          </Field>
        </FieldGroup>
      </form>
    </AuthFrame>
  )
}
