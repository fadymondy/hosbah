import type { MarkSpec } from "@/app/grid/grid-marks"

/**
 * Hosbah's cube mark. These cells move to the GENERATED app/grid/grid-marks.ts
 * (import it from there and delete this constant) once fadymondy.com-v2's
 * web/lib/brand/marks.ts carries a `hosbah` entry and the grid is re-synced.
 */
export const HOSBAH_MARK: MarkSpec = {
  name: "Hosbah",
  cells: [
    [1, 0],
    [3, 0],
    [2, 1],
    [1, 2],
    [3, 2],
    [2, 3],
    [1, 4],
    [3, 4],
  ],
  accentCells: [[3, 0]],
  body: "#2E6F9E",
  bodyOnDark: "#7FB0D6",
  accent: "#C9A227",
}

/** The name before the rename; components still import it. */
export const CLOUDY_MARK = HOSBAH_MARK
