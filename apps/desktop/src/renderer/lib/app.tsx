// App-wide state: the session, prefs, language/theme, the monitor's live summary and the hash route.
import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { palettes } from "@hosbah/client/brand";
import { isRTL } from "@hosbah/client/i18n";
import type { Locale } from "@hosbah/client";
import type { AppInfo } from "../../core/ipc";
import { tr, type Key } from "../../core/strings";
import type { SessionInfo } from "../../main/session";
import type { MonitorState } from "../../main/monitor";
import type { Prefs } from "../../main/store";
import { call, on } from "./bridge";

export interface Route {
  /** "" before sign-in / for settings. */
  team: string;
  section: "overview" | "apps" | "domains" | "backups" | "alerts" | "servers" | "settings" | "tray-preview";
  id?: string;
}

export function parseHash(hash: string, fallbackTeam: string): Route {
  const h = hash.replace(/^#\/?/, "");
  const seg = h.split("/").filter(Boolean).map(decodeURIComponent);
  if (seg[0] === "settings") return { team: fallbackTeam, section: "settings" };
  if (seg[0] === "tray-preview") return { team: fallbackTeam, section: "tray-preview" };
  if (seg[0] === "t" && seg[1]) {
    const section = (["apps", "domains", "backups", "alerts", "servers"] as const).find((s) => s === seg[2]);
    return { team: seg[1], section: section ?? "overview", ...(seg[3] ? { id: seg[3] } : {}) };
  }
  return { team: fallbackTeam, section: "overview" };
}

export const hashFor = (r: Route): string =>
  r.section === "settings" || r.section === "tray-preview"
    ? `#/${r.section}`
    : `#/t/${encodeURIComponent(r.team)}${r.section === "overview" ? "" : `/${r.section}`}${r.id ? `/${encodeURIComponent(r.id)}` : ""}`;

interface Ctx {
  ready: boolean;
  info: AppInfo | null;
  session: SessionInfo | null;
  prefs: Prefs;
  monitor: MonitorState | null;
  locale: Locale;
  dir: "rtl" | "ltr";
  dark: boolean;
  route: Route;
  t: (key: Key, vars?: Record<string, string | number>) => string;
  go: (r: Route) => void;
  setPrefs: (p: Partial<Prefs>) => Promise<void>;
  setTeam: (slug: string) => Promise<void>;
  refreshSession: () => Promise<void>;
}

const AppCtx = createContext<Ctx | null>(null);
export const useApp = (): Ctx => {
  const c = useContext(AppCtx);
  if (!c) throw new Error("useApp outside AppProvider");
  return c;
};

const DEFAULT_PREFS: Prefs = { theme: "system", locale: "ar", apiUrl: "", launchAtLogin: false };

function applyPalette(dark: boolean): void {
  const p = dark ? palettes.dark : palettes.light;
  const v: Record<string, string> = {
    brand: p.brand,
    action: p.action,
    "on-action": p.onAction,
    bg: p.bg,
    card: p.card,
    elevated: p.elevated,
    soft: p.soft,
    line: p.line,
    fg: p.fg,
    body: p.body,
    muted: p.muted,
    gold: p.gold,
    ok: p.ok,
    warn: p.warn,
    danger: p.danger,
    "ok-text": p.okText,
    "warn-text": p.warnText,
    "danger-text": p.dangerText,
  };
  const s = document.documentElement.style;
  for (const [k, val] of Object.entries(v)) s.setProperty(`--${k}`, val);
  document.documentElement.style.colorScheme = dark ? "dark" : "light";
}

export function AppProvider({ children }: { children: ReactNode }) {
  const [ready, setReady] = useState(false);
  const [info, setInfo] = useState<AppInfo | null>(null);
  const [session, setSession] = useState<SessionInfo | null>(null);
  const [prefs, setPrefsState] = useState<Prefs>(DEFAULT_PREFS);
  const [monitor, setMonitor] = useState<MonitorState | null>(null);
  const [hash, setHash] = useState(window.location.hash);
  const [systemDark, setSystemDark] = useState(window.matchMedia("(prefers-color-scheme: dark)").matches);

  useEffect(() => {
    const mq = window.matchMedia("(prefers-color-scheme: dark)");
    const h = () => setSystemDark(mq.matches);
    mq.addEventListener("change", h);
    return () => mq.removeEventListener("change", h);
  }, []);

  useEffect(() => {
    const onHash = () => setHash(window.location.hash);
    window.addEventListener("hashchange", onHash);
    const offs = [
      on("session", (s: SessionInfo) => setSession(s)),
      on("prefs", (p: Prefs) => setPrefsState(p)),
      on("monitor", (m: MonitorState) => setMonitor(m)),
      on("navigate", (h: string) => {
        window.location.hash = h;
      }),
    ];
    void (async () => {
      const [i, s, p, m] = await Promise.all([call<AppInfo>("app:info"), call<SessionInfo>("session:info"), call<Prefs>("prefs:get"), call<MonitorState>("monitor:state")]);
      setInfo(i);
      setSession(s);
      setPrefsState(p);
      setMonitor(m);
      setReady(true);
    })();
    return () => {
      window.removeEventListener("hashchange", onHash);
      offs.forEach((o) => o());
    };
  }, []);

  const locale = prefs.locale;
  const dir = isRTL(locale) ? "rtl" : "ltr";
  const dark = prefs.theme === "dark" || (prefs.theme === "system" && systemDark);

  useEffect(() => {
    document.documentElement.lang = locale;
    document.documentElement.dir = dir;
    applyPalette(dark);
  }, [locale, dir, dark]);

  const fallbackTeam = session?.activeTeam ?? session?.teams[0]?.slug ?? "";
  const route = useMemo(() => parseHash(hash, fallbackTeam), [hash, fallbackTeam]);
  const t = useCallback((key: Key, vars?: Record<string, string | number>) => tr(locale, key, vars), [locale]);
  const go = useCallback((r: Route) => {
    window.location.hash = hashFor(r);
  }, []);
  const setPrefs = useCallback(async (p: Partial<Prefs>) => {
    setPrefsState(await call<Prefs>("prefs:set", p));
  }, []);
  const setTeam = useCallback(async (slug: string) => {
    await call("session:setTeam", slug);
    window.location.hash = `#/t/${encodeURIComponent(slug)}`;
  }, []);
  const refreshSession = useCallback(async () => setSession(await call<SessionInfo>("session:info")), []);

  const value: Ctx = { ready, info, session, prefs, monitor, locale, dir, dark, route, t, go, setPrefs, setTeam, refreshSession };
  return <AppCtx.Provider value={value}>{children}</AppCtx.Provider>;
}
