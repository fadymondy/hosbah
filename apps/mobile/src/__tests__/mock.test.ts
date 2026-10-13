import { HosbahClient } from "@hosbah/client";

import { MOCK_TEAM_2_ID, MOCK_TEAM_ID, createMockFetch } from "@/lib/mock";

const make = () => {
  const m = createMockFetch({ latencyMs: 0 });
  return new HosbahClient({ baseUrl: "http://mock.test", fetch: m.fetch });
};

const reg = { name: "Pixel", platform: "android", app: "mobile", locale: "ar", app_version: "1.0.0" } as const;

describe("mock server", () => {
  it("signs in, registers a device and lists the demo team", async () => {
    const c = make();
    await c.login("me@example.com", "pw");
    await c.registerDevice(reg);
    expect(c.signedIn).toBe(true);
    const me = await c.me();
    expect(me.teams.map((t) => t.name)).toContain("Demo");
  });
  it("rejects wrong passwords and requires 2FA for 2fa accounts", async () => {
    const c = make();
    await expect(c.login("me@example.com", "wrong")).rejects.toMatchObject({ status: 401 });
    const r = await c.login("2fa@example.com", "pw").catch((e: unknown) => e);
    // Either the client returns a challenge result or throws 2fa_required; both mean "second step needed".
    expect(JSON.stringify(r)).toMatch(/2fa_required|mock-challenge|challenge/);
  });
  it("serves the fixture apps and blocks viewers from operating", async () => {
    const c = make();
    await c.login("me@example.com", "pw");
    await c.registerDevice(reg);
    const apps = await c.apps(MOCK_TEAM_ID);
    expect(apps.map((a) => a.name).sort()).toEqual(["blog", "shop-api", "status-page"]);
    await expect(c.restart(MOCK_TEAM_ID, "app-shop-api")).resolves.toBeTruthy();
    await expect(c.restart(MOCK_TEAM_2_ID, "app-shop-api")).rejects.toMatchObject({ status: 403 });
  });
  it("keeps private demo names out of the fixtures", async () => {
    const c = make();
    await c.login("me@example.com", "pw");
    await c.registerDevice(reg);
    const dump = JSON.stringify([await c.apps(MOCK_TEAM_ID), await c.me()]);
    expect(dump).not.toMatch(/elbash|elbasha|\brtx\b/i);
  });
});
