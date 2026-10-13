// The signed-in account, owned by the main process. The renderer never sees a token: it asks for
// data over IPC (api.ts) and gets data back.
//
// Sign-in: email + password (+ the 2FA challenge) through @hosbah/client in "cookie-header" CSRF
// mode, then once per install POST /api/me/devices so this machine gets a device id and a rotating
// refresh token. The JWT is short-lived; the client refreshes it with POST /api/auth/device/refresh,
// and every rotation goes through `onSession`, which persists (encrypted) before the call returns.
import { app } from "electron";
import { hostname } from "node:os";
import { EventEmitter } from "node:events";
import {
  ApiError,
  HosbahClient,
  normalizeApiUrl,
  twoFactorChallenge,
  type DevicePlatform,
  type Locale,
  type Session,
  type TeamSummary,
} from "@hosbah/client";
import * as store from "./store";

export interface SessionInfo {
  signedIn: boolean;
  email: string;
  apiUrl: string;
  teams: TeamSummary[];
  /** Slug of the active team. */
  activeTeam: string | null;
  /** Last refresh of the account failed because the server could not be reached. */
  offline: boolean;
}

export type SignInResult = { ok: true } | { ok: false; mfa: true; challenge: string };

export const events = new EventEmitter();

let client: HosbahClient | null = null;
let meta: Omit<store.Stored, "secret"> | null = null;
let offline = false;
let retryTimer: ReturnType<typeof setTimeout> | null = null;

const LOCAL = /^(localhost|127\.0\.0\.1|\[::1\])$/;

/** Test mode (screenshots, e2e) may only talk to a local server, never a real one. */
export function assertAllowedUrl(apiUrl: string): void {
  if (process.env.HOSBAH_E2E !== "1") return;
  if (!LOCAL.test(new URL(apiUrl).hostname)) throw new ApiError(0, "test mode: only a local server is allowed", { code: "local_only" });
}

export const platform = (): DevicePlatform => (process.platform === "win32" ? "windows" : process.platform === "darwin" ? "macos" : "linux");

export function api(): HosbahClient | null {
  return client;
}

export function info(): SessionInfo {
  return {
    signedIn: !!client && !!meta,
    email: meta?.email ?? "",
    apiUrl: meta?.apiUrl ?? store.loadPrefs().apiUrl,
    teams: meta?.teams ?? [],
    activeTeam: meta?.activeTeam ?? null,
    offline,
  };
}

function emit(): void {
  events.emit("session", info());
}

function persist(session: Session | null): void {
  if (meta && session) store.saveSession(meta, session);
}

function makeClient(apiUrl: string, session: Partial<Session> | null): HosbahClient {
  const c: HosbahClient = new HosbahClient({
    baseUrl: apiUrl,
    csrf: "cookie-header",
    session,
    // The refresh token rotates on every refresh and the old one dies the moment the server
    // answers, so this must hit the disk before the refresh call returns.
    onSession: (s) => {
      if (client !== c) return;
      if (s) persist(s);
    },
    onAuthRequired: () => expire(c),
  });
  return c;
}

/** The session is gone for good (refresh refused, device removed, account disabled). */
function expire(c: HosbahClient): void {
  if (client !== c) return;
  client = null;
  if (retryTimer) clearTimeout(retryTimer);
  const keep = meta ? { ...meta } : null;
  store.clearSession();
  meta = keep ? { ...keep, teams: [], activeTeam: null } : null;
  events.emit("expired");
  emit();
}

async function loadAccount(c: HosbahClient): Promise<void> {
  const me = await c.me();
  const teams = me.teams.filter((t) => !t.suspended);
  const keep = meta?.activeTeam && teams.some((t) => t.slug === meta!.activeTeam) ? meta.activeTeam : (teams[0]?.slug ?? null);
  meta = { apiUrl: meta?.apiUrl ?? c.baseUrl, email: me.user.email, userId: me.user.id, activeTeam: keep, teams };
  offline = false;
  if (c.session) persist(c.session);
}

/** Refreshes the team list (a new membership, a role change). Offline is not an error. */
export async function refreshAccount(): Promise<void> {
  const c = client;
  if (!c) return;
  try {
    await loadAccount(c);
    emit();
  } catch (e) {
    if (e instanceof ApiError && e.isNetwork && !offline) {
      offline = true;
      emit();
    }
  }
}

/** On launch: resume the stored session. Offline at launch is not signed out. */
export async function restore(): Promise<void> {
  const s = store.loadSession();
  if (!s) return;
  const { secret: _drop, ...rest } = s.stored;
  meta = rest;
  if (!s.session || (!s.session.token && !s.session.refreshToken)) return;
  assertAllowedUrl(meta.apiUrl);
  const c = makeClient(meta.apiUrl, s.session);
  client = c;
  try {
    await loadAccount(c);
  } catch (e) {
    if (client !== c) return; // expire() already ran
    if (e instanceof ApiError && e.isNetwork) {
      offline = true;
      scheduleRetry(c);
    } else if (e instanceof ApiError && (e.status === 401 || e.status === 403)) {
      expire(c);
      return;
    } else {
      offline = true;
      scheduleRetry(c);
    }
  }
  emit();
}

function scheduleRetry(c: HosbahClient, delay = 5_000): void {
  if (retryTimer) clearTimeout(retryTimer);
  retryTimer = setTimeout(async () => {
    if (client !== c) return;
    try {
      await loadAccount(c);
      emit();
    } catch (e) {
      if (client !== c) return;
      if (e instanceof ApiError && (e.status === 401 || e.status === 403)) return expire(c);
      scheduleRetry(c, Math.min(delay * 2, 60_000));
    }
  }, delay);
}

let pending: { client: HosbahClient; apiUrl: string; email: string } | null = null;

async function finish(c: HosbahClient, apiUrl: string, email: string, locale: Locale): Promise<SignInResult> {
  client = c;
  meta = { apiUrl, email, userId: "", activeTeam: meta?.apiUrl === apiUrl && meta.email === email ? meta.activeTeam : null, teams: [] };
  // Once per sign-in: a device id + refresh token for this machine.
  await c.registerDevice({ name: hostname() || "Desktop", platform: platform(), app: "desktop", locale, app_version: app.getVersion() });
  await loadAccount(c);
  pending = null;
  emit();
  return { ok: true };
}

export async function signIn(apiUrlIn: string, email: string, password: string, locale: Locale): Promise<SignInResult> {
  const apiUrl = normalizeApiUrl(apiUrlIn);
  assertAllowedUrl(apiUrl);
  const c = makeClient(apiUrl, null);
  try {
    await c.login(email.trim(), password, locale);
  } catch (e) {
    const challenge = twoFactorChallenge(e);
    if (challenge) {
      pending = { client: c, apiUrl, email: email.trim() };
      return { ok: false, mfa: true, challenge };
    }
    throw e;
  }
  return finish(c, apiUrl, email.trim(), locale);
}

export async function submitTwoFactor(challenge: string, answer: { code?: string; recovery_code?: string }, locale: Locale): Promise<SignInResult> {
  if (!pending) throw new ApiError(0, "start signing in again", { code: "no_challenge" });
  const { client: c, apiUrl, email } = pending;
  await c.completeTwoFactor(challenge, answer);
  return finish(c, apiUrl, email, locale);
}

export async function signOut(): Promise<void> {
  const c = client;
  client = null;
  if (retryTimer) clearTimeout(retryTimer);
  if (c) await c.signOut().catch(() => undefined); // removes the device on the server; offline still signs out locally
  store.clearSession();
  meta = meta ? { ...meta, teams: [], activeTeam: null, userId: "" } : null;
  events.emit("signed-out");
  emit();
}

export function setActiveTeam(slug: string): void {
  if (!meta || !meta.teams.some((t) => t.slug === slug)) return;
  meta = { ...meta, activeTeam: slug };
  if (client?.session) persist(client.session);
  emit();
}
