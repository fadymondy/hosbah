"use client"

import { BrandLoader } from "@/components/brand-loader"
import { useI18n } from "@/lib/i18n"

/** Full-screen loading: the brand mark building itself; the label fades in only if it takes a while. */
export function PageLoading() {
  const { t } = useI18n()
  return (
    <div className="flex min-h-dvh flex-col items-center justify-center gap-5" role="status" aria-live="polite">
      <BrandLoader size={96} />
      <span className="brand-loader-label text-xs tracking-[0.2em] text-muted-foreground uppercase">
        {t("common.brand")}
      </span>
      <span className="sr-only">{t("common.loading")}</span>
    </div>
  )
}
