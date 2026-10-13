// Pure health rules shared by the phone and the desktop tray. No I/O, no clock unless passed in.
import type { App, BackupSummary, Certificate, DataService, Role, Server } from "./types";

export type Health = "ok" | "warn" | "down";
export type Overall = "green" | "amber" | "red";

/** A CPU / memory / disk reading at or above this is a warning. */
export const WARN_PERCENT = 90;
/** A certificate this close to expiry (days) is a warning / a failure. */
export const CERT_WARN_DAYS = 21;
export const CERT_DOWN_DAYS = 7;
/** A scheduled backup with no success for this long is a warning. */
export const BACKUP_STALE_HOURS = 48;

const worst = (...hs: Health[]): Health => (hs.includes("down") ? "down" : hs.includes("warn") ? "warn" : "ok");

export function usagePercent(used: number, total: number): number {
  return total > 0 ? (used / total) * 100 : 0;
}

/**
 * - failed enrolment, or an enrolled server whose agent stopped heartbeating: down
 * - any of CPU / memory / disk at or above 90 %: warn
 * - pending / provisioning (nothing to watch yet): ok
 */
export function serverHealth(s: Pick<Server, "status" | "online" | "metrics">): Health {
  if (s.status === "failed") return "down";
  if (s.status !== "enrolled") return "ok";
  if (!s.online) return "down";
  const m = s.metrics;
  if (!m) return "ok";
  const hot =
    m.cpu_percent >= WARN_PERCENT ||
    usagePercent(m.mem_used_bytes, m.mem_total_bytes) >= WARN_PERCENT ||
    usagePercent(m.disk_used_bytes, m.disk_total_bytes) >= WARN_PERCENT;
  return hot ? "warn" : "ok";
}

/**
 * - failed, or an imported app whose workload is no longer found: down
 * - stopped, or running while its newest deployment failed (the previous release still serves): warn
 * - new / deploying / running: ok
 */
export function appHealth(a: Pick<App, "status" | "imported_state" | "latest_deployment">): Health {
  if (a.status === "failed" || a.imported_state === "missing") return "down";
  if (a.status === "stopped") return "warn";
  if (a.status === "running" && a.latest_deployment?.status === "failed") return "warn";
  return "ok";
}

/** days_left <= 7: down, <= 21: warn. Unknown (never read) counts as warn. */
export function certHealth(daysLeft: number | null | undefined): Health {
  if (daysLeft === null || daysLeft === undefined) return "warn";
  if (daysLeft <= CERT_DOWN_DAYS) return "down";
  if (daysLeft <= CERT_WARN_DAYS) return "warn";
  return "ok";
}

/** A certificate row: unreadable is down; a hostname mismatch is a warning; otherwise by days left. */
export function certificateHealth(c: Pick<Certificate, "ok" | "san_match" | "days_left">): Health {
  if (!c.ok) return "down";
  const byDays = certHealth(c.days_left);
  return !c.san_match ? worst("warn", byDays) : byDays;
}

/**
 * - newest backup failed: down
 * - restore test failed, never succeeded despite a schedule, or a live schedule with no success in 48 h: warn
 * - no backups and no schedule (nothing configured), or all fine: ok
 */
export function backupHealth(b: BackupSummary | null | undefined, now: number = Date.now()): Health {
  if (!b) return "ok";
  if (b.last_status === "failed") return "down";
  if (b.last_restore_status === "fail") return "warn";
  if (b.schedule && !b.schedule_paused) {
    if (!b.last_ok_at) return b.last_status === "running" || b.last_status === "pending" ? "ok" : "warn";
    if (now - Date.parse(b.last_ok_at) > BACKUP_STALE_HOURS * 3_600_000) return "warn";
  }
  return "ok";
}

export interface OverallInput {
  servers?: Pick<Server, "status" | "online" | "metrics">[];
  apps?: Pick<App, "status" | "imported_state" | "latest_deployment">[];
  certificates?: Pick<Certificate, "ok" | "san_match" | "days_left">[];
  services?: Pick<DataService, "backups">[];
  now?: number;
}

/** Everything folded into the tray colour: any down is red, any warn is amber, else green. */
export function overallStatus(i: OverallInput): Overall {
  const hs: Health[] = [
    ...(i.servers ?? []).map(serverHealth),
    ...(i.apps ?? []).map(appHealth),
    ...(i.certificates ?? []).map(certificateHealth),
    ...(i.services ?? []).map((s) => backupHealth(s.backups, i.now)),
  ];
  const w = worst(...hs);
  return w === "down" ? "red" : w === "warn" ? "amber" : "green";
}

/** Redeploy / restart / start / stop need owner | admin | deployer. */
export function canOperate(role: Role | string | null | undefined): boolean {
  return role === "owner" || role === "admin" || role === "deployer";
}
