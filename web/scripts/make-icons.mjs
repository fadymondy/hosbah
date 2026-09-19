// Generates Hosbah's favicon and PWA icons from the cube mark's cells (the same cells as
// lib/brand.ts). Run: node scripts/make-icons.mjs — outputs are committed, so this only reruns
// when the mark changes.
import fs from "node:fs"
import path from "node:path"
import sharp from "sharp"

const CELLS = [
  [1, 0],
  [3, 0],
  [2, 1],
  [1, 2],
  [3, 2],
  [2, 3],
  [1, 4],
  [3, 4],
]
const ACCENT = new Set(["3,0"])
const BODY_LIGHT = "#2E6F9E"
const BODY_DARK = "#7FB0D6"
const GOLD = "#C9A227"
const NAVY = "#0B1429" // the grid's dark ground

const minC = Math.min(...CELLS.map(([c]) => c))
const W = Math.max(...CELLS.map(([c]) => c)) - minC + 1 // 3
const H = Math.max(...CELLS.map(([, r]) => r)) + 1 // 5

/** The mark as SVG rects inside a box of `size`, occupying `fill` of its height, centred. */
function rects(size, fill, body, cls = "") {
  const unit = (size * fill) / H
  const ox = (size - W * unit) / 2
  const oy = (size - H * unit) / 2
  return CELLS.map(([c, r]) => {
    const accent = ACCENT.has(`${c},${r}`)
    const x = (ox + (c - minC) * unit).toFixed(3)
    const y = (oy + r * unit).toFixed(3)
    return `<rect x="${x}" y="${y}" width="${unit.toFixed(3)}" height="${unit.toFixed(3)}" fill="${accent ? GOLD : body}"${accent || !cls ? "" : ` class="${cls}"`}/>`
  }).join("")
}

const app = path.resolve("app")
const pub = path.resolve("public")
fs.mkdirSync(path.join(pub, "icons"), { recursive: true })

// 1. The favicon: transparent, and the body colour follows the browser's light/dark scheme.
const favicon = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"><style>.b{fill:${BODY_LIGHT}}@media (prefers-color-scheme: dark){.b{fill:${BODY_DARK}}}</style>${rects(64, 1, BODY_LIGHT, "b")}</svg>\n`
fs.writeFileSync(path.join(app, "icon.svg"), favicon)

// 2. Square icons on the navy ground (the mark in its dark-ground colours).
function square(size, fill) {
  return Buffer.from(
    `<svg xmlns="http://www.w3.org/2000/svg" width="${size}" height="${size}"><rect width="100%" height="100%" fill="${NAVY}"/>${rects(size, fill, BODY_DARK)}</svg>`,
  )
}
const png = (size, fill) => sharp(square(size, fill)).png({ compressionLevel: 9 }).toBuffer()

fs.writeFileSync(path.join(app, "apple-icon.png"), await png(180, 0.62))
fs.writeFileSync(path.join(pub, "icons", "icon-192.png"), await png(192, 0.62))
fs.writeFileSync(path.join(pub, "icons", "icon-512.png"), await png(512, 0.62))
// Maskable: launchers crop to a circle/squircle; keep the mark inside the 80% safe zone.
fs.writeFileSync(path.join(pub, "icons", "icon-maskable-512.png"), await png(512, 0.46))

// 3. favicon.ico for clients that ask for it by name: one 32×32 PNG inside an ICO container.
const ico32 = await sharp(
  Buffer.from(`<svg xmlns="http://www.w3.org/2000/svg" width="32" height="32">${rects(32, 1, BODY_LIGHT)}</svg>`),
)
  .png()
  .toBuffer()
const header = Buffer.alloc(6)
header.writeUInt16LE(0, 0) // reserved
header.writeUInt16LE(1, 2) // type: icon
header.writeUInt16LE(1, 4) // one image
const entry = Buffer.alloc(16)
entry.writeUInt8(32, 0) // width
entry.writeUInt8(32, 1) // height
entry.writeUInt8(0, 2) // palette
entry.writeUInt8(0, 3) // reserved
entry.writeUInt16LE(1, 4) // planes
entry.writeUInt16LE(32, 6) // bits per pixel
entry.writeUInt32LE(ico32.length, 8)
entry.writeUInt32LE(6 + 16, 12) // offset
fs.writeFileSync(path.join(app, "favicon.ico"), Buffer.concat([header, entry, ico32]))

console.log("icons written")
