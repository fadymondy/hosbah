// Watches every team the account is in: one SSE notification stream per team (raises native
// notifications for new unread alerts, respecting the per-event prefs) plus a slow poll of
// servers / apps / certificates / data services so the tray colour follows real state.
import { Notification } from "electron";
import { EventEmitter } from "node:events";
import type { AppNotification, EventName, NotificationPrefs, StreamHandle } from "@hosbah/client";
import { buildTrayModel, type TrayModel } from "../core/trayModel";
import { linkToHash, routeForNotification } from "../core/route";
import type { TeamAlert, TeamSnapshot } from "../core/trayStatus";
import * as session from "./session";
import * as store from "./store";

export interface MonitorState {
  tray: TrayModel;
  alerts: TeamAlert[];
}

export const events = new EventEmitter();

interface TeamState {
  snap: TeamSnapshot;
  alerts: TeamAlert[];
  seen: Set<string>;
  primed: boolean;
  stream: StreamHandle | null;
}

const teams = new Map<string, TeamState>();
let prefs: Partial<NotificationPrefs> = {};
let poll: ReturnType<typeof setInterval> | null = null;
/** Test hook: every notification we raised, in order. */
export const raised: { team: string; id: string; title: string; hash: string }[] = [];

const POLL_MS = 60_000;

const empty = (team: string): TeamState => ({
  snap: { team, servers: [], apps: [], certificates: [], services: [] },
  alerts: [],
  seen: new Set(),
  primed: false,
  stream: null,
});

export function state(): MonitorState {
  const snaps = [...teams.values()].map((t) => t.snap);
  const alerts = [...teams.values()].flatMap((t) => t.alerts);
  return { tray: buildTrayModel(store.loadPrefs().locale, snaps, alerts), alerts };
}

function changed(): void {
  events.emit("change", state());
}

export async function loadPrefs(): Promise<void> {
  const c = session.api();
  if (!c) return;
  try {
    prefs = (await c.prefs()).events;
  } catch {
    /* keep what we had */
  }
}

export function setPrefs(p: Partial<NotificationPrefs>): void {
  prefs = { ...prefs, ...p };
}

/** Called when the person opens an alert from a native notification or the tray. */
export type OpenHandler = (hash: string) => void;
let onOpen: OpenHandler = () => undefined;
export function setOpenHandler(h: OpenHandler): void {
  onOpen = h;
}

function raise(team: string, n: TeamAlert, ts: TeamState): void {
  if (prefs[n.kind as EventName] === false) return;
  const hash = linkToHash(routeForNotification(team, n, ts.snap.apps.map((a) => ({ id: a.id, name: a.name }))));
  raised.push({ team, id: n.id, title: n.title, hash });
  if (!Notification.isSupported()) return;
  const note = new Notification({ title: n.title, body: n.body ?? "", silent: false });
  note.on("click", () => onOpen(hash));
  note.show();
}

function onList(team: string, list: { notifications: AppNotification[] }): void {
  const ts = teams.get(team);
  if (!ts) return;
  const next = list.notifications.map((n) => ({ ...n, team }));
  if (ts.primed) {
    for (const n of next) if (!n.read_at && !ts.seen.has(n.id)) raise(team, n, ts);
  }
  for (const n of next) ts.seen.add(n.id);
  ts.primed = true;
  ts.alerts = next;
  changed();
  // An alert usually means state moved: look again soon rather than waiting for the poll.
  void refreshTeam(team);
}

export async function refreshTeam(team: string): Promise<void> {
  const c = session.api();
  const ts = teams.get(team);
  if (!c || !ts) return;
  const [servers, apps, certificates, services] = await Promise.allSettled([c.servers(team), c.apps(team), c.certificates(team), c.dataServices(team)]);
  if (servers.status === "fulfilled") ts.snap.servers = servers.value.servers;
  if (apps.status === "fulfilled") ts.snap.apps = apps.value;
  if (certificates.status === "fulfilled") ts.snap.certificates = certificates.value;
  if (services.status === "fulfilled") ts.snap.services = services.value;
  changed();
}

/** Brings the open streams in line with the teams the account is in. */
export function sync(): void {
  const c = session.api();
  const slugs = c ? session.info().teams.map((t) => t.slug) : [];
  for (const [slug, ts] of teams) {
    if (!slugs.includes(slug)) {
      ts.stream?.close();
      teams.delete(slug);
    }
  }
  if (c) {
    for (const slug of slugs) {
      if (teams.has(slug)) continue;
      const ts = empty(slug);
      teams.set(slug, ts);
      ts.stream = c.streamNotifications(slug, (r) => onList(slug, r));
      void refreshTeam(slug);
    }
    poll ??= setInterval(() => {
      for (const slug of teams.keys()) void refreshTeam(slug);
    }, POLL_MS);
  } else if (poll) {
    clearInterval(poll);
    poll = null;
  }
  changed();
}

export function stop(): void {
  for (const ts of teams.values()) ts.stream?.close();
  teams.clear();
  if (poll) clearInterval(poll);
  poll = null;
  changed();
}

/** The renderer marked something read: update our copy so the tray count follows. */
export function markLocal(team: string, id: string): void {
  const ts = teams.get(team);
  if (!ts) return;
  const now = new Date().toISOString();
  ts.alerts = ts.alerts.map((a) => (id === "all" || a.id === id ? { ...a, read_at: a.read_at ?? now } : a));
  changed();
}
