// The tray menu as data: the main process turns it into a native Menu, and the renderer draws the
// same model as a preview (a native menu cannot be screenshotted).
import type { Locale, Overall } from "@hosbah/client";
import { tr } from "./strings";
import { linkToHash, routeForNotification, type AppRef } from "./route";
import { computeTray, type TeamAlert, type TeamSnapshot } from "./trayStatus";

export interface TrayModel {
  level: Overall;
  statusLine: string;
  unread: number;
  unreadLine: string;
  recentTitle: string;
  recent: { id: string; title: string; unread: boolean; hash: string }[];
  emptyLine: string;
  openLabel: string;
  quitLabel: string;
  tooltip: string;
}

const clip = (s: string, n: number) => (s.length > n ? s.slice(0, n - 1) + "…" : s);

export function buildTrayModel(locale: Locale, snapshots: TeamSnapshot[], alerts: TeamAlert[], now: number = Date.now(), limit = 5): TrayModel {
  const s = computeTray(snapshots, alerts, now);
  const apps: Record<string, AppRef[]> = Object.fromEntries(snapshots.map((x) => [x.team, x.apps.map((a) => ({ id: a.id, name: a.name }))]));
  const recent = [...alerts]
    .sort((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at))
    .slice(0, limit)
    .map((a) => ({
      id: a.id,
      title: clip(a.title, 56),
      unread: !a.read_at,
      hash: linkToHash(routeForNotification(a.team, a, apps[a.team] ?? [])),
    }));
  const statusLine = tr(locale, `overall.${s.level}`);
  return {
    level: s.level,
    statusLine,
    unread: s.unread,
    unreadLine: s.unread > 0 ? tr(locale, "tray.unreadCount", { count: s.unread }) : tr(locale, "tray.none"),
    recentTitle: tr(locale, "tray.recent"),
    recent,
    emptyLine: tr(locale, "tray.none"),
    openLabel: tr(locale, "app.open"),
    quitLabel: tr(locale, "app.quit"),
    tooltip: tr(locale, "tray.tooltip", { status: statusLine }),
  };
}
