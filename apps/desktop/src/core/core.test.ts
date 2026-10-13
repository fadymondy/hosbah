import { describe, expect, it } from "vitest";
import type { App, Server } from "@hosbah/client";
import { computeTray, activeAlerts, type TeamAlert, type TeamSnapshot } from "./trayStatus";
import { findDeepLinkArg, hashForUrl, linkToHash, routeForNotification } from "./route";
import { encodePng, statusIconPng, statusIconRgba } from "./png";

const GB = 1024 ** 3;
const server = (over: Partial<Server> = {}): Server =>
  ({
    id: "s1",
    name: "edge-1",
    status: "enrolled",
    online: true,
    metrics: { cpu_percent: 20, mem_used_bytes: 2 * GB, mem_total_bytes: 8 * GB, disk_used_bytes: 10 * GB, disk_total_bytes: 100 * GB, uptime_seconds: 1, load1: 0, at: "" },
    ...over,
  }) as Server;
const app = (over: Partial<App> = {}): App => ({ id: "a1", name: "shop", status: "running", imported_state: null, latest_deployment: null, ...over }) as App;
const snap = (over: Partial<TeamSnapshot> = {}): TeamSnapshot => ({ team: "acme", servers: [server()], apps: [app()], certificates: [], services: [], ...over });
const alert = (over: Partial<TeamAlert> = {}): TeamAlert => ({
  id: "n1",
  team: "acme",
  kind: "app.down",
  title: "shop is down",
  body: null,
  subject_type: "app",
  subject_id: "a1",
  read_at: null,
  created_at: "2026-10-13T10:00:00Z",
  ...over,
});

describe("computeTray", () => {
  it("is green when everything is fine and nothing is unread", () => {
    expect(computeTray([snap()], [])).toEqual({ level: "green", unread: 0, reasons: {} });
  });

  it("is amber for a degraded app, a hot server, or an expiring-cert alert", () => {
    expect(computeTray([snap({ apps: [app({ status: "stopped" })] })], []).level).toBe("amber");
    const hot = server({ metrics: { ...server().metrics!, cpu_percent: 95 } });
    expect(computeTray([snap({ servers: [hot] })], []).level).toBe("amber");
    expect(computeTray([snap()], [alert({ kind: "cert.expiring", subject_type: "certificate" })]).level).toBe("amber");
  });

  it("is red for a failed app, an offline server, or an unread down/failed alert", () => {
    expect(computeTray([snap({ apps: [app({ status: "failed" })] })], []).level).toBe("red");
    expect(computeTray([snap({ servers: [server({ online: false })] })], []).level).toBe("red");
    expect(computeTray([snap()], [alert({ kind: "deploy.failed", subject_type: "deployment" })]).level).toBe("red");
    expect(computeTray([snap()], [alert({ kind: "server.offline", subject_type: "server" })]).level).toBe("red");
  });

  it("takes the worst across teams and counts unread alerts of every kind", () => {
    const r = computeTray(
      [snap(), snap({ team: "b", apps: [app({ status: "stopped" })] })],
      [alert({ kind: "deploy.succeeded", id: "x" }), alert({ id: "y", read_at: "2026-10-13T11:00:00Z" })],
    );
    expect(r.level).toBe("amber");
    expect(r.unread).toBe(1);
  });

  it("ignores read alerts, and an app.up after app.down cancels it", () => {
    expect(computeTray([snap()], [alert({ read_at: "2026-10-13T10:05:00Z" })]).level).toBe("green");
    const recovered = [alert(), alert({ id: "n2", kind: "app.up", created_at: "2026-10-13T10:10:00Z" })];
    expect(activeAlerts(recovered).map((a) => a.kind)).toEqual([]);
    expect(computeTray([snap()], recovered).level).toBe("green");
    // a later outage of the same app is red again
    const again = [...recovered, alert({ id: "n3", created_at: "2026-10-13T10:20:00Z" })];
    expect(computeTray([snap()], again).level).toBe("red");
  });
});

describe("routeForNotification", () => {
  const apps = [
    { id: "a1", name: "shop" },
    { id: "a2", name: "api-gateway" },
  ];
  const n = (kind: string, subject_type: string | null, subject_id: string | null, title = "") => ({ kind, title, subject_type, subject_id });

  it("maps app and server subjects to their screens", () => {
    expect(routeForNotification("acme", n("app.down", "app", "a1"))).toEqual({ team: "acme", screen: "app", id: "a1" });
    expect(routeForNotification("acme", n("server.offline", "server", "s1"))).toEqual({ team: "acme", screen: "server", id: "s1" });
    expect(routeForNotification("acme", n("resource.high", "server", "s1"))).toEqual({ team: "acme", screen: "server", id: "s1" });
  });

  it("maps certificates to domains and backups to backups", () => {
    expect(routeForNotification("acme", n("cert.expiring", "certificate", "c1")).screen).toBe("domains");
    expect(routeForNotification("acme", n("cert.expiring", null, null)).screen).toBe("domains");
    expect(routeForNotification("acme", n("backup.failed", "data_service", "d1")).screen).toBe("backups");
  });

  it("finds the app of a deployment notification by the name in the title, else alerts", () => {
    const title = "Deploy #4 of api-gateway failed";
    expect(routeForNotification("acme", n("deploy.failed", "deployment", "dep9", title), apps)).toEqual({ team: "acme", screen: "app", id: "a2" });
    expect(routeForNotification("acme", n("deploy.failed", "deployment", "dep9", title))).toEqual({ team: "acme", screen: "alerts" });
    expect(routeForNotification("acme", n("deploy.succeeded", "deployment", "dep9", "Deploy #1 of unknown is live"), apps).screen).toBe("alerts");
  });

  it("falls back to alerts for anything else", () => {
    expect(routeForNotification("acme", n("adoptable", null, null)).screen).toBe("alerts");
    expect(routeForNotification("acme", n("app.down", "app", null)).screen).toBe("alerts");
  });
});

describe("deep links", () => {
  it("turns hosbah:// URLs into renderer hash routes", () => {
    expect(hashForUrl("hosbah://t/acme/apps/a1")).toBe("#/t/acme/apps/a1");
    expect(hashForUrl("hosbah://t/acme/servers/s%201")).toBe("#/t/acme/servers/s%201");
    expect(hashForUrl("hosbah://t/acme/domains")).toBe("#/t/acme/domains");
    expect(hashForUrl("hosbah://t/acme/alerts")).toBe("#/t/acme/alerts");
    expect(hashForUrl("hosbah://nonsense")).toBeNull();
    expect(hashForUrl("https://example.com/")).toBeNull();
  });

  it("round-trips links to hash routes", () => {
    expect(linkToHash({ team: "a b", screen: "backups" })).toBe("#/t/a%20b/backups");
  });

  it("picks the link out of a second instance's argv", () => {
    expect(findDeepLinkArg(["Hosbah.exe", "--flag", "hosbah://t/acme/apps/a1"])).toBe("hosbah://t/acme/apps/a1");
    expect(findDeepLinkArg(["Hosbah.exe", "hosbah://bad"])).toBeNull();
    expect(findDeepLinkArg(["Hosbah.exe"])).toBeNull();
  });
});

describe("png", () => {
  it("writes a valid PNG signature and size, and distinct colours per level", () => {
    const g = statusIconPng("green", 16);
    expect([...g.subarray(0, 8)]).toEqual([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);
    expect(g.readUInt32BE(16)).toBe(16);
    expect(g.readUInt32BE(20)).toBe(16);
    const px = (l: "green" | "amber" | "red") => [...statusIconRgba(l, 32).subarray((3 * 32 + 16) * 4, (3 * 32 + 16) * 4 + 3)];
    expect(px("green")).not.toEqual(px("red"));
    expect(px("amber")).not.toEqual(px("red"));
    expect(statusIconRgba("red", 16)[3]).toBe(0); // corner is transparent
  });
  it("rejects a wrong-sized buffer", () => {
    expect(() => encodePng(2, 2, new Uint8Array(3))).toThrow();
  });
});
