"use client"

import { useRouter } from "next/navigation"
import { useEffect } from "react"

import { PageLoading } from "@/components/page-loading"
import { useDocumentTitle, useI18n } from "@/lib/i18n"
import { SessionGate, useSession } from "@/lib/session"

/** Home: into your first team, or onboarding when you have none. Signed-out visitors go to /login. */
export default function Home() {
  const { t } = useI18n()
  useDocumentTitle(t("common.loading"))
  return (
    <SessionGate fallback={<PageLoading />}>
      <Redirect />
    </SessionGate>
  )
}

function Redirect() {
  const { locale } = useI18n()
  const { me } = useSession()
  const router = useRouter()
  useEffect(() => {
    const first = me.teams[0]
    router.replace(first ? `/${locale}/t/${first.slug}` : `/${locale}/onboarding`)
  }, [me, locale, router])
  return <PageLoading />
}
