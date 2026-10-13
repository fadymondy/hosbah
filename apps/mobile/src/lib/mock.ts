// Dev-only fixture server (EXPO_PUBLIC_MOCK=1). It is a `fetch` replacement, so the real HosbahClient
// (auth, refresh, error mapping) runs unchanged on top of it. Used for screenshots and offline demos;
// production builds never set the flag, and nothing here ever leaves the device.
//
// All names are made up: team "Demo", apps shop-api / blog / status-page, hostnames under example.com.
import type {
  App,
  AppNotification,
  Backup,
  Certificate,
  DataService,
  Deployment,
  Device,
  EventName,
  LogLine,
  NotificationPrefs,
  Server,
  TeamDomain,
  TeamSummary,
} from "@hosbah/client";
import { EVENT_NAMES } from "@hosbah/client";

export const MOCK_TEAM_ID = "11111111-1111-4111-8111-111111111111";
export const MOCK_TEAM_2_ID = "22222222-2222-4222-8222-222222222222";

const MIN = 60_000;
const HOUR = 60 * MIN;
const DAY = 24 * HOUR;

interface Db {
  prefs: NotificationPrefs;
  devices: Device[];
  notifications: AppNotification[];
  appStatus: Record<string, App["status"]>;
  extraDeployments: Deployment[];
  nextNumber: number;
}

export interface MockServer {
  fetch: typeof fetch;
  db: Db;
}

function iso(ms: number): string {
  return new Date(ms).toISOString();
}

export function buildFixtures(now = Date.now()) {
  const teams: TeamSummary[] = [
    { id: MOCK_TEAM_ID, name: "Demo", slug: "demo", role: "owner", created_at: iso(now - 90 * DAY), suspended: false },
    { id: MOCK_TEAM_2_ID, name: "Sandbox", slug: "sandbox", role: "viewer", created_at: iso(now - 30 * DAY), suspended: false },
  ];

  const metrics = (cpu: number, mem: number, disk: number, uptimeDays: number) => ({
    cpu_percent: cpu,
    mem_used_bytes: Math.round(8 * 1024 ** 3 * (mem / 100)),
    mem_total_bytes: 8 * 1024 ** 3,
    disk_used_bytes: Math.round(120 * 1024 ** 3 * (disk / 100)),
    disk_total_bytes: 120 * 1024 ** 3,
    uptime_seconds: uptimeDays * 86_400,
    load1: cpu / 40,
    at: iso(now - 20_000),
  });

  const base = (id: string, name: string, extra: Partial<Server>): Server => ({
    id,
    name,
    provider: "byo",
    kind: "byo",
    status: "enrolled",
    online: true,
    hostname: name,
    ip_address: "203.0.113." + (10 + id.length),
    os: "Ubuntu",
    os_version: "24.04",
    arch: "amd64",
    agent_version: "0.14.2",
    error: null,
    last_seen_at: iso(now - 15_000),
    created_at: iso(now - 60 * DAY),
    color: null,
    icon: null,
    parent_server_id: null,
    metrics: null,
    ...extra,
  });

  const servers: Server[] = [
    base("srv-edge-01", "edge-01", { metrics: metrics(34, 61, 48, 41) }),
    base("srv-build-01", "build-01", { metrics: metrics(71, 93, 57, 12) }),
    base("srv-data-01", "data-01", { metrics: metrics(18, 44, 82, 63) }),
  ];

  const dep = (appId: string, number: number, status: Deployment["status"], ageMs: number, msg: string, extra: Partial<Deployment> = {}): Deployment => ({
    id: `dep-${appId}-${number}`,
    app_id: appId,
    number,
    ref: "main",
    commit_sha: `${number}`.padStart(2, "0") + "c0ffee1234567890abcdef1234567890ab",
    commit_message: msg,
    trigger: "github",
    track: "fast",
    kind: "deploy",
    status,
    step: null,
    error: status === "failed" ? "build failed: exit code 1" : null,
    created_at: iso(now - ageMs),
    started_at: iso(now - ageMs + 2_000),
    finished_at: iso(now - ageMs + (status === "failed" ? 41_000 : 92_000)),
    ...extra,
  });

  const deployments: Record<string, Deployment[]> = {
    "app-shop-api": [
      dep("app-shop-api", 42, "succeeded", 2 * HOUR, "Add rate limiting to checkout"),
      dep("app-shop-api", 41, "succeeded", 1 * DAY, "Fix cart total rounding"),
      dep("app-shop-api", 40, "succeeded", 3 * DAY, "Bump dependencies", { kind: "restart", commit_message: null, trigger: "manual" }),
      dep("app-shop-api", 39, "failed", 4 * DAY, "Try new payment provider"),
    ],
    "app-blog": [
      dep("app-blog", 17, "failed", 5 * HOUR, "Switch image pipeline to AVIF"),
      dep("app-blog", 16, "succeeded", 2 * DAY, "New post: how we cut deploy times"),
      dep("app-blog", 15, "succeeded", 6 * DAY, "Update footer links"),
    ],
    "app-status-page": [
      dep("app-status-page", 9, "failed", 26 * MIN, "Add incident history page", { error: "health check failed: no response on :3000" }),
      dep("app-status-page", 8, "succeeded", 8 * DAY, "Initial public status page"),
    ],
  };

  const app = (id: string, name: string, title: string, status: App["status"], serverId: string, host: string, runtime: string, latest: Deployment | undefined): App => ({
    id,
    name,
    title,
    description: null,
    color: null,
    icon: null,
    server_id: serverId,
    server_name: servers.find((s) => s.id === serverId)?.name ?? "",
    repo_url: `https://git.example.com/demo/${name}`,
    branch: "main",
    runtime,
    source: "git",
    imported_state: null,
    status,
    created_at: iso(now - 70 * DAY),
    latest_deployment: latest ?? null,
    domains: [{ id: `dom-${name}`, hostname: host, tls: "auto", verified: true, verified_at: iso(now - 60 * DAY), created_at: iso(now - 60 * DAY) }],
  });

  const apps: App[] = [
    app("app-shop-api", "shop-api", "Shop API", "running", "srv-edge-01", "shop.example.com", "node", deployments["app-shop-api"]?.[0]),
    app("app-blog", "blog", "Blog", "running", "srv-edge-01", "blog.example.com", "static", deployments["app-blog"]?.[0]),
    app("app-status-page", "status-page", "Status page", "failed", "srv-build-01", "status.example.com", "node", deployments["app-status-page"]?.[0]),
  ];

  const domains: TeamDomain[] = apps.flatMap((a) =>
    (a.domains ?? []).map((d) => ({ ...d, app_id: a.id, app_name: a.name, server_id: a.server_id })),
  );
  domains.push({
    id: "dom-api",
    hostname: "api.example.com",
    tls: "auto",
    verified: true,
    verified_at: iso(now - 40 * DAY),
    created_at: iso(now - 40 * DAY),
    app_id: "app-shop-api",
    app_name: "shop-api",
    server_id: "srv-edge-01",
  });

  const cert = (id: string, hostname: string, daysLeft: number, extra: Partial<Certificate> = {}): Certificate => ({
    id,
    hostname,
    source: "app",
    ok: true,
    issuer: "Let's Encrypt R11",
    not_before: iso(now - (90 - daysLeft) * DAY),
    not_after: iso(now + daysLeft * DAY),
    days_left: daysLeft,
    san_match: true,
    last_checked_at: iso(now - 3 * HOUR),
    ...extra,
  });
  const certificates: Certificate[] = [
    cert("c1", "shop.example.com", 62),
    cert("c2", "api.example.com", 80),
    cert("c3", "blog.example.com", 18),
    cert("c4", "status.example.com", 4),
  ];

  const services: DataService[] = [
    {
      id: "svc-shop-db",
      name: "shop-db",
      kind: "postgres",
      version: "16",
      status: "running",
      server_id: "srv-data-01",
      error: null,
      source: "managed",
      created_at: iso(now - 60 * DAY),
      backups: {
        last_status: "succeeded",
        last_at: iso(now - 3 * HOUR),
        last_error: null,
        last_restore_status: "ok",
        last_ok_at: iso(now - 3 * HOUR),
        last_ok_size: 134_217_728,
        count: 14,
        schedule: "0 3 * * *",
        schedule_paused: false,
        next_run_at: iso(now + 21 * HOUR),
        target: "s3",
      },
    },
    {
      id: "svc-blog-db",
      name: "blog-db",
      kind: "mysql",
      version: "8",
      status: "running",
      server_id: "srv-data-01",
      error: null,
      source: "managed",
      created_at: iso(now - 50 * DAY),
      backups: {
        last_status: "failed",
        last_at: iso(now - 9 * HOUR),
        last_error: "target unreachable",
        last_restore_status: null,
        last_ok_at: iso(now - 2 * DAY),
        last_ok_size: 21_000_000,
        count: 6,
        schedule: "0 2 * * *",
        schedule_paused: false,
        next_run_at: iso(now + 15 * HOUR),
        target: "minio",
      },
    },
    {
      id: "svc-cache",
      name: "cache",
      kind: "redis",
      version: "7",
      status: "running",
      server_id: "srv-data-01",
      error: null,
      source: "managed",
      created_at: iso(now - 40 * DAY),
    },
  ];

  const notifications: AppNotification[] = [
    { id: "n1", kind: "deploy.failed", title: "status-page: deployment #9 failed", body: "health check failed: no response on :3000", subject_type: "app", subject_id: "app-status-page", read_at: null, created_at: iso(now - 26 * MIN) },
    { id: "n2", kind: "resource.high", title: "build-01: memory at 93%", body: "Memory has been above 90% for 10 minutes.", subject_type: "server", subject_id: "srv-build-01", read_at: null, created_at: iso(now - 80 * MIN) },
    { id: "n3", kind: "backup.failed", title: "blog-db: backup failed", body: "target unreachable", subject_type: "data_service", subject_id: "svc-blog-db", read_at: null, created_at: iso(now - 9 * HOUR) },
    { id: "n4", kind: "cert.expiring", title: "status.example.com: certificate expires in 4 days", body: null, subject_type: "certificate", subject_id: "c4", read_at: iso(now - 5 * HOUR), created_at: iso(now - 12 * HOUR) },
    { id: "n5", kind: "deploy.succeeded", title: "shop-api: deployment #42 succeeded", body: "Add rate limiting to checkout", subject_type: "app", subject_id: "app-shop-api", read_at: iso(now - 1 * HOUR), created_at: iso(now - 2 * HOUR) },
  ];

  const logs = (failed: boolean): LogLine[] => {
    const lines: [string, string][] = [
      ["system", "Cloning git.example.com/demo/app (main)"],
      ["system", "Detected Node.js project, building with Railpack"],
      ["stdout", "#1 [internal] load build definition"],
      ["stdout", "#4 npm ci --omit=dev"],
      ["stdout", "added 312 packages in 14s"],
      ["stdout", "#6 npm run build"],
      ...(failed
        ? ([
            ["stderr", "error TS2339: Property 'sharp' does not exist on type 'Image'."],
            ["stderr", "Build failed with exit code 1"],
          ] as [string, string][])
        : ([
            ["stdout", "Build complete in 31s"],
            ["system", "Starting release, waiting for the health check"],
            ["system", "Health check passed; switching traffic"],
            ["system", "Release is live"],
          ] as [string, string][])),
    ];
    return lines.map(([stream, line], i) => ({ seq: i + 1, event: "log", stream, line }));
  };

  return { teams, servers, apps, deployments, domains, certificates, services, notifications, logs };
}

type Fixtures = ReturnType<typeof buildFixtures>;

function json(status: number, body: unknown): Response {
  return new Response(body === undefined ? null : JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function defaultPrefs(): NotificationPrefs {
  return Object.fromEntries(EVENT_NAMES.map((e) => [e, true])) as NotificationPrefs;
}

/** Builds the fixture `fetch`. `latencyMs` makes loading states visible (0 in tests). */
export function createMockFetch(opts: { now?: () => number; latencyMs?: number } = {}): MockServer {
  const clock = opts.now ?? Date.now;
  const fx: Fixtures = buildFixtures(clock());
  const db: Db = {
    prefs: defaultPrefs(),
    devices: [],
    notifications: fx.notifications.map((n) => ({ ...n })),
    appStatus: {},
    extraDeployments: [],
    nextNumber: 100,
  };
  let deviceSeq = 0;

  const appOf = (id: string): App | undefined => {
    const a = fx.apps.find((x) => x.id === id || x.name === id);
    if (!a) return undefined;
    const latest = [...db.extraDeployments.filter((d) => d.app_id === a.id), ...(fx.deployments[a.id] ?? [])][0];
    return { ...a, status: db.appStatus[a.id] ?? a.status, latest_deployment: latest ?? a.latest_deployment ?? null };
  };
  const deploymentsOf = (appId: string): Deployment[] => [...db.extraDeployments.filter((d) => d.app_id === appId), ...(fx.deployments[appId] ?? [])];

  const handle = (method: string, url: URL, body: Record<string, unknown>): Response => {
    const p = url.pathname;
    let m: RegExpMatchArray | null;

    if (method === "POST" && p === "/api/auth/login") {
      const email = String(body.email ?? "");
      if (body.password === "wrong") return json(401, { error: "invalid credentials" });
      if (/2fa/i.test(email)) return json(401, { error: "two-factor required", code: "2fa_required", challenge: "mock-challenge" });
      return json(200, { token: "mock.session.token", user: { id: "user-1", email } });
    }
    if (method === "POST" && p === "/api/auth/2fa/challenge") {
      if (body.code === "000000") return json(401, { error: "invalid code", code: "2fa_invalid" });
      return json(200, { token: "mock.session.token", user: { id: "user-1", email: "demo-2fa@example.com" } });
    }
    if (method === "POST" && p === "/api/auth/device/refresh") {
      return json(200, { token: "mock.session.token", refresh_token: "hbr_mockrefresh" + ++deviceSeq, device_id: db.devices[0]?.id ?? "dev-1" });
    }
    if (method === "GET" && p === "/api/me") {
      return json(200, { user: { id: "user-1", email: "demo@example.com" }, teams: fx.teams });
    }
    if (method === "POST" && p === "/api/me/devices") {
      const d: Device = {
        id: `dev-${++deviceSeq}`,
        name: String(body.name ?? "Phone"),
        platform: (body.platform as Device["platform"]) ?? "android",
        app: "mobile",
        locale: String(body.locale ?? "ar"),
        app_version: String(body.app_version ?? "1.0.0"),
        push: false,
        created_at: iso(clock()),
        last_seen_at: iso(clock()),
      };
      if (db.devices.length === 0) {
        db.devices.push(
          { ...d, id: "dev-laptop", name: "Work laptop", platform: "windows", app: "desktop", locale: "en", app_version: "1.0.0", created_at: iso(clock() - 12 * DAY), last_seen_at: iso(clock() - 3 * HOUR) },
        );
      }
      db.devices.unshift(d);
      return json(201, { device: d, refresh_token: "hbr_mockrefresh0" });
    }
    if (method === "GET" && p === "/api/me/devices") return json(200, { devices: db.devices });
    if ((m = p.match(/^\/api\/me\/devices\/([^/]+)$/)) && method === "DELETE") {
      db.devices = db.devices.filter((d) => d.id !== m![1]);
      return json(204, undefined);
    }
    if ((m = p.match(/^\/api\/me\/devices\/([^/]+)\/push$/)) && method === "PUT") {
      const d = db.devices.find((x) => x.id === m![1]);
      if (d) d.push = !!body.token;
      return json(204, undefined);
    }
    if ((m = p.match(/^\/api\/me\/devices\/([^/]+)\/test$/)) && method === "POST") return json(200, { sent: 1 });
    if (p === "/api/me/notification-prefs") {
      if (method === "PUT") {
        const ev = (body.events ?? {}) as Partial<Record<EventName, boolean>>;
        for (const [k, v] of Object.entries(ev)) if ((EVENT_NAMES as readonly string[]).includes(k)) db.prefs[k as EventName] = !!v;
      }
      return json(200, { events: db.prefs, push_available: true });
    }

    const tm = p.match(/^\/api\/teams\/([^/]+)(\/.*)?$/);
    if (tm) {
      const team = fx.teams.find((t) => t.id === tm[1]);
      if (!team) return json(404, { error: "not found" });
      const rest = tm[2] ?? "";
      const writable = team.role !== "viewer";

      if (method === "GET" && rest === "/servers") return json(200, { servers: fx.servers, agent_version: "0.14.2" });
      if ((m = rest.match(/^\/servers\/([^/]+)$/)) && method === "GET") {
        const s = fx.servers.find((x) => x.id === m![1]);
        return s ? json(200, s) : json(404, { error: "not found" });
      }
      if ((m = rest.match(/^\/servers\/([^/]+)\/metrics$/)) && method === "GET") {
        const s = fx.servers.find((x) => x.id === m![1]);
        const cpu = s?.metrics?.cpu_percent ?? 20;
        const points = Array.from({ length: 48 }, (_, i) => ({
          at: iso(clock() - (47 - i) * 30 * MIN),
          cpu: Math.max(2, Math.min(99, cpu + Math.sin(i / 4) * 14 + ((i * 7) % 5))),
          mem_used: 0,
          mem_total: 1,
          disk_used: 0,
          disk_total: 1,
          load1: 0,
        }));
        return json(200, { range: url.searchParams.get("range") ?? "24h", step_seconds: 1800, points });
      }
      if (method === "GET" && rest === "/apps") return json(200, { apps: fx.apps.map((a) => appOf(a.id)) });
      if ((m = rest.match(/^\/apps\/([^/]+)$/)) && method === "GET") {
        const a = appOf(m[1]!);
        if (!a) return json(404, { error: "not found" });
        return json(200, {
          app: a,
          live_release: a.status === "failed" ? null : { id: "rel-1", version: 42, image: `registry.example.com/${a.name}:42`, commit_sha: "42c0ffee", status: "live", deployment_id: a.latest_deployment?.id ?? "", activated_at: iso(clock() - 2 * HOUR), created_at: iso(clock() - 2 * HOUR) },
          previous_release: null,
          domains: a.domains ?? [],
          processes: [{ name: "web", command: "node server.js" }],
        });
      }
      if ((m = rest.match(/^\/apps\/([^/]+)\/deployments$/))) {
        const a = appOf(m[1]!);
        if (!a) return json(404, { error: "not found" });
        if (method === "GET") return json(200, { deployments: deploymentsOf(a.id) });
        if (method === "POST") {
          if (!writable) return json(403, { error: "forbidden" });
          const d: Deployment = { id: `dep-new-${++db.nextNumber}`, app_id: a.id, number: db.nextNumber, ref: "main", commit_sha: null, commit_message: null, trigger: "manual", track: "fast", kind: "deploy", status: "queued", step: null, error: null, created_at: iso(clock()), started_at: null, finished_at: null };
          db.extraDeployments.unshift(d);
          return json(201, d);
        }
      }
      if ((m = rest.match(/^\/apps\/([^/]+)\/deployments\/([^/]+)$/)) && method === "GET") {
        const a = appOf(m[1]!);
        const d = a ? deploymentsOf(a.id).find((x) => x.id === m![2]) : undefined;
        if (!a || !d) return json(404, { error: "not found" });
        return json(200, { deployment: d, logs: fx.logs(d.status === "failed") });
      }
      if ((m = rest.match(/^\/apps\/([^/]+)\/restart$/)) && method === "POST") {
        const a = appOf(m[1]!);
        if (!a) return json(404, { error: "not found" });
        if (!writable) return json(403, { error: "forbidden" });
        const d: Deployment = { id: `dep-new-${++db.nextNumber}`, app_id: a.id, number: db.nextNumber, ref: "main", commit_sha: null, commit_message: null, trigger: "manual", track: null, kind: "restart", status: "queued", step: null, error: null, created_at: iso(clock()), started_at: null, finished_at: null };
        db.extraDeployments.unshift(d);
        return json(201, d);
      }
      if ((m = rest.match(/^\/apps\/([^/]+)\/power$/)) && method === "POST") {
        const a = appOf(m[1]!);
        if (!a) return json(404, { error: "not found" });
        if (!writable) return json(403, { error: "forbidden" });
        db.appStatus[a.id] = body.action === "stop" ? "stopped" : "running";
        return json(202, { operation: "queued" });
      }
      if (method === "GET" && rest === "/domains") return json(200, { domains: fx.domains });
      if (method === "GET" && rest === "/certificates") return json(200, { certificates: fx.certificates });
      if (method === "GET" && rest === "/data-services") return json(200, { services: fx.services });
      if ((m = rest.match(/^\/data-services\/([^/]+)\/backups$/)) && method === "GET") {
        const list: Backup[] = [];
        return json(200, { backups: list });
      }
      if (method === "GET" && rest === "/notifications") {
        return json(200, { notifications: db.notifications, unread: db.notifications.filter((n) => !n.read_at).length });
      }
      if (method === "POST" && rest === "/notifications/read") {
        for (const n of db.notifications) n.read_at ??= iso(clock());
        return json(204, undefined);
      }
      if ((m = rest.match(/^\/notifications\/([^/]+)\/read$/)) && method === "POST") {
        const n = db.notifications.find((x) => x.id === m![1]);
        if (n) n.read_at ??= iso(clock());
        return json(204, undefined);
      }
    }
    return json(404, { error: `mock: no fixture for ${method} ${p}` });
  };

  const mockFetch = (async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const url = new URL(typeof input === "string" ? input : input instanceof URL ? input.toString() : (input as Request).url);
    const method = (init?.method ?? "GET").toUpperCase();
    let body: Record<string, unknown> = {};
    if (typeof init?.body === "string" && init.body) {
      try {
        body = JSON.parse(init.body) as Record<string, unknown>;
      } catch {
        body = {};
      }
    }
    if (url.pathname === "/api/auth/csrf") return json(200, { csrf_token: "mock" });
    if (opts.latencyMs) await new Promise((r) => setTimeout(r, opts.latencyMs));
    return handle(method, url, body);
  }) as typeof fetch;

  return { fetch: mockFetch, db };
}
