// A tiny RGBA -> PNG encoder (node:zlib only) and the tray status icon drawn on top of it. Pure: the
// tray, the icon generator script and the tests all share it, and nothing here touches Electron.
import { deflateSync } from "node:zlib";
import { statusColors, type Overall } from "./colors";

const CRC_TABLE = (() => {
  const t = new Uint32Array(256);
  for (let n = 0; n < 256; n++) {
    let c = n;
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    t[n] = c >>> 0;
  }
  return t;
})();

function crc32(buf: Uint8Array): number {
  let c = 0xffffffff;
  for (const b of buf) c = CRC_TABLE[(c ^ b) & 0xff]! ^ (c >>> 8);
  return (c ^ 0xffffffff) >>> 0;
}

function chunk(type: string, data: Uint8Array): Buffer {
  const out = Buffer.alloc(12 + data.length);
  out.writeUInt32BE(data.length, 0);
  out.write(type, 4, "ascii");
  Buffer.from(data.buffer, data.byteOffset, data.length).copy(out, 8);
  out.writeUInt32BE(crc32(out.subarray(4, 8 + data.length)), 8 + data.length);
  return out;
}

/** Encodes straight (non-premultiplied) RGBA pixels, row-major, as an 8-bit RGBA PNG. */
export function encodePng(width: number, height: number, rgba: Uint8Array): Buffer {
  if (rgba.length !== width * height * 4) throw new Error("encodePng: pixel buffer has the wrong size");
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(width, 0);
  ihdr.writeUInt32BE(height, 4);
  ihdr[8] = 8; // bit depth
  ihdr[9] = 6; // RGBA
  const stride = width * 4;
  const raw = Buffer.alloc((stride + 1) * height); // each row starts with filter type 0
  for (let y = 0; y < height; y++) {
    raw[y * (stride + 1)] = 0;
    Buffer.from(rgba.buffer, rgba.byteOffset + y * stride, stride).copy(raw, y * (stride + 1) + 1);
  }
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk("IHDR", ihdr),
    chunk("IDAT", deflateSync(raw, { level: 9 })),
    chunk("IEND", new Uint8Array(0)),
  ]);
}

function hexToRgb(hex: string): [number, number, number] {
  const n = parseInt(hex.slice(1), 16);
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
}

const SS = 4; // supersampling per axis, for smooth edges at 16 px

/**
 * The tray icon: a status-coloured disc with the three stacked layers of the Hosbah mark in white.
 * Drawn in unit space (0..1) so every size (16, 32, @2x ...) is the same picture.
 */
export function statusIconRgba(level: Overall, size: number): Uint8Array {
  const [r, g, b] = hexToRgb(statusColors[level]);
  const out = new Uint8Array(size * size * 4);
  // Three layers: [x0, x1, y0, y1] in unit coordinates, narrowing downwards like a stack.
  const bars: [number, number, number, number][] = [
    [0.3, 0.7, 0.26, 0.4],
    [0.24, 0.76, 0.44, 0.58],
    [0.3, 0.7, 0.62, 0.76],
  ];
  for (let y = 0; y < size; y++) {
    for (let x = 0; x < size; x++) {
      let disc = 0;
      let bar = 0;
      for (let sy = 0; sy < SS; sy++) {
        for (let sx = 0; sx < SS; sx++) {
          const u = (x + (sx + 0.5) / SS) / size;
          const v = (y + (sy + 0.5) / SS) / size;
          const dx = u - 0.5;
          const dy = v - 0.5;
          if (dx * dx + dy * dy <= 0.47 * 0.47) {
            disc++;
            if (bars.some(([x0, x1, y0, y1]) => u >= x0 && u <= x1 && v >= y0 && v <= y1)) bar++;
          }
        }
      }
      const n = SS * SS;
      const a = disc / n;
      if (a === 0) continue;
      const w = bar / disc; // share of the covered area that is white
      const i = (y * size + x) * 4;
      out[i] = Math.round(r + (255 - r) * w);
      out[i + 1] = Math.round(g + (255 - g) * w);
      out[i + 2] = Math.round(b + (255 - b) * w);
      out[i + 3] = Math.round(a * 255);
    }
  }
  return out;
}

export function statusIconPng(level: Overall, size: number): Buffer {
  return encodePng(size, size, statusIconRgba(level, size));
}
