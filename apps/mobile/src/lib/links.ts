import { parseDeepLink, pushDataToLink, type DeepLink, type PushData } from "@hosbah/client";

// One place that turns "something was tapped / opened" into a destination.
// Sources: a pushed notification (foreground banner, background tap, cold start), and a hosbah:// URL
// handed in by Linking (expo-router feeds those through +native-intent.ts).

/** A destination. `team` is absent for links that mean "the team you are already in" (hosbah://alerts). */
export interface Destination {
  team?: string;
  screen: DeepLink["screen"];
  id?: string;
}

/** hosbah://t/<team>/..., https://<host>/t/<team>/..., and the bare hosbah://alerts of the test notification. */
export function parseAnyLink(url: string | null | undefined): Destination | null {
  if (!url) return null;
  const full = parseDeepLink(url);
  if (full) return full;
  const m = /^hosbah:\/\/\/?(alerts|domains|backups)\/?(?:[?#].*)?$/i.exec(url.trim());
  if (m?.[1]) return { screen: m[1].toLowerCase() as "alerts" | "domains" | "backups" };
  return null;
}

/** A push message's data map to a destination (its `url` first, then team + subject). */
export function destinationFromPush(data: PushData | Record<string, unknown> | null | undefined): Destination | null {
  if (!data) return null;
  const direct = pushDataToLink(data);
  if (direct) return direct;
  const url = typeof data.url === "string" ? parseAnyLink(data.url) : null;
  return url;
}

export type RouteTarget =
  | { pathname: "/(tabs)/alerts" | "/(tabs)/infra" | "/(tabs)/apps" | "/(tabs)" }
  | { pathname: "/(tabs)/infra"; params: { tab: "domains" | "backups" } }
  | { pathname: "/apps/[id]"; params: { id: string } }
  | { pathname: "/servers/[id]"; params: { id: string } };

/** expo-router target for a destination. */
export function routeFor(d: Destination): RouteTarget {
  switch (d.screen) {
    case "app":
      return d.id ? { pathname: "/apps/[id]", params: { id: d.id } } : { pathname: "/(tabs)/apps" };
    case "server":
      return d.id ? { pathname: "/servers/[id]", params: { id: d.id } } : { pathname: "/(tabs)" };
    case "domains":
      return { pathname: "/(tabs)/infra", params: { tab: "domains" } };
    case "backups":
      return { pathname: "/(tabs)/infra", params: { tab: "backups" } };
    default:
      return { pathname: "/(tabs)/alerts" };
  }
}

// ── pending queue ─────────────────────────────────────────────────────────
// A link can arrive before the app is signed in or the router is mounted (cold start). It waits here
// until the signed-in navigator mounts and consumes it.

let pending: Destination | null = null;
const listeners = new Set<() => void>();

export function dispatchLink(d: Destination): void {
  pending = d;
  for (const l of listeners) l();
}

export function takePendingLink(): Destination | null {
  const p = pending;
  pending = null;
  return p;
}

export function onLink(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}
