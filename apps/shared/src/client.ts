import { readSse, SseParser } from "./sse";
import type {
  App,
  AuthProvider,
  AuthProviderId,
  DeleteAccountProof,
  NativeSignIn,
  AppDetail,
  Backup,
  Certificate,
  DataService,
  Deployment,
  DeploymentDetail,
  DeploymentStreamEvent,
  Device,
  EventName,
  LoginResult,
  LogLine,
  Me,
  MetricsRange,
  MetricsSeries,
  NotificationPrefsResponse,
  PushRegistration,
  NotificationsResponse,
  PowerAction,
  RefreshResult,
  RegisterDeviceInput,
  Server,
  ServersResponse,
  Session,
  TeamDomain,
} from "./types";

/**
 * The live console. The apps let the user change it (Settings → Server URL); this is only the default.
 * Source: deploy/lxc/.env.prod.example (APP_URL / HOSBAH_WEB_ORIGIN = https://app.hosbah.com), where the
 * web dashboard and its /api proxy live. hosbah.com itself is the marketing site.
 */
export const DEFAULT_API_URL = "https://app.hosbah.com";

/**
 * How the login POSTs satisfy the auth plugin's double-submit CSRF check.
 * - "cookie-header": send a random value as both `Cookie: togo_csrf=` and `X-CSRF-Token` (Node, Electron
 *   main, React Native: none of them forbid the Cookie header).
 * - "browser": `Cookie` is forbidden in browsers, so GET /api/auth/csrf with credentials and echo the token.
 */
export type CsrfMode = "cookie-header" | "browser";

export interface ClientOptions {
  baseUrl: string;
  fetch?: typeof fetch;
  csrf?: CsrfMode;
  /** The persisted session, if any. */
  session?: Partial<Session> | null;
  /**
   * Called with every new session (persist it before returning: the refresh token rotates on every
   * refresh, and the old one is dead the moment the server answers) and with null on sign-out / revocation.
   */
  onSession?: (session: Session | null) => void | Promise<void>;
  /** Called when the session is gone for good and the user has to sign in again. */
  onAuthRequired?: () => void;
  /** Clock, for tests. */
  now?: () => number;
}

export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
    readonly body?: unknown,
  ) {
    super(message);
    this.name = "ApiError";
  }
  /** The server's machine code ({error, code}), e.g. "2fa_required", "account_disabled", "email_unverified". */
  get code(): string | undefined {
    const c = (this.body as { code?: unknown } | undefined)?.code;
    return typeof c === "string" ? c : undefined;
  }
  /** Status 0: the request never got an answer (offline, DNS, TLS, aborted). */
  get isNetwork(): boolean {
    return this.status === 0 && this.code === "network";
  }
}

/** The challenge to answer when a login came back 401 `2fa_required`, else undefined. */
export function twoFactorChallenge(err: unknown): string | undefined {
  if (!(err instanceof ApiError) || err.code !== "2fa_required") return undefined;
  const c = (err.body as { challenge?: unknown }).challenge;
  return typeof c === "string" ? c : undefined;
}

type Query = Record<string, string | number | boolean | undefined | null>;

interface RequestOpts {
  body?: unknown;
  query?: Query;
  signal?: AbortSignal;
  /** Internal: already retried after a refresh. */
  retried?: boolean;
  /** Internal: a 401 here is the answer, not a reason to refresh (sign-out). */
  noRefresh?: boolean;
}

function randomHex(bytes = 16): string {
  const a = new Uint8Array(bytes);
  globalThis.crypto.getRandomValues(a);
  return Array.from(a, (b) => b.toString(16).padStart(2, "0")).join("");
}

/** The `exp` claim of a JWT in ms, or null. The signature is not checked (the API does that). */
export function tokenExpiry(token: string): number | null {
  const part = token.split(".")[1];
  if (!part) return null;
  try {
    const json = atob(part.replace(/-/g, "+").replace(/_/g, "/").padEnd(Math.ceil(part.length / 4) * 4, "="));
    const exp = (JSON.parse(json) as { exp?: unknown }).exp;
    return typeof exp === "number" ? exp * 1000 : null;
  } catch {
    return null;
  }
}

/** Normalises a user-typed server URL: adds https://, drops trailing slashes and a trailing /api. */
export function normalizeApiUrl(input: string): string {
  let u = input.trim();
  if (!u) return DEFAULT_API_URL;
  if (!/^https?:\/\//i.test(u)) u = "https://" + u;
  const url = new URL(u);
  let path = url.pathname.replace(/\/+$/, "");
  if (path.endsWith("/api")) path = path.slice(0, -4);
  return url.origin + path;
}

const enc = encodeURIComponent;
const teamBase = (team: string) => `/api/teams/${enc(team)}`;

export interface StreamOptions {
  /** Called as the connection state changes. */
  onStatus?: (s: "open" | "reconnecting" | "closed") => void;
  /** Initial reconnect delay in ms (default 3000), doubled on errors up to maxDelay (default 60 000). */
  retryMs?: number;
  maxDelay?: number;
}

export interface StreamHandle {
  close(): void;
}

export class HosbahClient {
  readonly baseUrl: string;
  private readonly f: typeof fetch;
  private readonly csrf: CsrfMode;
  private readonly opts: ClientOptions;
  private refreshing: Promise<boolean> | null = null;
  token: string | null;
  refreshToken: string | null;
  deviceId: string | null;

  constructor(opts: ClientOptions) {
    this.opts = opts;
    this.baseUrl = opts.baseUrl.replace(/\/+$/, "");
    this.f = opts.fetch ?? ((...a: Parameters<typeof fetch>) => globalThis.fetch(...a));
    this.csrf = opts.csrf ?? "cookie-header";
    this.token = opts.session?.token ?? null;
    this.refreshToken = opts.session?.refreshToken ?? null;
    this.deviceId = opts.session?.deviceId ?? null;
  }

  private now(): number {
    return this.opts.now ? this.opts.now() : Date.now();
  }

  get session(): Session | null {
    if (!this.token && !this.refreshToken) return null;
    return { token: this.token ?? "", refreshToken: this.refreshToken, deviceId: this.deviceId };
  }

  get signedIn(): boolean {
    return !!(this.token || this.refreshToken);
  }

  private async setSession(s: Partial<Session> | null): Promise<void> {
    this.token = s?.token || null;
    this.refreshToken = s?.refreshToken ?? null;
    this.deviceId = s?.deviceId ?? null;
    await this.opts.onSession?.(s ? { token: this.token ?? "", refreshToken: this.refreshToken, deviceId: this.deviceId } : null);
  }

  // ── auth ────────────────────────────────────────────────────────────────

  private async csrfHeaders(): Promise<{ headers: Record<string, string>; credentials?: RequestCredentials }> {
    if (this.csrf === "cookie-header") {
      const v = randomHex();
      return { headers: { Cookie: `togo_csrf=${v}`, "X-CSRF-Token": v } };
    }
    const res = await this.fetchRaw(this.baseUrl + "/api/auth/csrf", { credentials: "include" });
    if (!res.ok) throw new ApiError(res.status, "could not get a CSRF token");
    const { csrf_token } = (await res.json()) as { csrf_token: string };
    return { headers: { "X-CSRF-Token": csrf_token }, credentials: "include" };
  }

  /** fetch, with transport failures turned into a recognisable ApiError (status 0, code "network"). */
  private async fetchRaw(url: string, init?: RequestInit): Promise<Response> {
    try {
      return await this.f(url, init);
    } catch (err) {
      if (init?.signal?.aborted) throw err;
      throw new ApiError(0, "network error", { code: "network", cause: String(err) });
    }
  }

  private async readBody(res: Response): Promise<unknown> {
    const text = await res.text();
    if (!text) return undefined;
    try {
      return JSON.parse(text);
    } catch {
      return text;
    }
  }

  private errorOf(res: Response, data: unknown): ApiError {
    const d = data as { detail?: string; title?: string; error?: string; errors?: { message?: string }[] } | undefined;
    const msg = (typeof d === "object" && d ? (d.error ?? d.detail ?? d.errors?.[0]?.message ?? d.title) : undefined) ?? `HTTP ${res.status}`;
    return new ApiError(res.status, msg, data);
  }

  private async credentialPost(path: string, body: unknown): Promise<LoginResult> {
    const c = await this.csrfHeaders();
    const res = await this.fetchRaw(this.baseUrl + path, {
      method: "POST",
      headers: { "Content-Type": "application/json", Accept: "application/json", ...c.headers },
      body: JSON.stringify(body),
      ...(c.credentials ? { credentials: c.credentials } : {}),
    });
    const data = (await this.readBody(res)) as Partial<LoginResult> & { error?: string } | undefined;
    if (!res.ok || !data || typeof data !== "object" || !data.token) throw this.errorOf(res, data);
    // A fresh sign-in: the old device session (if any) belongs to a previous sign-in, so start clean.
    await this.setSession({ token: data.token });
    return data as LoginResult;
  }

  /**
   * POST /api/auth/login. Throws ApiError with code "2fa_required" (use twoFactorChallenge(err) and then
   * completeTwoFactor), "account_disabled" (403) or "email_unverified" (403).
   */
  login(email: string, password: string, locale: string = "ar"): Promise<LoginResult> {
    return this.credentialPost("/api/auth/login", { email, password, locale });
  }

  /** POST /api/auth/2fa/challenge: answer with a TOTP `code` or a `recovery_code`. */
  completeTwoFactor(challenge: string, answer: { code?: string; recovery_code?: string }): Promise<LoginResult> {
    return this.credentialPost("/api/auth/2fa/challenge", { challenge, ...answer });
  }

  /** GET /api/auth/providers: the social sign-in buttons this server offers ([] when unreachable). */
  async authProviders(): Promise<AuthProvider[]> {
    try {
      const res = await this.fetchRaw(this.baseUrl + "/api/auth/providers", { headers: { Accept: "application/json" } });
      if (!res.ok) return [];
      const data = (await this.readBody(res)) as { providers?: AuthProvider[] } | undefined;
      return Array.isArray(data?.providers) ? data.providers : [];
    } catch {
      return [];
    }
  }

  /**
   * POST /api/auth/oauth/{provider}/native with what Google's or Apple's SDK returned. Answers like login():
   * the token, or ApiError 2fa_required / account_disabled / oauth_email_unverified / signup_closed / provider_error.
   */
  nativeSignIn(provider: "google" | "apple", body: NativeSignIn): Promise<LoginResult> {
    return this.credentialPost(`/api/auth/oauth/${provider}/native`, body);
  }

  /**
   * Where to open the system browser for a provider without a native SDK in the app (GitHub, Apple on
   * Android). The trip ends at hosbah://auth/callback?code=… (or ?error=…); redeem with exchangeAppCode.
   */
  oauthBrowserUrl(provider: AuthProviderId, codeChallenge: string, locale: string = "ar"): string {
    return this.url(`/api/auth/oauth/${enc(provider)}`, { app: "1", code_challenge: codeChallenge, code_challenge_method: "S256", locale });
  }

  /** POST /api/auth/app/token: the one-time code from the deep link plus the PKCE verifier. Answers like login(). */
  exchangeAppCode(code: string, codeVerifier: string): Promise<LoginResult> {
    return this.credentialPost("/api/auth/app/token", { code, code_verifier: codeVerifier });
  }

  /** POST /api/me/delete/code: mails a 6-digit code that confirms deleting the account. */
  async requestDeleteCode(locale: string = "ar"): Promise<void> {
    await this.request("POST", "/api/me/delete/code", { body: { locale } });
  }

  /**
   * DELETE /api/me with one proof (password, mailed code, or a fresh native sign-in). On success the
   * account is gone, so the local session is dropped. Throws ApiError reauth_required / invalid_password /
   * invalid_code / sole_owner / team_has_servers / last_admin.
   */
  async deleteAccount(proof: DeleteAccountProof): Promise<void> {
    await this.request("DELETE", "/api/me", { body: proof, noRefresh: false });
    await this.setSession(null);
  }

  /** POST /api/me/devices: call once after the first sign-in; stores the device id and refresh token. */
  async registerDevice(input: RegisterDeviceInput): Promise<Device> {
    const r = await this.request<{ device: Device; refresh_token: string }>("POST", "/api/me/devices", { body: input });
    await this.setSession({ ...(this.token ? { token: this.token } : {}), refreshToken: r.refresh_token, deviceId: r.device.id });
    return r.device;
  }

  /**
   * Exchanges the refresh token for a new JWT. De-duplicated: concurrent callers share one network call
   * (the token rotates, so a second call with the old one would fail). Resolves false when the session is
   * dead (onAuthRequired has fired); rejects with ApiError(code "network") when offline, keeping the
   * refresh token so a later attempt can succeed.
   */
  refresh(): Promise<boolean> {
    if (!this.refreshToken) return Promise.resolve(false);
    this.refreshing ??= this.doRefresh().finally(() => {
      this.refreshing = null;
    });
    return this.refreshing;
  }

  private async doRefresh(): Promise<boolean> {
    const c = await this.csrfHeaders();
    const res = await this.fetchRaw(this.baseUrl + "/api/auth/device/refresh", {
      method: "POST",
      headers: { "Content-Type": "application/json", Accept: "application/json", ...c.headers },
      body: JSON.stringify({ refresh_token: this.refreshToken }),
      ...(c.credentials ? { credentials: c.credentials } : {}),
    });
    const data = (await this.readBody(res)) as Partial<RefreshResult> | undefined;
    if (res.ok && data && data.token && data.refresh_token) {
      await this.setSession({ token: data.token, refreshToken: data.refresh_token, deviceId: data.device_id ?? this.deviceId });
      return true;
    }
    if (res.status >= 500 || res.status === 429) throw this.errorOf(res, data); // transient: keep the token
    // invalid_refresh | account_disabled | session_revoked (401), or anything else the server refused for good.
    await this.setSession(null);
    this.opts.onAuthRequired?.();
    return false;
  }

  /** Removes this device on the server (its refresh and push tokens), then forgets the session locally. */
  async signOut(): Promise<void> {
    if (this.deviceId && this.token) {
      try {
        await this.request("DELETE", `/api/me/devices/${enc(this.deviceId)}`, { noRefresh: true });
      } catch {
        // Offline or already revoked: still sign out locally. The device row can be removed from another device.
      }
    }
    await this.setSession(null);
  }

  /** Refresh ahead of time when the JWT expires within a minute. */
  private async ensureFresh(): Promise<void> {
    if (!this.token || !this.refreshToken) return;
    const exp = tokenExpiry(this.token);
    if (exp !== null && exp - this.now() < 60_000) await this.refresh().catch(() => false);
  }

  // ── transport ───────────────────────────────────────────────────────────

  private url(path: string, query?: Query): string {
    const u = new URL(this.baseUrl + path);
    for (const [k, v] of Object.entries(query ?? {})) {
      if (v !== undefined && v !== null && v !== "") u.searchParams.set(k, String(v));
    }
    return u.toString();
  }

  private authHeaders(json: boolean, extra: Record<string, string> = {}): Record<string, string> {
    const h: Record<string, string> = { Accept: "application/json", ...extra };
    if (json) h["Content-Type"] = "application/json";
    if (this.token) h.Authorization = `Bearer ${this.token}`;
    return h;
  }

  /** Makes sure there is a usable token, refreshing if needed. False when the user must sign in again. */
  private async ready(): Promise<boolean> {
    await this.ensureFresh();
    if (this.token) return true;
    if (!this.refreshToken) {
      this.opts.onAuthRequired?.();
      return false;
    }
    return this.refresh(); // false => refresh() already fired onAuthRequired
  }

  /** Sends one request. On 401 it refreshes once (shared) and retries; if that fails, onAuthRequired fires. */
  async request<T>(method: string, path: string, opts: RequestOpts = {}): Promise<T> {
    if (!opts.noRefresh && !(await this.ready())) throw new ApiError(401, "sign in required");
    const json = opts.body !== undefined;
    const res = await this.fetchRaw(this.url(path, opts.query), {
      method,
      headers: this.authHeaders(json),
      ...(json ? { body: JSON.stringify(opts.body) } : {}),
      ...(opts.signal ? { signal: opts.signal } : {}),
    });
    if (res.status === 401 && !opts.noRefresh) {
      if (!opts.retried && this.refreshToken) {
        if (await this.refresh()) return this.request<T>(method, path, { ...opts, retried: true });
        throw new ApiError(401, "session expired: sign in again");
      }
      await this.setSession(null);
      this.opts.onAuthRequired?.();
      throw new ApiError(401, "session expired: sign in again");
    }
    if (res.status === 204) return undefined as T;
    const data = await this.readBody(res);
    if (!res.ok) throw this.errorOf(res, data);
    return data as T;
  }

  // ── identity ────────────────────────────────────────────────────────────

  me(): Promise<Me> {
    return this.request<Me>("GET", "/api/me");
  }

  // ── servers ─────────────────────────────────────────────────────────────

  servers(team: string): Promise<ServersResponse> {
    return this.request<ServersResponse>("GET", `${teamBase(team)}/servers`);
  }

  server(team: string, server: string): Promise<Server> {
    return this.request<Server>("GET", `${teamBase(team)}/servers/${enc(server)}`);
  }

  serverMetrics(team: string, server: string, range: MetricsRange = "1h"): Promise<MetricsSeries> {
    return this.request<MetricsSeries>("GET", `${teamBase(team)}/servers/${enc(server)}/metrics`, { query: { range } });
  }

  // ── apps ────────────────────────────────────────────────────────────────

  async apps(team: string): Promise<App[]> {
    return (await this.request<{ apps: App[] }>("GET", `${teamBase(team)}/apps`)).apps;
  }

  app(team: string, app: string): Promise<AppDetail> {
    return this.request<AppDetail>("GET", `${teamBase(team)}/apps/${enc(app)}`);
  }

  async deployments(team: string, app: string, limit = 30): Promise<Deployment[]> {
    const r = await this.request<{ deployments: Deployment[] }>("GET", `${teamBase(team)}/apps/${enc(app)}/deployments`, { query: { limit } });
    return r.deployments;
  }

  deployment(team: string, app: string, deployment: string): Promise<DeploymentDetail> {
    return this.request<DeploymentDetail>("GET", `${teamBase(team)}/apps/${enc(app)}/deployments/${enc(deployment)}`);
  }

  /** Redeploy the app's branch. 201 with the queued Deployment. Needs owner | admin | deployer. */
  redeploy(team: string, app: string, ref?: string): Promise<Deployment> {
    return this.request<Deployment>("POST", `${teamBase(team)}/apps/${enc(app)}/deployments`, { body: ref ? { ref } : {} });
  }

  /** Restart the live release in fresh containers. 201 with a Deployment of kind "restart". */
  restart(team: string, app: string): Promise<Deployment> {
    return this.request<Deployment>("POST", `${teamBase(team)}/apps/${enc(app)}/restart`);
  }

  /** Start or stop the app's containers. 202: an operation is queued on the server's agent. */
  power(team: string, app: string, action: PowerAction): Promise<unknown> {
    return this.request("POST", `${teamBase(team)}/apps/${enc(app)}/power`, { body: { action } });
  }

  // ── domains / certificates / backups ────────────────────────────────────

  async domains(team: string): Promise<TeamDomain[]> {
    return (await this.request<{ domains: TeamDomain[] }>("GET", `${teamBase(team)}/domains`)).domains;
  }

  async certificates(team: string): Promise<Certificate[]> {
    return (await this.request<{ certificates: Certificate[] }>("GET", `${teamBase(team)}/certificates`)).certificates;
  }

  /** Each service carries `backups` (last status, schedule) in the team list. */
  async dataServices(team: string): Promise<DataService[]> {
    return (await this.request<{ services: DataService[] }>("GET", `${teamBase(team)}/data-services`)).services;
  }

  async backups(team: string, service: string): Promise<Backup[]> {
    return (await this.request<{ backups: Backup[] }>("GET", `${teamBase(team)}/data-services/${enc(service)}/backups`)).backups;
  }

  // ── notifications ───────────────────────────────────────────────────────

  notifications(team: string): Promise<NotificationsResponse> {
    return this.request<NotificationsResponse>("GET", `${teamBase(team)}/notifications`);
  }

  markRead(team: string, id: string): Promise<void> {
    return this.request<void>("POST", `${teamBase(team)}/notifications/${enc(id)}/read`);
  }

  markAllRead(team: string): Promise<void> {
    return this.request<void>("POST", `${teamBase(team)}/notifications/read`);
  }

  // ── notification preferences / devices / push ──────────────────────────

  /** GET /api/me/notification-prefs: `{events, push_available}` (push_available is false when the server has no FCM). */
  prefs(): Promise<NotificationPrefsResponse> {
    return this.request<NotificationPrefsResponse>("GET", "/api/me/notification-prefs");
  }

  /** PUT /api/me/notification-prefs: takes a partial `events` map, answers with the full response. */
  putPrefs(events: Partial<Record<EventName, boolean>>): Promise<NotificationPrefsResponse> {
    return this.request<NotificationPrefsResponse>("PUT", "/api/me/notification-prefs", { body: { events } });
  }

  async devices(): Promise<Device[]> {
    return (await this.request<{ devices: Device[] }>("GET", "/api/me/devices")).devices;
  }

  /** Signs that device out. Deleting this device's own id is signOut()'s job; this one keeps the local session. */
  deleteDevice(id: string): Promise<void> {
    return this.request<void>("DELETE", `/api/me/devices/${enc(id)}`);
  }

  /** PUT /api/me/devices/{id}/push (204): registers the FCM token; "" clears it. Defaults to this client's device. */
  setPushToken(token: string, reg: PushRegistration = {}, deviceId: string | null = this.deviceId): Promise<void> {
    if (!deviceId) return Promise.reject(new ApiError(0, "this device is not registered", { code: "no_device" }));
    return this.request<void>("PUT", `/api/me/devices/${enc(deviceId)}/push`, { body: { token, ...reg } });
  }

  /** POST /api/me/devices/{id}/test: `{sent}`. ApiError 503 when the server has no FCM, 409 when the device has no push token. */
  testPush(deviceId: string | null = this.deviceId): Promise<{ sent: number }> {
    if (!deviceId) return Promise.reject(new ApiError(0, "this device is not registered", { code: "no_device" }));
    return this.request<{ sent: number }>("POST", `/api/me/devices/${enc(deviceId)}/test`);
  }

  // ── SSE ─────────────────────────────────────────────────────────────────

  /**
   * Opens a team SSE endpoint once and feeds every event to `onEvent` until the stream ends or `signal`
   * aborts. EventSource cannot send Authorization, hence fetch + SseParser. Returns the server's retry hint.
   * A 401 refreshes once and reopens.
   */
  async streamOnce(path: string, onEvent: (name: string, data: string) => void, signal?: AbortSignal, query?: Query): Promise<number | undefined> {
    if (!(await this.ready())) throw new ApiError(401, "sign in required");
    const open = () =>
      this.fetchRaw(this.url(path, query), {
        headers: this.authHeaders(false, { Accept: "text/event-stream" }),
        ...(signal ? { signal } : {}),
      });
    let res = await open();
    if (res.status === 401) {
      if (this.refreshToken && (await this.refresh())) res = await open();
      else {
        await this.setSession(null);
        this.opts.onAuthRequired?.();
      }
    }
    if (res.status === 401) throw new ApiError(401, "session expired: sign in again");
    if (!res.ok || !res.body) throw this.errorOf(res, undefined);
    const parser = new SseParser();
    for await (const ev of readSse(res.body, parser)) onEvent(ev.event, ev.data);
    return parser.retry;
  }

  /** A reconnecting `streamOnce` (backoff 3 s doubling to 60 s). `run` returns false to stop for good. */
  private subscribe(run: (signal: AbortSignal) => Promise<boolean | number | undefined>, o: StreamOptions): StreamHandle {
    let ctrl: AbortController | null = null;
    let closed = false;
    let delay = o.retryMs ?? 3000;
    const base = delay;
    let timer: ReturnType<typeof setTimeout> | null = null;
    const loop = async () => {
      if (closed) return;
      ctrl = new AbortController();
      try {
        o.onStatus?.("open");
        const r = await run(ctrl.signal);
        if (r === false) {
          closed = true;
          o.onStatus?.("closed");
          return;
        }
        delay = typeof r === "number" ? Math.max(r, 1000) : base;
      } catch (err) {
        if (closed) return;
        if (err instanceof ApiError && err.status === 401) {
          closed = true;
          o.onStatus?.("closed");
          return;
        }
        delay = Math.min(delay * 2, o.maxDelay ?? 60_000);
      }
      if (closed) return;
      o.onStatus?.("reconnecting");
      timer = setTimeout(loop, delay);
    };
    void loop();
    return {
      close() {
        closed = true;
        if (timer) clearTimeout(timer);
        ctrl?.abort();
        o.onStatus?.("closed");
      },
    };
  }

  /** GET …/notifications/stream: `onChange` gets the full list + unread count each time it changes. */
  streamNotifications(team: string, onChange: (r: NotificationsResponse) => void, o: StreamOptions = {}): StreamHandle {
    return this.subscribe(
      (signal) =>
        this.streamOnce(
          `${teamBase(team)}/notifications/stream`,
          (name, data) => {
            if (name !== "notifications") return;
            const parsed = parseJson<NotificationsResponse>(data);
            if (parsed) onChange(parsed);
          },
          signal,
        ),
      o,
    );
  }

  /**
   * GET …/deployments/{d}/stream: events line, status, done (and gone when the deployment vanished).
   * Resumes after the last line seen on reconnect; stops by itself on done/gone.
   */
  streamDeployment(team: string, app: string, deployment: string, onEvent: (e: DeploymentStreamEvent) => void, o: StreamOptions = {}): StreamHandle {
    let after = 0;
    let finished = false;
    return this.subscribe(async (signal) => {
      const retry = await this.streamOnce(
        `${teamBase(team)}/apps/${enc(app)}/deployments/${enc(deployment)}/stream`,
        (name, data) => {
          const e = toDeploymentEvent(name, data);
          if (!e) return;
          if (e.type === "line") after = Math.max(after, e.line.seq);
          if (e.type === "done" || e.type === "gone") finished = true;
          onEvent(e);
        },
        signal,
        { after: after || undefined },
      );
      return finished ? false : retry;
    }, o);
  }
}

function parseJson<T>(raw: string): T | null {
  try {
    return JSON.parse(raw) as T;
  } catch {
    return null;
  }
}

/** Maps a raw SSE event from the deployment stream to a typed event (null for anything unknown). */
export function toDeploymentEvent(name: string, raw: string): DeploymentStreamEvent | null {
  switch (name) {
    case "line": {
      const line = parseJson<LogLine>(raw);
      return line ? { type: "line", line } : null;
    }
    case "status": {
      const deployment = parseJson<Deployment>(raw);
      return deployment ? { type: "status", deployment } : null;
    }
    case "done": {
      const d = parseJson<{ status: Deployment["status"] }>(raw);
      return d ? { type: "done", status: d.status } : null;
    }
    case "gone":
      return { type: "gone" };
    default:
      return null;
  }
}
