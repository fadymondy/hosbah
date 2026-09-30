"use client"

import { Button } from "@fadymondy/nasaq/web"
import Link from "next/link"

import { AuthFrame } from "@/components/auth-frame"

import { useDocumentTitle, useI18n } from "@/lib/i18n"

export default function NotFound() {
  const { t, locale } = useI18n()
  useDocumentTitle(t("auth.notFoundTitle"))
  return (
    <AuthFrame eyebrow="404" title={t("auth.notFoundTitle")} description={t("auth.notFoundHint")}>
      <Button variant="primary" nativeButton={false} render={<Link href={`/${locale}`} />}>
        {t("common.home")}
      </Button>
    </AuthFrame>
  )
}
