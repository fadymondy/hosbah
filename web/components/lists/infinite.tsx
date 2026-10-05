"use client"

import { Spinner } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useRef, useState } from "react"

import { useI18n } from "@/lib/i18n"

/** How many rows a list shows at first, and how many more each scroll to its end reveals. */
export const LIST_STEP = 25

/**
 * A growing limit for a long list: show the first `step` items, then `step` more each time the end comes
 * into view. Resets when `resetKey` changes (a new filter or search).
 */
export function useInfiniteLimit(total: number, step = LIST_STEP, resetKey?: unknown) {
  const [limit, setLimit] = useState(step)
  useEffect(() => setLimit(step), [step, resetKey])
  const more = useCallback(() => setLimit((l) => l + step), [step])
  return { limit, hasMore: total > limit, more }
}

/**
 * Sits after the last item and calls `onMore` when it scrolls into view (inside any scroll container),
 * with a button as the keyboard and no-observer fallback.
 */
export function LoadMore({ hasMore, onMore, loading }: { hasMore: boolean; onMore: () => void; loading?: boolean }) {
  const { t } = useI18n()
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const el = ref.current
    if (!el || !hasMore || loading || typeof IntersectionObserver === "undefined") return
    const io = new IntersectionObserver((entries) => entries.some((e) => e.isIntersecting) && onMore(), { rootMargin: "240px" })
    io.observe(el)
    return () => io.disconnect()
  }, [hasMore, loading, onMore])
  if (!hasMore) return null
  return (
    <div ref={ref} className="flex justify-center py-3">
      <button
        type="button"
        onClick={onMore}
        disabled={loading}
        className="inline-flex items-center gap-2 rounded-full px-3 py-1 text-xs text-muted-foreground hover:bg-secondary hover:text-foreground"
      >
        {loading ? <Spinner className="size-3.5" /> : null}
        {t("common.loadMore")}
      </button>
    </div>
  )
}
