import { describe, expect, it } from "vitest";
import { appHealth, backupHealth, canOperate, certHealth, certificateHealth, overallStatus, serverHealth } from "../status";
import type { BackupSummary } from "../types";

const metrics = (o: Record<string, number> = {}) => ({
  cpu_percent: 10, mem_used_bytes: 10, mem_total_bytes: 100, disk_used_bytes: 10, disk_total_bytes: 100,
  uptime_seconds: 1, load1: 0, at: "", ...o,
});
const srv = (o: object = {}) => ({ status: "enrolled" as const, online: true, metrics: metrics(), ...o });

describe("serverHealth", () => {
  it("is ok when enrolled, online and cool", () => expect(serverHealth(srv())).toBe("ok"));
  it("is down when failed or offline", () => {
    expect(serverHealth(srv({ status: "failed" }))).toBe("down");
    expect(serverHealth(srv({ online: false }))).toBe("down");
  });
  it("ignores servers that are not enrolled yet", () => expect(serverHealth(srv({ status: "pending", online: false }))).toBe("ok"));
  it("warns at 90 percent", () => {
    expect(serverHealth(srv({ metrics: metrics({ cpu_percent: 90 }) }))).toBe("warn");
    expect(serverHealth(srv({ metrics: metrics({ mem_used_bytes: 95 }) }))).toBe("warn");
    expect(serverHealth(srv({ metrics: metrics({ disk_used_bytes: 89 }) }))).toBe("ok");
  });
});

describe("appHealth", () => {
  it("maps statuses", () => {
    expect(appHealth({ status: "running", imported_state: null })).toBe("ok");
    expect(appHealth({ status: "failed", imported_state: null })).toBe("down");
    expect(appHealth({ status: "running", imported_state: "missing" })).toBe("down");
    expect(appHealth({ status: "stopped", imported_state: null })).toBe("warn");
  });
  it("warns when the newest deployment failed", () => {
    expect(appHealth({ status: "running", imported_state: null, latest_deployment: { status: "failed" } as never })).toBe("warn");
  });
});

describe("certs", () => {
  it("thresholds", () => {
    expect(certHealth(30)).toBe("ok");
    expect(certHealth(21)).toBe("warn");
    expect(certHealth(7)).toBe("down");
    expect(certHealth(undefined)).toBe("warn");
  });
  it("certificateHealth", () => {
    expect(certificateHealth({ ok: false, san_match: true })).toBe("down");
    expect(certificateHealth({ ok: true, san_match: false, days_left: 60 })).toBe("warn");
    expect(certificateHealth({ ok: true, san_match: true, days_left: 60 })).toBe("ok");
  });
});

describe("backupHealth", () => {
  const now = Date.parse("2026-10-10T12:00:00Z");
  const b = (o: Partial<BackupSummary> = {}): BackupSummary => ({
    last_status: "succeeded", last_at: null, last_error: null, last_restore_status: null,
    last_ok_at: "2026-10-10T06:00:00Z", last_ok_size: 1, count: 1, schedule: "0 3 * * *",
    schedule_paused: false, next_run_at: null, target: "local", ...o,
  });
  it("ok when fresh", () => expect(backupHealth(b(), now)).toBe("ok"));
  it("undefined summary is ok", () => expect(backupHealth(undefined, now)).toBe("ok"));
  it("down when the last failed", () => expect(backupHealth(b({ last_status: "failed" }), now)).toBe("down"));
  it("warn on failed restore test", () => expect(backupHealth(b({ last_restore_status: "fail" }), now)).toBe("warn"));
  it("warn when stale or never ok under a schedule", () => {
    expect(backupHealth(b({ last_ok_at: "2026-10-07T00:00:00Z" }), now)).toBe("warn");
    expect(backupHealth(b({ last_ok_at: null, last_status: null }), now)).toBe("warn");
  });
  it("a paused schedule does not go stale", () => {
    expect(backupHealth(b({ last_ok_at: "2026-10-01T00:00:00Z", schedule_paused: true }), now)).toBe("ok");
  });
});

describe("overallStatus and canOperate", () => {
  it("is green with nothing", () => expect(overallStatus({})).toBe("green"));
  it("worst wins", () => {
    expect(overallStatus({ servers: [srv()], apps: [{ status: "stopped", imported_state: null }] })).toBe("amber");
    expect(overallStatus({ servers: [srv({ online: false })], apps: [{ status: "stopped", imported_state: null }] })).toBe("red");
  });
  it("roles", () => {
    expect(["owner", "admin", "deployer"].every(canOperate)).toBe(true);
    expect(canOperate("viewer")).toBe(false);
    expect(canOperate(undefined)).toBe(false);
  });
});
