// The contract between the main process and the renderer. The renderer never holds a token: it
// calls these channels and gets data (or a failure) back.
import type { Locale } from "@hosbah/client";

/** Channels the renderer may invoke. Anything else is refused by the preload. */
export const INVOKE_CHANNELS = [
  "session:info",
  "session:signIn",
  "session:twoFactor",
  "session:signOut",
  "session:setTeam",
  "data",
  "action",
  "alerts:markRead",
  "prefs:get",
  "prefs:set",
  "monitor:state",
  "app:info",
] as const;
export type InvokeChannel = (typeof INVOKE_CHANNELS)[number];

/** Events the main process pushes to the renderer. */
export const EVENT_CHANNELS = ["session", "navigate", "monitor", "prefs"] as const;
export type EventChannel = (typeof EVENT_CHANNELS)[number];

/** Read calls the renderer may make on the signed-in API client. */
export const DATA_FNS = [
  "servers",
  "server",
  "serverMetrics",
  "apps",
  "app",
  "deployments",
  "deployment",
  "domains",
  "certificates",
  "dataServices",
  "backups",
  "notifications",
  "prefs",
  "putPrefs",
] as const;
export type DataFn = (typeof DATA_FNS)[number];

export type AppAction = "redeploy" | "restart" | "start" | "stop";

export interface ActionRequest {
  team: string;
  app: string;
  appName: string;
  action: AppAction;
}

export type Reply<T> = { ok: true; data: T } | { ok: false; status: number; code?: string; message: string };

export interface AppInfo {
  version: string;
  platform: "windows" | "macos" | "linux";
  touchId: boolean;
  /** Test mode: a local server only, no protocol registration. */
  e2e: boolean;
  /** Where session credentials are encrypted at rest (false: nothing secret is written). */
  encrypted: boolean;
  locale: Locale;
}

export interface BridgeApi {
  invoke(channel: InvokeChannel, ...args: unknown[]): Promise<unknown>;
  on(channel: EventChannel, cb: (payload: unknown) => void): () => void;
}
