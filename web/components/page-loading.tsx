"use client"

import { BrandLoader } from "@/components/brand-loader"
import { useI18n } from "@/lib/i18n"

/** Full-screen loading: the brand mark; the label sits under it. */
export function PageLoading() {
  const { t } = useI18n()
  return (
    <div className="flex min-h-dvh flex-col items-center justify-center gap-5" role="status" aria-live="polite">
      <BrandLoader size={72} />
      <span className="eyebrow">{t("common.brand")}</span>
      <span className="sr-only">{t("common.loading")}</span>
    </div>
  )
}
