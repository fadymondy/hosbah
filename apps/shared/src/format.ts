// Number and time formatting. Latin digits in both languages (like the web console). No Intl
// dependency (Hermes ships a partial Intl), so the Arabic plural forms are written out.
import type { Locale } from "./types";

export function formatBytes(bytes: number | null | undefined, digits = 1): string {
  if (bytes === null || bytes === undefined || !Number.isFinite(bytes)) return "-";
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  let v = Math.max(0, bytes);
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  const n = i === 0 ? String(Math.round(v)) : v.toFixed(v >= 100 ? 0 : digits).replace(/\.0+$/, "");
  return `${n} ${units[i]}`;
}

export function formatPercent(value: number | null | undefined, digits = 0): string {
  if (value === null || value === undefined || !Number.isFinite(value)) return "-";
  return `${value.toFixed(digits)}%`;
}

type Unit = "minute" | "hour" | "day" | "month" | "year";
const SECONDS: Record<Unit, number> = { minute: 60, hour: 3600, day: 86_400, month: 2_592_000, year: 31_536_000 };

// Arabic forms: [one, two, 3-10, 11+].
const AR: Record<Unit, [string, string, string, string]> = {
  minute: ["دقيقة", "دقيقتين", "دقائق", "دقيقة"],
  hour: ["ساعة", "ساعتين", "ساعات", "ساعة"],
  day: ["يوم", "يومين", "أيام", "يومًا"],
  month: ["شهر", "شهرين", "أشهر", "شهرًا"],
  year: ["سنة", "سنتين", "سنوات", "سنة"],
};

function arCount(unit: Unit, n: number): string {
  const f = AR[unit];
  if (n === 1) return f[0];
  if (n === 2) return f[1];
  return `${n} ${n >= 3 && n <= 10 ? f[2] : f[3]}`;
}

/** "5 minutes ago" / "منذ 5 دقائق"; future times read "in 2 hours" / "خلال ساعتين". */
export function relativeTime(when: string | number | Date | null | undefined, locale: Locale = "ar", now: number = Date.now()): string {
  if (when === null || when === undefined || when === "") return "-";
  const t = when instanceof Date ? when.getTime() : typeof when === "number" ? when : Date.parse(when);
  if (!Number.isFinite(t)) return "-";
  const diff = Math.round((t - now) / 1000);
  const abs = Math.abs(diff);
  if (abs < 45) return locale === "ar" ? "الآن" : "just now";
  const unit: Unit = abs < 3570 ? "minute" : abs < 84_600 ? "hour" : abs < SECONDS.month ? "day" : abs < SECONDS.year ? "month" : "year";
  const n = Math.max(1, Math.round(abs / SECONDS[unit]));
  const future = diff > 0;
  if (locale === "ar") return `${future ? "خلال" : "منذ"} ${arCount(unit, n)}`;
  const label = `${n} ${unit}${n === 1 ? "" : "s"}`;
  return future ? `in ${label}` : `${label} ago`;
}

/** The two largest non-zero units: "3d 4h", "2h 5m", "45s" (en); "3 ي 4 س", "45 ث" (ar). */
export function duration(seconds: number | null | undefined, locale: Locale = "ar"): string {
  if (seconds === null || seconds === undefined || !Number.isFinite(seconds)) return "-";
  let s = Math.max(0, Math.floor(seconds));
  const d = Math.floor(s / 86_400);
  s -= d * 86_400;
  const h = Math.floor(s / 3600);
  s -= h * 3600;
  const m = Math.floor(s / 60);
  s -= m * 60;
  const ar = locale === "ar";
  const units: [number, string][] = [
    [d, ar ? "ي" : "d"],
    [h, ar ? "س" : "h"],
    [m, ar ? "د" : "m"],
    [s, ar ? "ث" : "s"],
  ];
  const first = units.findIndex(([n]) => n > 0);
  if (first < 0) return ar ? "0 ث" : "0s";
  return units
    .slice(first, first + 2)
    .filter(([n]) => n > 0)
    .map(([n, u]) => (ar ? `${n} ${u}` : `${n}${u}`))
    .join(" ");
}
