"use client"

import { Skeleton } from "@/components/ui/skeleton"
import { useI18n } from "@/lib/i18n"

export function PageLoading() {
  const { t } = useI18n()
  return (
    <div className="flex min-h-dvh items-center justify-center" role="status" aria-live="polite">
      <div className="flex w-64 flex-col gap-2">
        <Skeleton className="h-4 w-full" />
        <Skeleton className="h-4 w-2/3" />
        <span className="sr-only">{t("common.loading")}</span>
      </div>
    </div>
  )
}
