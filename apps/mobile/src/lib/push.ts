import type { HosbahClient, Locale } from "@hosbah/client";
import type { RemoteMessage } from "@react-native-firebase/messaging";
import { useSyncExternalStore } from "react";
import { PermissionsAndroid, Platform } from "react-native";

import { APP_VERSION } from "@/lib/config";
import { rnfbApp, rnfbCrashlytics, rnfbMessaging } from "@/lib/firebase";
import { destinationFromPush, dispatchLink, type Destination } from "@/lib/links";
import { getStored, KEYS, removeStored, setStored } from "@/lib/storage";

// Push through Firebase Cloud Messaging (APNs is reached via Firebase). Every entry point is a no-op in
// a build without the Firebase config files (see src/lib/firebase.ts).
//
// The server sends `data = {team, kind, subject_type, subject_id, url}` and, on Android, channel_id
// "alerts" (created natively by plugins/with-alerts-channel.js).

const ASKED_KEY = "hosbah.push.asked";

export type PushState = "unavailable" | "off" | "denied" | "on";

type MessagingModule = typeof import("@react-native-firebase/messaging");
type Messaging = ReturnType<MessagingModule["getMessaging"]>;
const fm = () => rnfbMessaging() as MessagingModule;

function messaging(): Messaging | null {
  const mod = rnfbMessaging();
  const app = rnfbApp();
  if (!mod || !app) return null;
  try {
    if (app.getApps().length === 0) return null;
    return mod.getMessaging();
  } catch {
    return null;
  }
}

export function pushAvailable(): boolean {
  return messaging() !== null;
}

/** Records a non-fatal error with Crashlytics when it is linked; never throws. */
export function reportError(e: unknown, context: string): void {
  try {
    const c = rnfbCrashlytics();
    if (!c) return;
    const inst = c.getCrashlytics();
    c.log(inst, context);
    c.recordError(inst, e instanceof Error ? e : new Error(String(e)));
  } catch {
    // ignore
  }
}

type Permission = "granted" | "denied" | "undetermined";

async function osPermission(m: Messaging, ask: boolean): Promise<Permission> {
  if (Platform.OS === "android") {
    if (typeof Platform.Version === "number" && Platform.Version < 33) return "granted"; // granted at install
    const perm = PermissionsAndroid.PERMISSIONS.POST_NOTIFICATIONS;
    if (await PermissionsAndroid.check(perm)) return "granted";
    if (!ask) return "undetermined";
    const r = await PermissionsAndroid.request(perm);
    if (r === PermissionsAndroid.RESULTS.GRANTED) return "granted";
    return r === PermissionsAndroid.RESULTS.NEVER_ASK_AGAIN ? "denied" : "undetermined";
  }
  const s = ask ? await fm().requestPermission(m) : await fm().hasPermission(m);
  const A = fm().AuthorizationStatus;
  if (s === A.AUTHORIZED || s === A.PROVISIONAL || s === A.EPHEMERAL) return "granted";
  return s === A.DENIED ? "denied" : "undetermined";
}

/** PUT /api/me/devices/{id}/push with the current FCM token. Re-sent on every refresh. */
async function register(client: HosbahClient, locale: Locale, token: string): Promise<void> {
  await client.setPushToken(token, { locale, app_version: APP_VERSION });
  await setStored(KEYS.pushToken, token);
}

/** What Settings shows. Never throws. */
export async function pushStatus(): Promise<PushState> {
  const m = messaging();
  if (!m) return "unavailable";
  try {
    const p = await osPermission(m, false);
    if (p === "denied") return "denied";
    return p === "granted" && (await getStored(KEYS.pushToken)) ? "on" : "off";
  } catch (e) {
    reportError(e, "push: status");
    return "off";
  }
}

/** Asks for permission (the OS prompts at most once) and registers this device. */
export async function enablePush(client: HosbahClient, locale: Locale): Promise<PushState> {
  const m = messaging();
  if (!m) return "unavailable";
  try {
    const p = await osPermission(m, true);
    if (p !== "granted") return p === "denied" ? "denied" : "off";
    await register(client, locale, await fm().getToken(m));
    return "on";
  } catch (e) {
    reportError(e, "push: enable");
    throw e;
  }
}

// ── in-app banner for foreground messages ────────────────────────────────

export interface Banner {
  id: number;
  title: string;
  body: string;
  dest: Destination | null;
}

let banner: Banner | null = null;
let bannerSeq = 0;
const bannerListeners = new Set<() => void>();

export function showBanner(b: Omit<Banner, "id">): void {
  banner = { ...b, id: ++bannerSeq };
  bannerListeners.forEach((l) => l());
}

export function dismissBanner(): void {
  banner = null;
  bannerListeners.forEach((l) => l());
}

export function useBanner(): Banner | null {
  return useSyncExternalStore(
    (l) => {
      bannerListeners.add(l);
      return () => bannerListeners.delete(l);
    },
    () => banner,
    () => banner,
  );
}

/** Title/body/destination of a push, whether it carries a notification block or only data. */
export function bannerFrom(msg: Pick<RemoteMessage, "notification" | "data">): Omit<Banner, "id"> {
  const data = (msg.data ?? {}) as Record<string, unknown>;
  const str = (v: unknown) => (typeof v === "string" ? v : "");
  return {
    title: msg.notification?.title ?? str(data.title),
    body: msg.notification?.body ?? str(data.body),
    dest: destinationFromPush(data),
  };
}

// ── keeping a signed-in device registered ────────────────────────────────

let coldStartHandled = false;

export interface WatchOptions {
  /** Called whenever a push arrives while the app is open (refresh the lists behind it). */
  onReceived?: () => void;
  /** Ask for permission now if it has never been asked (on the first sign-in). */
  askOnce?: boolean;
}

/**
 * Registers the FCM token while a session is valid and re-registers it when it rotates; shows foreground
 * messages as a banner; routes a tapped notification (background tap and cold start) via the link queue.
 * Returns a cleanup.
 */
export function watchPush(client: HosbahClient, getLocale: () => Locale, o: WatchOptions = {}): () => void {
  const m = messaging();
  if (!m) return () => undefined;
  const offs: (() => void)[] = [];
  let alive = true;

  const refresh = async (token?: string) => {
    let ask = false;
    if (o.askOnce && (await getStored(ASKED_KEY)) !== "1") {
      await setStored(ASKED_KEY, "1");
      ask = true;
    }
    if ((await osPermission(m, ask)) !== "granted") return;
    await register(client, getLocale(), token ?? (await fm().getToken(m)));
  };
  void refresh().catch((e) => reportError(e, "push: refresh"));

  try {
    offs.push(
      fm().onTokenRefresh(m, (token) => {
        if (alive) void refresh(token).catch((e) => reportError(e, "push: rotate"));
      }),
      fm().onMessage(m, async (msg: RemoteMessage) => {
        showBanner(bannerFrom(msg));
        o.onReceived?.();
      }),
      fm().onNotificationOpenedApp(m, (msg: RemoteMessage) => {
        const d = destinationFromPush(msg.data);
        if (d) dispatchLink(d);
      }),
    );
  } catch (e) {
    reportError(e, "push: listen");
  }

  if (!coldStartHandled) {
    coldStartHandled = true;
    void fm()
      .getInitialNotification(m)
      .then((msg) => {
        const d = msg ? destinationFromPush(msg.data) : null;
        if (d) dispatchLink(d);
      })
      .catch((e) => reportError(e, "push: initial"));
  }

  return () => {
    alive = false;
    for (const off of offs) {
      try {
        off();
      } catch {
        // ignore
      }
    }
  };
}

export async function forgetPushToken(): Promise<void> {
  await removeStored(KEYS.pushToken);
}
