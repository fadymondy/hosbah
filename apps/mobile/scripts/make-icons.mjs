// Renders the Hosbah mark (apps/shared/assets/mark.svg: eight squares on a 64x64 grid, one gold) into
// the app icon, adaptive-icon layers, splash images and the Android notification glyph. No
// dependencies: the mark is axis-aligned squares, so we paint an RGBA buffer and encode the PNG with
// zlib ourselves.
//
//   node scripts/make-icons.mjs      -> assets/brand/*.png, assets/notification-icon.png
import { deflateSync } from "node:zlib";
import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "assets");
const out = join(root, "brand");
mkdirSync(out, { recursive: true });

const BRAND = "#2e6f9e";
const BRAND_DARK = "#7fb0d6";
const GOLD = "#c9a227";
const INK = "#0b1429";
const PAPER = "#f0ebe1";

// [x, y, role] on the 64x64 viewBox; every square is 12.8.
const SQ = 12.8;
const MARK = [
  [12.8, 0, "body"],
  [38.4, 0, "accent"],
  [25.6, 12.8, "body"],
  [12.8, 25.6, "body"],
  [38.4, 25.6, "body"],
  [25.6, 38.4, "body"],
  [12.8, 51.2, "body"],
  [38.4, 51.2, "body"],
];

const hex = (h) => [parseInt(h.slice(1, 3), 16), parseInt(h.slice(3, 5), 16), parseInt(h.slice(5, 7), 16), 255];

const CRC = (() => {
  const t = new Uint32Array(256);
  for (let n = 0; n < 256; n++) {
    let c = n;
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    t[n] = c >>> 0;
  }
  return t;
})();
function crc32(buf) {
  let c = 0xffffffff;
  for (const b of buf) c = CRC[(c ^ b) & 0xff] ^ (c >>> 8);
  return (c ^ 0xffffffff) >>> 0;
}
function chunk(type, data) {
  const len = Buffer.alloc(4);
  len.writeUInt32BE(data.length);
  const td = Buffer.concat([Buffer.from(type), data]);
  const crc = Buffer.alloc(4);
  crc.writeUInt32BE(crc32(td));
  return Buffer.concat([len, td, crc]);
}
function png(size, rgba) {
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(size, 0);
  ihdr.writeUInt32BE(size, 4);
  ihdr[8] = 8;
  ihdr[9] = 6;
  const raw = Buffer.alloc((size * 4 + 1) * size);
  for (let y = 0; y < size; y++) {
    raw[y * (size * 4 + 1)] = 0;
    rgba.copy(raw, y * (size * 4 + 1) + 1, y * size * 4, (y + 1) * size * 4);
  }
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk("IHDR", ihdr),
    chunk("IDAT", deflateSync(raw, { level: 9 })),
    chunk("IEND", Buffer.alloc(0)),
  ]);
}

/** Paints the mark scaled to `scale` of the canvas (centred) on `bg` (null = transparent). */
function render(size, { bg, body, accent, scale = 0.8, mono }) {
  const buf = Buffer.alloc(size * size * 4);
  if (bg) {
    const c = hex(bg);
    for (let i = 0; i < size * size; i++) c.forEach((v, j) => (buf[i * 4 + j] = v));
  }
  const unit = (size * scale) / 64;
  const off = size / 2 - 32 * unit;
  for (const [x, y, role] of MARK) {
    const c = hex(mono ?? (role === "accent" ? accent : body));
    const x0 = Math.round(off + x * unit), x1 = Math.round(off + (x + SQ) * unit);
    const y0 = Math.round(off + y * unit), y1 = Math.round(off + (y + SQ) * unit);
    for (let py = Math.max(0, y0); py < Math.min(size, y1); py++)
      for (let px = Math.max(0, x0); px < Math.min(size, x1); px++) c.forEach((v, j) => (buf[(py * size + px) * 4 + j] = v));
  }
  return png(size, buf);
}

const v = { bg: PAPER, body: BRAND, accent: GOLD };
const w = (file, data) => writeFileSync(join(out, file), data);
w("hosbah-icon.png", render(1024, { ...v, scale: 0.72 }));
// Android adaptive icon: the foreground sits inside the 66% safe zone.
w("hosbah-adaptive-fg.png", render(1024, { ...v, bg: null, scale: 0.52 }));
w("hosbah-adaptive-mono.png", render(1024, { ...v, bg: null, scale: 0.52, mono: "#ffffff" }));
w("hosbah-splash.png", render(512, { ...v, bg: null, scale: 0.9 }));
w("hosbah-splash-dark.png", render(512, { ...v, bg: null, body: BRAND_DARK, scale: 0.9 }));
w("hosbah-favicon.png", render(48, { ...v, scale: 0.8 }));
// Android renders only the alpha channel of a notification icon: white on transparent.
writeFileSync(join(root, "notification-icon.png"), render(96, { bg: null, body: "#ffffff", accent: "#ffffff", scale: 0.8, mono: "#ffffff" }));
console.log("icons written to", root);
