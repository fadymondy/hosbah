import type { MarkSpec } from "@/app/grid/grid-marks"
import { CLOUDY_MARK } from "@/lib/brand"

/**
 * A cube-lattice mark drawn from its cells, normalised to the cells' OWN bounding box (Cloudy's
 * cells start at column 1 — measuring from 0 would push it off-centre). Same approach as
 * fadymondy.com-v2's web/components/marks/cube-mark.tsx.
 */
export function CubeMark({
  mark = CLOUDY_MARK,
  size = 24,
  onDark = false,
  className,
}: {
  mark?: MarkSpec
  size?: number
  onDark?: boolean
  className?: string
}) {
  const cols = mark.cells.map(([c]) => c)
  const rows = mark.cells.map(([, r]) => r)
  const minCol = Math.min(...cols)
  const minRow = Math.min(...rows)
  const width = Math.max(...cols) - minCol + 1
  const height = Math.max(...rows) - minRow + 1
  const unit = 100 / Math.max(width, height)
  const body = onDark && mark.bodyOnDark ? mark.bodyOnDark : mark.body
  const accent = new Set(mark.accentCells.map(([c, r]) => `${c},${r}`))

  return (
    <svg
      viewBox={`0 0 ${width * unit} ${height * unit}`}
      width={(size * width) / Math.max(width, height)}
      height={(size * height) / Math.max(width, height)}
      className={className}
      role="img"
      aria-label={mark.name}
    >
      {mark.cells.map(([c, r]) => (
        <rect
          key={`${c},${r}`}
          x={(c - minCol) * unit}
          y={(r - minRow) * unit}
          width={unit}
          height={unit}
          fill={accent.has(`${c},${r}`) ? mark.accent : body}
        />
      ))}
    </svg>
  )
}

/** The mark on the page's own ground: the dark-ground body is swapped in by CSS (theme is not known on the server). */
export function ThemedCubeMark({ size, className = "" }: { size?: number; className?: string }) {
  return (
    <>
      <CubeMark size={size} className={`dark:hidden ${className}`} />
      <CubeMark size={size} onDark className={`hidden dark:block ${className}`} />
    </>
  )
}
