import { describe, expect, it, vi } from "vitest";
import { ApiError, HosbahClient, normalizeApiUrl, tokenExpiry, twoFactorChallenge } from "../client";
import type { Session } from "../types";

const NOW = Date.parse("2026-10-10T12:00:00Z");
const jwt = (expMs: number) => {
  const b = (o: object) => btoa(JSON.stringify(o)).replace(/=+$/, "");
  return `${b({ alg: "none" })}.${b({ exp: Math.floor(expMs / 1000) })}.sig`;
};
const fresh = () => jwt(NOW + 3_600_000);
const json = (status: number, body?: unknown) =>
  new Response(body === undefined ? null : JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

interface Call { url: URL; init: RequestInit; headers: Record<string, string> }
function harness(handler: (c: Call, n: number) => Response | Promise<Response>) {
  const calls: Call[] = [];
  const f = (async (input: string | URL | Request, init: RequestInit = {}) => {
    const call: Call = { url: new URL(String(input)), init, headers: { ...(init.headers as Record<string, string>) } };
    calls.push(call);
    return handler(call, calls.length);
  }) as typeof fetch;
  return { calls, f };
}

function make(h: ReturnType<typeof harness>, session: Partial<Session> | null = null) {
  const sessions: (Session | null)[] = [];
  const onAuthRequired = vi.fn();
  const client = new HosbahClient({
    baseUrl: "https://h.test/",
    fetch: h.f,
    session,
    now: () => NOW,
    onSession: (s) => void sessions.push(s),
    onAuthRequired,
  });
  return { client, sessions, onAuthRequired };
}

describe("helpers", () => {
  it("tokenExpiry", () => {
    expect(tokenExpiry(jwt(NOW))).toBe(Math.floor(NOW / 1000) * 1000);
    expect(tokenExpiry("garbage")).toBeNull();
  });
  it("normalizeApiUrl", () => {
    expect(normalizeApiUrl("app.hosbah.com/")).toBe("https://app.hosbah.com");
    expect(normalizeApiUrl("http://x.test:8710/api")).toBe("http://x.test:8710");
    expect(normalizeApiUrl("")).toBe("https://app.hosbah.com");
  });
});

describe("login", () => {
  it("sends the CSRF pair and stores the token", async () => {
    const tok = fresh();
    const h = harness(() => json(200, { token: tok, user: { id: "u", email: "a@b.c" } }));
    const { client, sessions } = make(h);
    await client.login("a@b.c", "pw");
    const c = h.calls[0]!;
    expect(c.url.pathname).toBe("/api/auth/login");
    expect(JSON.parse(String(c.init.body))).toEqual({ email: "a@b.c", password: "pw", locale: "ar" });
    const csrf = c.headers["X-CSRF-Token"];
    expect(csrf).toBeTruthy();
    expect(c.headers.Cookie).toBe(`togo_csrf=${csrf}`);
    expect(client.token).toBe(tok);
    expect(sessions.at(-1)?.token).toBe(tok);
  });

  it("surfaces 2fa_required with its challenge, then completes", async () => {
    const tok = fresh();
    const h = harness((c) =>
      c.url.pathname.endsWith("/login")
        ? json(401, { error: "two-factor required", code: "2fa_required", challenge: "ch1" })
        : json(200, { token: tok, user: { id: "u", email: "e" } }),
    );
    const { client } = make(h);
    const err = await client.login("a", "b").catch((e) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err.status).toBe(401);
    expect(twoFactorChallenge(err)).toBe("ch1");
    await client.completeTwoFactor("ch1", { code: "123456" });
    expect(h.calls[1]!.url.pathname).toBe("/api/auth/2fa/challenge");
    expect(JSON.parse(String(h.calls[1]!.init.body))).toEqual({ challenge: "ch1", code: "123456" });
    expect(client.token).toBe(tok);
  });

  it("maps account_disabled and email_unverified", async () => {
    for (const code of ["account_disabled", "email_unverified"]) {
      const h = harness(() => json(403, { error: code, code }));
      const err = await make(h).client.login("a", "b").catch((e) => e);
      expect(err.status).toBe(403);
      expect(err.code).toBe(code);
    }
  });

  it("reports offline as a network ApiError", async () => {
    const h = harness(() => {
      throw new TypeError("fetch failed");
    });
    const err = await make(h).client.login("a", "b").catch((e) => e);
    expect(err.isNetwork).toBe(true);
  });
});

describe("devices and refresh", () => {
  it("registerDevice stores the refresh token and device id", async () => {
    const h = harness(() => json(201, { device: { id: "d1" }, refresh_token: "hbr_1" }));
    const { client, sessions } = make(h, { token: fresh() });
    await client.registerDevice({ name: "Pixel", platform: "android", app: "mobile" });
    expect(h.calls[0]!.headers.Authorization).toMatch(/^Bearer /);
    expect(client.refreshToken).toBe("hbr_1");
    expect(client.deviceId).toBe("d1");
    expect(sessions.at(-1)).toMatchObject({ refreshToken: "hbr_1", deviceId: "d1" });
  });

  it("refreshes ahead when the JWT is about to expire, and persists the rotated token first", async () => {
    const newTok = fresh();
    const order: string[] = [];
    const h = harness((c) => {
      if (c.url.pathname === "/api/auth/device/refresh") {
        expect(JSON.parse(String(c.init.body))).toEqual({ refresh_token: "hbr_old" });
        return json(200, { token: newTok, refresh_token: "hbr_new", device_id: "d1" });
      }
      order.push("data:" + c.headers.Authorization);
      return json(200, { servers: [], agent_version: "1" });
    });
    const sessions: (Session | null)[] = [];
    const client = new HosbahClient({
      baseUrl: "https://h.test",
      fetch: h.f,
      now: () => NOW,
      session: { token: jwt(NOW + 10_000), refreshToken: "hbr_old", deviceId: "d1" },
      onSession: (s) => void (sessions.push(s), order.push("persist:" + s?.refreshToken)),
    });
    await client.servers("t");
    expect(order).toEqual(["persist:hbr_new", `data:Bearer ${newTok}`]);
  });

  it("retries once after a 401 and de-duplicates concurrent refreshes", async () => {
    const newTok = fresh();
    let refreshes = 0;
    const h = harness((c) => {
      if (c.url.pathname === "/api/auth/device/refresh") {
        refreshes++;
        return json(200, { token: newTok, refresh_token: "hbr_2", device_id: "d1" });
      }
      return c.headers.Authorization === `Bearer ${newTok}` ? json(200, { ok: true }) : json(401, { error: "expired" });
    });
    const { client } = make(h, { token: jwt(NOW + 1_800_000), refreshToken: "hbr_1", deviceId: "d1" });
    const [a, b] = await Promise.all([client.request("GET", "/api/x"), client.request("GET", "/api/y")]);
    expect(a).toEqual({ ok: true });
    expect(b).toEqual({ ok: true });
    expect(refreshes).toBe(1);
  });

  it("a dead refresh token clears the session and asks for sign-in", async () => {
    const h = harness((c) => (c.url.pathname.endsWith("/refresh") ? json(401, { code: "invalid_refresh" }) : json(401, {})));
    const { client, sessions, onAuthRequired } = make(h, { token: fresh(), refreshToken: "hbr_x", deviceId: "d" });
    await expect(client.me()).rejects.toBeInstanceOf(ApiError);
    expect(onAuthRequired).toHaveBeenCalled();
    expect(sessions.at(-1)).toBeNull();
    expect(client.signedIn).toBe(false);
  });

  it("an offline refresh keeps the refresh token", async () => {
    const h = harness((c) => {
      if (c.url.pathname.endsWith("/refresh")) throw new TypeError("offline");
      return json(401, {});
    });
    const { client, onAuthRequired } = make(h, { token: fresh(), refreshToken: "hbr_keep", deviceId: "d" });
    const err = await client.me().catch((e) => e);
    expect(err.isNetwork).toBe(true);
    expect(client.refreshToken).toBe("hbr_keep");
    expect(onAuthRequired).not.toHaveBeenCalled();
  });

  it("signOut deletes this device then clears locally, even if offline", async () => {
    const h = harness(() => json(204));
    const { client, sessions } = make(h, { token: fresh(), refreshToken: "r", deviceId: "d9" });
    await client.signOut();
    expect(h.calls[0]!.init.method).toBe("DELETE");
    expect(h.calls[0]!.url.pathname).toBe("/api/me/devices/d9");
    expect(sessions.at(-1)).toBeNull();

    const off = harness(() => {
      throw new TypeError("offline");
    });
    const m = make(off, { token: fresh(), refreshToken: "r", deviceId: "d9" });
    await m.client.signOut();
    expect(m.client.signedIn).toBe(false);
  });
});

describe("data methods", () => {
  it("unwraps lists and builds team paths", async () => {
    const h = harness((c) => {
      const p = c.url.pathname;
      if (p.endsWith("/apps")) return json(200, { apps: [{ id: "a" }] });
      if (p.endsWith("/deployments")) return json(200, { deployments: [{ id: "d" }] });
      if (p.endsWith("/certificates")) return json(200, { certificates: [{ id: "c" }] });
      if (p.endsWith("/restart") || p.endsWith("/deployments")) return json(201, { id: "d2" });
      return json(200, {});
    });
    const { client } = make(h, { token: fresh() });
    expect(await client.apps("my team")).toEqual([{ id: "a" }]);
    expect(h.calls[0]!.url.pathname).toBe("/api/teams/my%20team/apps");
    expect(await client.deployments("t", "a", 5)).toEqual([{ id: "d" }]);
    expect(h.calls[1]!.url.searchParams.get("limit")).toBe("5");
    expect(await client.certificates("t")).toEqual([{ id: "c" }]);
  });

  it("restart posts and returns the deployment", async () => {
    const h = harness(() => json(201, { id: "d2" }));
    const { client } = make(h, { token: fresh() });
    expect(await client.restart("t", "a")).toEqual({ id: "d2" });
    expect(h.calls[0]!.init.method).toBe("POST");
    expect(h.calls[0]!.url.pathname).toBe("/api/teams/t/apps/a/restart");
  });

  it("markAllRead tolerates 204", async () => {
    const h = harness(() => json(204));
    const { client } = make(h, { token: fresh() });
    await expect(client.markAllRead("t")).resolves.toBeUndefined();
    expect(h.calls[0]!.url.pathname).toBe("/api/teams/t/notifications/read");
  });

  it("sign-in required without any session", async () => {
    const h = harness(() => json(200, {}));
    const { client, onAuthRequired } = make(h);
    await expect(client.me()).rejects.toMatchObject({ status: 401 });
    expect(onAuthRequired).toHaveBeenCalled();
    expect(h.calls).toHaveLength(0);
  });
});

describe("notification prefs and push registration", () => {
  const session = { token: fresh(), refreshToken: "r", deviceId: "dev-1" };

  it("prefs() returns events and push_available; putPrefs sends a partial {events} map", async () => {
    const events = { "deploy.failed": true };
    const h = harness((c) => json(200, { events, push_available: false, seen: c.init.method }));
    const { client } = make(h, session);
    const r = await client.prefs();
    expect(r.push_available).toBe(false);
    await client.putPrefs({ "deploy.failed": false });
    const put = h.calls[1]!;
    expect(put.url.pathname).toBe("/api/me/notification-prefs");
    expect(put.init.method).toBe("PUT");
    expect(JSON.parse(String(put.init.body))).toEqual({ events: { "deploy.failed": false } });
  });

  it("setPushToken PUTs {token, locale, app_version} for this device (204)", async () => {
    const h = harness(() => json(204));
    const { client } = make(h, session);
    await client.setPushToken("fcm-token", { locale: "ar", app_version: "1.2.3" });
    const c = h.calls[0]!;
    expect(c.url.pathname).toBe("/api/me/devices/dev-1/push");
    expect(c.init.method).toBe("PUT");
    expect(JSON.parse(String(c.init.body))).toEqual({ token: "fcm-token", locale: "ar", app_version: "1.2.3" });
  });

  it("setPushToken / testPush reject before the device is registered", async () => {
    const { client } = make(harness(() => json(204)), { token: fresh(), refreshToken: null, deviceId: null });
    await expect(client.setPushToken("t")).rejects.toBeInstanceOf(ApiError);
    await expect(client.testPush()).rejects.toBeInstanceOf(ApiError);
  });

  it("testPush returns {sent}; 503 and 409 surface as ApiError statuses", async () => {
    let status = 200;
    const h = harness(() => json(status, status === 200 ? { sent: 1 } : { error: "x" }));
    const { client } = make(h, session);
    expect(await client.testPush()).toEqual({ sent: 1 });
    expect(h.calls[0]!.url.pathname).toBe("/api/me/devices/dev-1/test");
    status = 503;
    await expect(client.testPush()).rejects.toMatchObject({ status: 503 });
    status = 409;
    await expect(client.testPush()).rejects.toMatchObject({ status: 409 });
  });
});

describe("social sign-in and account deletion", () => {
  it("lists providers, and [] when the server is unreachable", async () => {
    const providers = [{ id: "google", name: "Google", web: true, native: true, client_id: "web.apps" }];
    expect(await make(harness(() => json(200, { providers }))).client.authProviders()).toEqual(providers);
    expect(await make(harness(() => { throw new TypeError("offline"); })).client.authProviders()).toEqual([]);
  });

  it("signs in with a native token like a login, CSRF pair included", async () => {
    const tok = fresh();
    const h = harness(() => json(200, { token: tok, user: { id: "u", email: "e" } }));
    const { client } = make(h);
    await client.nativeSignIn("apple", { id_token: "idt", authorization_code: "ac", nonce: "n", full_name: { given_name: "A" } });
    const c = h.calls[0]!;
    expect(c.url.pathname).toBe("/api/auth/oauth/apple/native");
    expect(JSON.parse(String(c.init.body))).toEqual({ id_token: "idt", authorization_code: "ac", nonce: "n", full_name: { given_name: "A" } });
    expect(c.headers["X-CSRF-Token"]).toBeTruthy();
    expect(client.token).toBe(tok);
  });

  it("surfaces a native 2FA challenge", async () => {
    const h = harness(() => json(401, { code: "2fa_required", challenge: "ch", error: "x" }));
    const err = await make(h).client.nativeSignIn("google", { id_token: "t" }).catch((e) => e);
    expect(twoFactorChallenge(err)).toBe("ch");
  });

  it("builds the browser URL and redeems the app code", async () => {
    const tok = fresh();
    const h = harness(() => json(200, { token: tok, user: { id: "u", email: "e" } }));
    const { client } = make(h);
    const u = new URL(client.oauthBrowserUrl("github", "chal", "en"));
    expect(u.origin + u.pathname).toBe("https://h.test/api/auth/oauth/github");
    expect(Object.fromEntries(u.searchParams)).toEqual({ app: "1", code_challenge: "chal", code_challenge_method: "S256", locale: "en" });
    await client.exchangeAppCode("code1", "verifier");
    expect(h.calls[0]!.url.pathname).toBe("/api/auth/app/token");
    expect(JSON.parse(String(h.calls[0]!.init.body))).toEqual({ code: "code1", code_verifier: "verifier" });
    expect(client.token).toBe(tok);
  });

  it("deletes the account with a proof and forgets the session", async () => {
    const h = harness(() => json(200, { status: "deleted" }));
    const { client, sessions } = make(h, { token: fresh(), refreshToken: "hbr_x", deviceId: "d1" });
    await client.deleteAccount({ password: "pw" });
    expect(h.calls[0]!.init.method).toBe("DELETE");
    expect(h.calls[0]!.url.pathname).toBe("/api/me");
    expect(JSON.parse(String(h.calls[0]!.init.body))).toEqual({ password: "pw" });
    expect(client.signedIn).toBe(false);
    expect(sessions.at(-1)).toBeNull();
  });

  it("keeps the session when deletion is refused", async () => {
    const h = harness(() => json(409, { code: "sole_owner", error: "only owner" }));
    const { client } = make(h, { token: fresh(), refreshToken: "hbr_x", deviceId: "d1" });
    const err = await client.deleteAccount({ code: "123456" }).catch((e) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err.code).toBe("sole_owner");
    expect(client.signedIn).toBe(true);
  });
});
