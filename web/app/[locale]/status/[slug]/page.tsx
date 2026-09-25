"use client"

import { useParams } from "next/navigation"
import { useEffect, useState } from "react"
import { CheckCircle2Icon, CircleHelpIcon, CircleXIcon, FrownIcon } from "lucide-react"

import { AuthFrame } from "@/components/auth-frame"
import { useTimeAgo } from "@/components/home/sparkline"
import { Badge } from "@/components/ui/badge"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { apiFetch, ApiError } from "@/lib/api"
import { useDocumentTitle, useI18n } from "@/lib/i18n"

type PublicMonitor = {
  id: string
  name: string
  kind: string
  state: "unknown" | "ok" | "failing"
  last_checked_at: string | null
  open_incident: { opened_at: string; reason: string } | null
}
type PublicStatusPage = {
  title: string
  slug: string
  monitors: PublicMonitor[]
}

/**
 * The public status page (MH-575 H3): no session, no team header — GET /api/public/status/{slug}
 * (internal/api/public.go) answers 404 identically for "no such slug" and "not published", so this
 * page shows the same not-found state either way.
 */
export default function PublicStatusPage() {
  const { slug } = useParams<{ slug: string }>()
  const { t, locale } = useI18n()
  const ago = useTimeAgo(locale)
  const [page, setPage] = useState<PublicStatusPage | null>(null)
  const [notFound, setNotFound] = useState(false)

  useEffect(() => {
    let cancelled = false
    apiFetch<PublicStatusPage>(`/api/public/status/${slug}`)
      .then((p) => {
        if (!cancelled) setPage(p)
      })
      .catch((err) => {
        if (cancelled) return
        if (err instanceof ApiError && err.status === 404) setNotFound(true)
        else setNotFound(true)
      })
    return () => {
      cancelled = true
    }
  }, [slug])

  useDocumentTitle(page ? page.title : t("publicStatus.notFoundTitle"))

  if (notFound) {
    return (
      <AuthFrame eyebrow={t("common.brand")} title={t("publicStatus.notFoundTitle")}>
        <Empty>
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <FrownIcon />
            </EmptyMedia>
            <EmptyTitle>{t("publicStatus.notFoundTitle")}</EmptyTitle>
            <EmptyDescription>{t("publicStatus.notFoundHint")}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      </AuthFrame>
    )
  }

  if (!page) {
    return (
      <AuthFrame eyebrow={t("common.brand")} title={t("common.loading")}>
        <div className="animate-pulse text-sm text-muted-foreground">{t("common.loading")}</div>
      </AuthFrame>
    )
  }

  const allOk = page.monitors.every((m) => m.state !== "failing")

  return (
    <AuthFrame eyebrow={t("common.brand")} title={page.title}>
      <div className="flex flex-col gap-6">
        <Badge variant={allOk ? "default" : "destructive"} className="w-fit gap-1.5">
          {allOk ? <CheckCircle2Icon className="size-3.5" /> : <CircleXIcon className="size-3.5" />}
          {allOk ? t("publicStatus.allOk") : t("publicStatus.someDown")}
        </Badge>
        {page.monitors.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t("statusPages.noMonitors")}</p>
        ) : (
          <ul className="flex flex-col divide-y divide-border border-y">
            {page.monitors.map((m) => (
              <li key={m.id} className="flex flex-col gap-1 py-3">
                <div className="flex items-center justify-between gap-2">
                  <span dir="auto" className="font-medium">
                    {m.name}
                  </span>
                  <Badge variant={m.state === "failing" ? "destructive" : m.state === "ok" ? "default" : "secondary"} className="gap-1.5">
                    {m.state === "failing" ? (
                      <CircleXIcon className="size-3.5" />
                    ) : m.state === "ok" ? (
                      <CheckCircle2Icon className="size-3.5" />
                    ) : (
                      <CircleHelpIcon className="size-3.5" />
                    )}
                    {t(`site.monitors.state.${m.state}`)}
                  </Badge>
                </div>
                {m.open_incident ? (
                  <p dir="auto" className="text-xs text-destructive">
                    {m.open_incident.reason} · {t("publicStatus.since", { when: ago(m.open_incident.opened_at) })}
                  </p>
                ) : m.last_checked_at ? (
                  <p dir="ltr" className="text-start text-xs text-muted-foreground">
                    {ago(m.last_checked_at)}
                  </p>
                ) : null}
              </li>
            ))}
          </ul>
        )}
      </div>
    </AuthFrame>
  )
}
