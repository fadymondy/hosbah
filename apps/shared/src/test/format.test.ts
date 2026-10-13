import { describe, expect, it } from "vitest";
import { duration, formatBytes, formatPercent, relativeTime } from "../format";

const now = Date.parse("2026-10-10T12:00:00Z");
const ago = (s: number) => now - s * 1000;

describe("formatBytes / formatPercent", () => {
  it("formats", () => {
    expect(formatBytes(0)).toBe("0 B");
    expect(formatBytes(1536)).toBe("1.5 KB");
    expect(formatBytes(1024 * 1024)).toBe("1 MB");
    expect(formatBytes(250 * 1024 ** 3)).toBe("250 GB");
    expect(formatBytes(null)).toBe("-");
    expect(formatPercent(42.4)).toBe("42%");
    expect(formatPercent(42.46, 1)).toBe("42.5%");
    expect(formatPercent(undefined)).toBe("-");
  });
});

describe("relativeTime", () => {
  it("english", () => {
    expect(relativeTime(ago(10), "en", now)).toBe("just now");
    expect(relativeTime(ago(300), "en", now)).toBe("5 minutes ago");
    expect(relativeTime(ago(3600), "en", now)).toBe("1 hour ago");
    expect(relativeTime(now + 7200_000, "en", now)).toBe("in 2 hours");
    expect(relativeTime(ago(3 * 86400), "en", now)).toBe("3 days ago");
  });
  it("arabic uses Latin digits and proper plurals", () => {
    expect(relativeTime(ago(10), "ar", now)).toBe("الآن");
    expect(relativeTime(ago(60), "ar", now)).toBe("منذ دقيقة");
    expect(relativeTime(ago(120), "ar", now)).toBe("منذ دقيقتين");
    expect(relativeTime(ago(300), "ar", now)).toBe("منذ 5 دقائق");
    expect(relativeTime(ago(15 * 60), "ar", now)).toBe("منذ 15 دقيقة");
    expect(relativeTime(now + 7200_000, "ar", now)).toBe("خلال ساعتين");
    expect(relativeTime(ago(3 * 86400), "ar", now)).toBe("منذ 3 أيام");
  });
  it("handles bad input and ISO strings", () => {
    expect(relativeTime("nope", "en", now)).toBe("-");
    expect(relativeTime(null, "en", now)).toBe("-");
    expect(relativeTime("2026-10-10T11:55:00Z", "en", now)).toBe("5 minutes ago");
  });
});

describe("duration", () => {
  it("shows two largest units", () => {
    expect(duration(45, "en")).toBe("45s");
    expect(duration(125, "en")).toBe("2m 5s");
    expect(duration(3 * 86400 + 4 * 3600 + 59, "en")).toBe("3d 4h");
    expect(duration(0, "en")).toBe("0s");
    expect(duration(90, "ar")).toBe("1 د 30 ث");
    expect(duration(undefined)).toBe("-");
  });
});
