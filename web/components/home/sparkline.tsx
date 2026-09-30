"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"

import { useEventStream } from "@/lib/servers"

/**
 * A small area chart for a server card: values 0..max drawn left→right as the samples came in
 * (left-to-right even in RTL — time runs the same way in both languages, as in every chart).
 * Pure SVG, no library; the last value is marked. Empty or single-point input draws nothing.
 */
export function Sparkline({
  values,
  max = 100,
  label,
  tone = "brand",
  height = 40,
}: {
  values: number[]
  max?: number
  label: string
  tone?: "brand" | "gold" | "ok"
  height?: number
}) {
  const w = 200
  const h = height
  const pts = useMemo(() => {
    if (values.length < 2) return null
    const top = Math.max(max, ...values, 1)
    const step = w / (values.length - 1)
    return values.map((v, i) => [i * step, h - 2 - (Math.max(0, v) / top) * (h - 4)] as const)
  }, [values, max, h])
  if (!pts) return null
  const line = pts.map(([x, y], i) => `${i ? "L" : "M"}${x.toFixed(1)},${y.toFixed(1)}`).join(" ")
  const area = `${line} L${w},${h} L0,${h} Z`
  const color = tone === "gold" ? "var(--nq-accent)" : tone === "ok" ? "var(--nq-success)" : "var(--nq-brand)"
  const [lx, ly] = pts[pts.length - 1]
  return (
    <svg viewBox={`0 0 ${w} ${h}`} preserveAspectRatio="none" className="block h-10 w-full" style={{ direction: "ltr" }} role="img" aria-label={label}>
      <path d={area} fill={color} opacity={0.14} />
      <path d={line} fill="none" stroke={color} strokeWidth={1.5} vectorEffect="non-scaling-stroke" />
      <circle cx={lx} cy={ly} r={2.5} fill={color} vectorEffect="non-scaling-stroke" />
    </svg>
  )
}

/** "3 minutes ago" / "منذ 3 دقائق", from an ISO time. */
export function useTimeAgo(locale: string) {
  return useMemo(() => {
    const rtf = new Intl.RelativeTimeFormat(locale === "ar" ? "ar" : "en", { numeric: "auto" })
    const units: [Intl.RelativeTimeFormatUnit, number][] = [
      ["year", 31536000],
      ["month", 2592000],
      ["week", 604800],
      ["day", 86400],
      ["hour", 3600],
      ["minute", 60],
    ]
    return (iso: string | null | undefined) => {
      if (!iso) return "—"
      const s = (new Date(iso).getTime() - Date.now()) / 1000
      for (const [unit, secs] of units) if (Math.abs(s) >= secs) return rtf.format(Math.round(s / secs), unit)
      return rtf.format(Math.round(s), "second")
    }
  }, [locale])
}

/** A live reading as the server stream sends it (the "server" event's metrics, or a "metrics" event). */
type LivePoint = { at?: string; cpu?: number; cpu_percent?: number; mem_used?: number; mem_used_bytes?: number; mem_total?: number; mem_total_bytes?: number; gpus?: { util?: number }[] }

const WINDOW = 60

/**
 * Rolling CPU / memory / GPU series for one server: seeded with the recent history, then every
 * reading its live stream sends is appended (the oldest drop off), so the chart moves in real time.
 */
export function useLiveSeries(streamUrl: string | null, seed?: { cpu?: number[]; mem?: number[]; gpu?: number[] | null } | null) {
  const [series, setSeries] = useState<{ cpu: number[]; mem: number[]; gpu: number[] }>({ cpu: [], mem: [], gpu: [] })
  const last = useRef<string | null>(null)

  // (Re)seed when history arrives, keeping any live points already appended after it.
  useEffect(() => {
    if (!seed) return
    setSeries((s) => ({
      cpu: [...(seed.cpu ?? []), ...s.cpu].slice(-WINDOW),
      mem: [...(seed.mem ?? []), ...s.mem].slice(-WINDOW),
      gpu: [...(seed.gpu ?? []), ...s.gpu].slice(-WINDOW),
    }))
    // Only when the seed itself changes identity.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [seed])

  const push = useCallback((p: LivePoint | null | undefined) => {
    if (!p) return
    if (p.at && p.at === last.current) return
    last.current = p.at ?? null
    const cpu = p.cpu ?? p.cpu_percent
    const used = p.mem_used ?? p.mem_used_bytes
    const total = p.mem_total ?? p.mem_total_bytes
    const gpu = p.gpus?.[0]?.util
    setSeries((s) => ({
      cpu: cpu == null ? s.cpu : [...s.cpu, cpu].slice(-WINDOW),
      mem: used == null || !total ? s.mem : [...s.mem, (used / total) * 100].slice(-WINDOW),
      gpu: gpu == null ? s.gpu : [...s.gpu, gpu].slice(-WINDOW),
    }))
  }, [])

  useEventStream(streamUrl, {
    server: (d) => push((d as { metrics?: LivePoint }).metrics),
    metrics: (d) => push(d as LivePoint),
    operations: () => {},
    gone: () => {},
  })
  return series
}
