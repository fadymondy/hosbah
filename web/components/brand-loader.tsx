import type { MarkSpec } from "@/app/grid/grid-marks"
import { CLOUDY_MARK } from "@/lib/brand"

/**
 * The cube mark as a loader: its cells stack in from the bottom one at a time, the accent cell
 * lands last, then the lattice clears in the same order and builds again. Pure CSS (brand-loader
 * in globals.css), so it renders on the server and animates before hydration. Reduced motion
 * gets the whole mark with a slow opacity pulse instead.
 */
export function BrandLoader({
  mark = CLOUDY_MARK,
  size = 56,
  className = "",
}: {
  mark?: MarkSpec
  size?: number
  className?: string
}) {
  const cols = mark.cells.map(([c]) => c)
  const rows = mark.cells.map(([, r]) => r)
  const minCol = Math.min(...cols)
  const minRow = Math.min(...rows)
  const width = Math.max(...cols) - minCol + 1
  const height = Math.max(...rows) - minRow + 1
  const unit = 100 / Math.max(width, height)
  const accent = new Set(mark.accentCells.map(([c, r]) => `${c},${r}`))
  // Bottom row first, left to right; accent cells always last so the gold one "closes" the build.
  const order = [...mark.cells].sort((a, b) => {
    const aa = accent.has(`${a[0]},${a[1]}`) ? 1 : 0
    const bb = accent.has(`${b[0]},${b[1]}`) ? 1 : 0
    return aa - bb || b[1] - a[1] || a[0] - b[0]
  })
  const gap = unit * 0.08

  return (
    <svg
      viewBox={`0 0 ${width * unit} ${height * unit}`}
      width={(size * width) / Math.max(width, height)}
      height={(size * height) / Math.max(width, height)}
      className={`brand-loader ${className}`}
      aria-hidden
    >
      {order.map(([c, r], i) => {
        const isAccent = accent.has(`${c},${r}`)
        return (
          <rect
            key={`${c},${r}`}
            x={(c - minCol) * unit + gap / 2}
            y={(r - minRow) * unit + gap / 2}
            width={unit - gap}
            height={unit - gap}
            className={isAccent ? "brand-loader-accent" : "brand-loader-cell"}
            style={{ animationDelay: `${i * 90}ms`, ...(isAccent ? { fill: mark.accent } : {}) }}
          />
        )
      })}
    </svg>
  )
}
