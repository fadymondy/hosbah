import { describe, expect, it } from "vitest";
import { ar, en, isRTL, pickLocale, t } from "../i18n";
import { EVENT_NAMES } from "../types";

const ph = (s: string) => [...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort();

describe("i18n", () => {
  it("has the same keys in ar and en", () => {
    expect(Object.keys(ar).sort()).toEqual(Object.keys(en).sort());
  });
  it("has the same placeholders in every string", () => {
    for (const k of Object.keys(en) as (keyof typeof en)[]) expect(ph(ar[k]), k).toEqual(ph(en[k]));
  });
  it("has no empty strings", () => {
    for (const k of Object.keys(en) as (keyof typeof en)[]) {
      expect(en[k].length, k).toBeGreaterThan(0);
      expect(ar[k].length, k).toBeGreaterThan(0);
    }
  });
  it("covers every push event with a name and a description", () => {
    for (const e of EVENT_NAMES) {
      expect(`event.${e}` in en, e).toBe(true);
      expect(`event.${e}.desc` in en, e).toBe(true);
    }
  });
  it("brands Arabic as حوسبة", () => expect(t("ar", "app.name")).toBe("حوسبة"));
  it("substitutes variables and leaves unknown ones", () => {
    expect(t("en", "confirm.restart", { app: "web" })).toBe("Restart web?");
    expect(t("en", "confirm.restart")).toBe("Restart {app}?");
    expect(t("ar", "settings.version", { version: "1.2" })).toContain("1.2");
  });
  it("rtl and locale pick", () => {
    expect(isRTL("ar")).toBe(true);
    expect(isRTL("en")).toBe(false);
    expect(pickLocale("en-US")).toBe("en");
    expect(pickLocale("ar-EG")).toBe("ar");
    expect(pickLocale("fr")).toBe("ar");
    expect(pickLocale(undefined)).toBe("ar");
  });
});
