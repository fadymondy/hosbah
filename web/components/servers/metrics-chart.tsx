"use client"

import { useRef, useState, type MouseEvent, type TouchEvent } from "react"

export type ChartPoint = { at: string; value: number | null }
export type ChartSeries = { key: string; label: string; color: string; points: ChartPoint[] }

/**
 * A responsive SVG line/area chart for a server's metrics history: one or more series on a shared
 * time axis, y-axis gridlines with value labels, a hover/touch tooltip, and start/end time labels.
 * Extends the Sparkline/site-analytics approach (no chart library in this codebase) with real
 * axes and a tooltip. Always drawn left→right regardless of page direction — time runs the same
 * way in both languages — and reads `currentColor`/CSS variables, so it is readable in light and
 * dark. A null value in a series is a gap (the bucket had no samples), not a zero.
 */
export function MetricChart({
  series,
  max,
  unit = "",
  height = 180,
  formatValue,
  formatTime,
  ariaLabel,
}: {
  series: ChartSeries[]
  /** Fixed axis ceiling (e.g. 100 for a percent chart). Auto from the data when omitted. */
  max?: number
  unit?: string
  height?: number
  formatValue?: (v: number) => string
  formatTime: (iso: string) => string
  ariaLabel: string
}) {
  const w = 960
  const pad = { top: 8, bottom: 20, start: 46, end: 6 }
  const innerW = w - pad.start - pad.end
  const innerH = height - pad.top - pad.bottom
  const [hover, setHover] = useState<number | null>(null)
  const svgRef = useRef<SVGSVGElement | null>(null)

  const longest = series.reduce((a, s) => Math.max(a, s.points.length), 0)
  const dataMax = Math.max(0, ...series.flatMap((s) => s.points.map((p) => p.value ?? 0)))
  const top = Math.max(1, max ?? dataMax)
  const fmt = formatValue ?? ((v: number) => `${Math.round(v * 10) / 10}${unit}`)

  const x = (i: number) => pad.start + (longest > 1 ? (i / (longest - 1)) * innerW : innerW / 2)
  const y = (v: number) => pad.top + innerH - (Math.max(0, Math.min(v, top)) / top) * innerH

  function moveTo(clientX: number) {
    const svg = svgRef.current
    if (!svg || longest < 1) return
    const rect = svg.getBoundingClientRect()
    if (rect.width === 0) return
    const px = ((clientX - rect.left) / rect.width) * w
    const i = Math.round(((px - pad.start) / innerW) * (longest - 1))
    setHover(Math.min(longest - 1, Math.max(0, i)))
  }
  const onMouseMove = (e: MouseEvent<SVGSVGElement>) => moveTo(e.clientX)
  const onTouchMove = (e: TouchEvent<SVGSVGElement>) => {
    const t = e.touches[0]
    if (t) moveTo(t.clientX)
  }

  if (longest === 0) return null // the caller renders the empty state

  const ticks = [0, 0.5, 1].map((f) => top * f)
  const first = series.map((s) => s.points[0]).find(Boolean)
  const last = series.map((s) => s.points[s.points.length - 1]).find(Boolean)
  const hoverAt = hover != null ? (series.map((s) => s.points[hover]).find(Boolean)?.at ?? null) : null

  return (
    <div dir="ltr" className="relative">
      <svg
        ref={svgRef}
        viewBox={`0 0 ${w} ${height}`}
        preserveAspectRatio="none"
        className="block h-44 w-full touch-pan-y"
        role="img"
        aria-label={ariaLabel}
        onMouseMove={onMouseMove}
        onMouseLeave={() => setHover(null)}
        onTouchMove={onTouchMove}
        onTouchEnd={() => setHover(null)}
      >
        {ticks.map((t, i) => (
          <g key={i}>
            <line
              x1={pad.start}
              y1={y(t)}
              x2={w - pad.end}
              y2={y(t)}
              stroke="currentColor"
              strokeOpacity={i === 0 ? 0.18 : 0.08}
              strokeDasharray={i === 0 ? undefined : "4 4"}
            />
            <text x={pad.start - 6} y={y(t)} textAnchor="end" dominantBaseline="middle" className="fill-current text-[9px] opacity-60">
              {fmt(t)}
            </text>
          </g>
        ))}
        {series.map((s) => {
          const segments: { x: number; y: number }[][] = []
          let cur: { x: number; y: number }[] = []
          s.points.forEach((p, i) => {
            if (p.value == null) {
              if (cur.length) segments.push(cur)
              cur = []
              return
            }
            cur.push({ x: x(i), y: y(p.value) })
          })
          if (cur.length) segments.push(cur)
          return (
            <g key={s.key}>
              {segments.map((seg, si) => {
                const line = seg.map((p, i) => `${i ? "L" : "M"}${p.x.toFixed(1)},${p.y.toFixed(1)}`).join(" ")
                const area = seg.length > 1 ? `${line} L${seg[seg.length - 1].x},${pad.top + innerH} L${seg[0].x},${pad.top + innerH} Z` : ""
                return (
                  <g key={si}>
                    {series.length === 1 && area ? <path d={area} fill={s.color} opacity={0.14} /> : null}
                    {seg.length > 1 ? <path d={line} fill="none" stroke={s.color} strokeWidth={1.5} vectorEffect="non-scaling-stroke" /> : null}
                    {seg.length === 1 ? <circle cx={seg[0].x} cy={seg[0].y} r={1.5} fill={s.color} /> : null}
                  </g>
                )
              })}
            </g>
          )
        })}
        {hover != null ? <line x1={x(hover)} y1={pad.top} x2={x(hover)} y2={pad.top + innerH} stroke="currentColor" strokeOpacity={0.25} /> : null}
        {hover != null
          ? series.map((s) => {
              const p = s.points[hover]
              if (!p || p.value == null) return null
              return <circle key={s.key} cx={x(hover)} cy={y(p.value)} r={3} fill={s.color} />
            })
          : null}
      </svg>
      {hover != null && hoverAt ? (
        <div
          className="pointer-events-none absolute top-1 z-10 flex max-w-[12rem] flex-col gap-0.5 rounded border border-line bg-grid-card px-2 py-1 text-[11px] shadow-sm"
          style={{
            left: `${Math.min(88, Math.max(0, (x(hover) / w) * 100))}%`,
            transform: (x(hover) / w) * 100 > 50 ? "translateX(-100%)" : undefined,
          }}
        >
          <span className="text-grid-muted">{formatTime(hoverAt)}</span>
          {series.map((s) => {
            const p = s.points[hover]
            return (
              <span key={s.key} className="flex items-center gap-1.5">
                <span aria-hidden className="size-1.5 shrink-0 rounded-full" style={{ background: s.color }} />
                <span className="text-grid-muted">{s.label}</span>
                <span className="ms-auto font-mono tabular-nums text-grid-fg">{p?.value == null ? "—" : fmt(p.value)}</span>
              </span>
            )
          })}
        </div>
      ) : null}
      <div className="mt-1 flex justify-between text-[10px] text-grid-muted">
        <span>{first ? formatTime(first.at) : ""}</span>
        <span>{last ? formatTime(last.at) : ""}</span>
      </div>
    </div>
  )
}
