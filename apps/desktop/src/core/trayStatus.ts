// What colour the tray icon is. Pure: it folds the teams' live state (the shared health rules) and
// the unread alerts into one of green / amber / red, plus the counts the menu shows.
import { overallStatus, type App, type AppNotification, type Certificate, type DataService, type Overall, type Server } from "@hosbah/client";

export interface TeamSnapshot {
  team: string;
  servers: Server[];
  apps: App[];
  certificates: Certificate[];
  services: DataService[];
}

export interface TeamAlert extends AppNotification {
  team: string;
}

/** Unread alert kinds that turn the icon red / amber while they are active. */
const RED_KINDS = new Set(["app.down", "server.offline", "deploy.failed", "backup.failed"]);
const AMBER_KINDS = new Set(["resource.high", "cert.expiring"]);

const rank: Record<Overall, number> = { green: 0, amber: 1, red: 2 };
export const worstOf = (...l: Overall[]): Overall => l.reduce((a, b) => (rank[b] > rank[a] ? b : a), "green" as Overall);

/**
 * Unread alerts that still count: read ones never do, and an `app.up` that arrives after an
 * `app.down` for the same app cancels it (the app recovered).
 */
export function activeAlerts(alerts: TeamAlert[]): TeamAlert[] {
  const unread = alerts.filter((a) => !a.read_at).sort((a, b) => Date.parse(a.created_at) - Date.parse(b.created_at));
  const down = new Map<string, TeamAlert>();
  const keep: TeamAlert[] = [];
  for (const a of unread) {
    const key = `${a.team}:${a.subject_id ?? ""}`;
    if (a.kind === "app.up") {
      down.delete(key);
      continue;
    }
    if (a.kind === "app.down") down.set(key, a);
    keep.push(a);
  }
  return keep.filter((a) => a.kind !== "app.down" || down.get(`${a.team}:${a.subject_id ?? ""}`) === a);
}

export function alertLevel(kind: string): Overall {
  if (RED_KINDS.has(kind)) return "red";
  if (AMBER_KINDS.has(kind)) return "amber";
  return "green";
}

export interface TrayState {
  level: Overall;
  /** Unread alerts, all kinds (what the menu says). */
  unread: number;
  /** Per-reason counts that put the icon above green: state-derived and alert-derived. */
  reasons: Record<string, number>;
}

export function computeTray(snapshots: TeamSnapshot[], alerts: TeamAlert[], now: number = Date.now()): TrayState {
  const reasons: Record<string, number> = {};
  let level: Overall = "green";
  for (const s of snapshots) {
    const o = overallStatus({ servers: s.servers, apps: s.apps, certificates: s.certificates, services: s.services, now });
    if (o !== "green") reasons[`state.${o}`] = (reasons[`state.${o}`] ?? 0) + 1;
    level = worstOf(level, o);
  }
  for (const a of activeAlerts(alerts)) {
    const l = alertLevel(a.kind);
    if (l === "green") continue;
    reasons[a.kind] = (reasons[a.kind] ?? 0) + 1;
    level = worstOf(level, l);
  }
  return { level, unread: alerts.filter((a) => !a.read_at).length, reasons };
}
