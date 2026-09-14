"use client"

import { AlertCircleIcon } from "lucide-react"

import { Alert, AlertDescription } from "@/components/ui/alert"
import { ApiError } from "@/lib/api"
import { useI18n } from "@/lib/i18n"

/** Maps an API error to a translated sentence; unknown server text is kept, LTR. */
export function useErrorText() {
  const { t } = useI18n()
  return (err: unknown): string => {
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
  }
}

export function FormError({ message }: { message: string | null }) {
  if (!message) return null
  return (
    <Alert variant="destructive" role="alert">
      <AlertCircleIcon />
      <AlertDescription>{message}</AlertDescription>
    </Alert>
  )
}
