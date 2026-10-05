"use client"

import { useSearchParams } from "next/navigation"
import { Suspense } from "react"

import { AuthFrame } from "@/components/auth-frame"

import { useDocumentTitle, useI18n } from "@/lib/i18n"

/**
 * Where a used or expired panel link lands (MH-1153), and where a customer from one ends up after
 * signing out (?out=1). The link worked once; only the service that sent it can mint another.
 */
export default function LinkExpiredPage() {
  return (
    <Suspense>
      <LinkExpired />
    </Suspense>
  )
}

function LinkExpired() {
  const { t } = useI18n()
  const out = useSearchParams()?.get("out") === "1"
  const title = t(out ? "panel.linkExpired.signedOutTitle" : "panel.linkExpired.title")
  useDocumentTitle(title)
  return (
    <AuthFrame title={title} description={t(out ? "panel.linkExpired.signedOutHint" : "panel.linkExpired.hint")}>
      {null}
    </AuthFrame>
  )
}
