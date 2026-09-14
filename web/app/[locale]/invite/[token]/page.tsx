"use client"

import Link from "next/link"
import { useParams, useRouter } from "next/navigation"
import { useEffect, useState } from "react"

import { AuthFrame } from "@/components/auth-frame"
import { FormError, useErrorText } from "@/components/form-error"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { apiFetch, ApiError, type Role, type Team } from "@/lib/api"
import { useDocumentTitle, useI18n } from "@/lib/i18n"

type Invite = { team_name: string; email: string; role: Role; status: string }

export default function InvitePage() {
  const { t, locale } = useI18n()
  useDocumentTitle(t("invite.title"))
  const { token } = useParams<{ token: string }>()
  const router = useRouter()
  const errorText = useErrorText()

  const [invite, setInvite] = useState<Invite | null>(null)
  const [signedOut, setSignedOut] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    apiFetch<Invite>(`/api/invitations/${encodeURIComponent(token)}`)
      .then(setInvite)
      .catch((err) => {
        if (err instanceof ApiError && err.status === 401) setSignedOut(true)
        else if (err instanceof ApiError && err.status === 404) setError(t("invite.invalid"))
        else setError(errorText(err))
      })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [token])

  async function accept() {
    setBusy(true)
    setError(null)
    try {
      const res = await apiFetch<{ team: Team }>(`/api/invitations/${encodeURIComponent(token)}/accept`, {
        method: "POST",
      })
      router.replace(`/${locale}/t/${res.team.slug}`)
    } catch (err) {
      setError(errorText(err))
      setBusy(false)
    }
  }

  const back = encodeURIComponent(`/${locale}/invite/${token}`)

  if (signedOut) {
    return (
      <AuthFrame title={t("invite.title")} description={t("invite.signInFirst")}>
        <div className="flex flex-col gap-2">
          <Button nativeButton={false} render={<Link href={`/${locale}/login?next=${back}`} />}>
            {t("auth.login.title")}
          </Button>
          <Button variant="outline" nativeButton={false} render={<Link href={`/${locale}/register?next=${back}`} />}>
            {t("auth.register.title")}
          </Button>
        </div>
      </AuthFrame>
    )
  }

  const pending = invite?.status === "pending"

  return (
    <AuthFrame title={t("invite.title")} description={invite ? t("invite.description") : undefined}>
      <div className="flex flex-col gap-4">
        <FormError message={error} />
        {!invite && !error ? (
          <div className="flex flex-col gap-2">
            <Skeleton className="h-5 w-2/3" />
            <Skeleton className="h-4 w-1/2" />
          </div>
        ) : null}
        {invite ? (
          <>
            <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm">
              <dt className="text-muted-foreground">{t("invite.team")}</dt>
              <dd className="font-medium">{invite.team_name}</dd>
              <dt className="text-muted-foreground">{t("invite.for")}</dt>
              <dd dir="ltr" className="text-start">
                {invite.email}
              </dd>
              <dt className="text-muted-foreground">{t("invite.role")}</dt>
              <dd>
                <Badge variant="secondary">{t(`roles.${invite.role}`)}</Badge>
              </dd>
            </dl>
            {pending ? (
              <Button onClick={accept} disabled={busy}>
                {busy ? t("common.working") : t("invite.accept")}
              </Button>
            ) : (
              <p className="text-sm text-muted-foreground">
                {t("invite.notPending", { status: t(`invitations.status.${invite.status}`, undefined, invite.status) })}
              </p>
            )}
          </>
        ) : null}
      </div>
    </AuthFrame>
  )
}
