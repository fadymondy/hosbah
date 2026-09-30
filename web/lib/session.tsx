"use client"
import { Button } from "@fadymondy/nasaq/web"

import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from "react"
import { usePathname, useRouter } from "next/navigation"

import { apiFetch, ApiError, type Me } from "@/lib/api"
import { useI18n } from "@/lib/i18n"

type Session = { me: Me; refresh: () => Promise<void> }

const Ctx = createContext<Session | null>(null)

/**
 * Loads /api/me once for every signed-in page. The API is the security boundary: a 401 here
 * only decides where to SEND the visitor (login, with ?next= back to this page).
 */
export function SessionGate({ children, fallback }: { children: ReactNode; fallback?: ReactNode }) {
  const { locale } = useI18n()
  const router = useRouter()
  const pathname = usePathname()
  const [me, setMe] = useState<Me | null>(null)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      setMe(await apiFetch<Me>("/api/me"))
      setError(null)
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) {
        const search = typeof window === "undefined" ? "" : window.location.search
        router.replace(`/${locale}/login?next=${encodeURIComponent(pathname + search)}`)
        return
      }
      setError(e instanceof Error ? e.message : String(e))
    }
  }, [locale, pathname, router])

  useEffect(() => {
    void load()
    // Only on mount: pages call refresh() after changing teams.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  if (error) return <ApiUnavailable message={error} onRetry={load} />
  if (!me) return <>{fallback ?? null}</>
  return <Ctx.Provider value={{ me, refresh: load }}>{children}</Ctx.Provider>
}

export function useSession(): Session {
  const ctx = useContext(Ctx)
  if (!ctx) throw new Error("useSession outside SessionGate")
  return ctx
}

function ApiUnavailable({ message, onRetry }: { message: string; onRetry: () => void }) {
  const { t } = useI18n()
  return (
    <div className="flex min-h-dvh flex-col items-center justify-center gap-3 p-6 text-center">
      <p className="text-lg font-medium">{t("errors.apiUnavailable")}</p>
      <p className="text-sm text-muted-foreground" dir="ltr">
        {message}
      </p>
      <Button type="button" variant="primary" onClick={onRetry}>
        {t("common.retry")}
      </Button>
    </div>
  )
}
