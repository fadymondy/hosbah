"use client"

import { Alert } from "@fadymondy/nasaq/web"
import { AlertCircleIcon } from "lucide-react"
import { useCallback } from "react"

import { ApiError } from "@/lib/api"
import { useI18n } from "@/lib/i18n"

/** Maps an API error to a translated sentence; unknown server text is kept, LTR. */
export function useErrorText() {
  const { t } = useI18n()
  // Stable per locale: callers list it in effect/callback dependencies, and a fresh function on
  // every render would refetch in a loop.
  return useCallback((err: unknown): string => {
    if (err instanceof ApiError) {
      const key = `errors.api.${err.code.replace(/\s+/g, "_")}`
      const known = t(key)
      if (known !== key) return known
      if (err.status === 403) return t("errors.forbidden")
      if (err.status === 404) return t("errors.notFound")
      if (err.status === 429) return t("errors.rateLimited")
      if (err.status >= 500) return t("errors.server")
      return `${t("errors.generic")} (${err.message})`
    }
    return t("errors.network")
  }, [t])
}

export function FormError({ message }: { message: string | null }) {
  if (!message) return null
  return (
    <Alert tone="danger" role="alert">
{message}
</Alert>
  )
}
