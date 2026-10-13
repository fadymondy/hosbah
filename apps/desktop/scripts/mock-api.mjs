// A tiny stand-in for the Hosbah API, with made-up data, for developing the desktop app and taking
// screenshots without ever touching a real server or a real app.
//
//   node scripts/mock-api.mjs            # http://127.0.0.1:8710 (the local stack's port) or $PORT
//   sign in with  demo@example.com / demo-pass     (2FA user: twofa@example.com / demo-pass, code 123456)
//
// Implements exactly what the app calls: login + 2FA, device register / refresh / delete, /api/me,
// servers + metrics, apps + deployments (+ redeploy / restart / power, which only change this
// process's memory), domains, certificates, data services + backups, notifications (+ SSE stream +
// read), notification prefs and devices. Dev helpers: POST /__mock/notify {team?, kind, title, ...}
// pushes a notification to every open stream; GET /__mock/calls lists the mutating calls received.
import { createServer } from "node:http";
import { randomBytes } from "node:crypto";

const PORT = Number(process.env.PORT ?? 8710);
const TTL = Number(process.env.MOCK_TTL ?? 3600); // seconds a token lives (set 90 to watch refresh)
const NOW = () => Date.now();
const iso = (ms) => new Date(ms).toISOString();
const ago = (min) => iso(NOW() - min * 60_000);
const GB = 1024 ** 3;

// ── fixtures (nothing here is real) ───────────────────────────────────────────────────────────
const teams = [
  { id: "team_acme", name: "Acme Hosting", slug: "acme", role: "owner", created_at: ago(60 * 24 * 90), suspended: false },
  { id: "team_studio", name: "Studio Nile", slug: "studio", role: "viewer", created_at: ago(60 * 24 * 40), suspended: false },
];

const mkServer = (id, name, o) => ({
  id, name, provider: "byo", kind: "byo", status: "enrolled", online: true, hostname: name, ip_address: o.ip, os: "ubuntu", os_version: "24.04",
  arch: "amd64", agent_version: "0.9.2", error: null, last_seen_at: ago(0.2), created_at: ago(60 * 24 * 60), color: null, icon: null, parent_server_id: null,
  metrics: { cpu_percent: o.cpu, mem_used_bytes: o.mem * GB, mem_total_bytes: o.memTotal * GB, disk_used_bytes: o.disk * GB, disk_total_bytes: o.diskTotal * GB, uptime_seconds: o.up, load1: o.cpu / 40, at: ago(0.2) },
});
const servers = {
  team_acme: [
    mkServer("srv_edge1", "edge-1", { ip: "203.0.113.10", cpu: 34, mem: 5, memTotal: 8, disk: 48, diskTotal: 100, up: 86400 * 21 + 3600 * 5 }),
    mkServer("srv_db1", "db-1", { ip: "203.0.113.11", cpu: 18, mem: 21, memTotal: 32, disk: 372, diskTotal: 400, up: 86400 * 63 + 7200 }),
    mkServer("srv_build1", "build-1", { ip: "203.0.113.12", cpu: 7, mem: 3, memTotal: 16, disk: 60, diskTotal: 200, up: 86400 * 4 }),
  ],
  team_studio: [mkServer("srv_st1", "studio-1", { ip: "198.51.100.20", cpu: 12, mem: 2, memTotal: 4, disk: 11, diskTotal: 80, up: 86400 * 9 })],
};

const dep = (appId, number, status, o = {}) => ({
  id: `dep_${appId}_${number}`, app_id: appId, number, ref: "main", commit_sha: o.sha ?? randomBytes(20).toString("hex"), commit_message: o.msg ?? "Update dependencies",
  trigger: "github", track: "fast", kind: o.kind ?? "deploy", status, step: null, error: o.error ?? null, created_at: ago(o.ago ?? 60),
  started_at: ago((o.ago ?? 60) - 0.1), finished_at: status === "running" || status === "queued" ? null : ago((o.ago ?? 60) - 1.4),
});
const dom = (host) => ({ id: `dom_${host}`, hostname: host, tls: "auto", verified: true, verified_at: ago(60 * 24 * 30), created_at: ago(60 * 24 * 30) });
const mkApp = (id, name, title, serverId, status, deps, hosts) => ({
  id, name, title, description: null, color: null, icon: null, server_id: serverId, repo_url: `https://github.com/example/${name}`, branch: "main", runtime: "node",
  source: "git", imported_state: null, status, created_at: ago(60 * 24 * 50), _deps: deps, _hosts: hosts,
});
const apps = {
  team_acme: [
    mkApp("app_shop", "shop", "Shop storefront", "srv_edge1", "running", [dep("app_shop", 41, "succeeded", { ago: 35, msg: "Add gift cards to checkout" }), dep("app_shop", 40, "succeeded", { ago: 60 * 26, msg: "Fix cart rounding" })], ["shop.example.com", "www.shop.example.com"]),
    mkApp("app_blog", "blog", "Company blog", "srv_edge1", "running", [dep("app_blog", 17, "succeeded", { ago: 60 * 50, msg: "New post template" })], ["blog.example.com"]),
    mkApp("app_gw", "api-gateway", "API gateway", "srv_edge1", "running", [dep("app_gw", 88, "failed", { ago: 120, msg: "Switch to new auth middleware", error: "health check failed: GET /healthz returned 502" }), dep("app_gw", 87, "succeeded", { ago: 60 * 30, msg: "Rate limit per key" })], ["api.example.com"]),
    mkApp("app_docs", "docs", "Documentation", "srv_build1", "stopped", [dep("app_docs", 9, "succeeded", { ago: 60 * 24 * 6, msg: "Reorganise sidebar" })], ["docs.example.com"]),
  ],
  team_studio: [mkApp("app_site", "site", "Studio website", "srv_st1", "running", [dep("app_site", 5, "succeeded", { ago: 60 * 8, msg: "Launch banner" })], ["studio.example.org"])],
};

const certs = {
  team_acme: [
    { id: "c1", hostname: "shop.example.com", source: "app", ok: true, issuer: "Let's Encrypt R11", not_before: ago(60 * 24 * 78), not_after: iso(NOW() + 60 * 60_000 * 24 * 12), days_left: 12, san_match: true, last_checked_at: ago(30) },
    { id: "c2", hostname: "blog.example.com", source: "app", ok: true, issuer: "Let's Encrypt R11", not_before: ago(60 * 24 * 30), not_after: iso(NOW() + 60 * 60_000 * 24 * 60), days_left: 60, san_match: true, last_checked_at: ago(30) },
    { id: "c3", hostname: "api.example.com", source: "app", ok: true, issuer: "Let's Encrypt R11", not_before: ago(60 * 24 * 20), not_after: iso(NOW() + 60 * 60_000 * 24 * 70), days_left: 70, san_match: true, last_checked_at: ago(30) },
    { id: "c4", hostname: "docs.example.com", source: "app", ok: true, issuer: "Let's Encrypt R11", not_before: ago(60 * 24 * 10), not_after: iso(NOW() + 60 * 60_000 * 24 * 80), days_left: 80, san_match: true, last_checked_at: ago(30) },
  ],
  team_studio: [{ id: "c5", hostname: "studio.example.org", source: "app", ok: true, issuer: "Let's Encrypt R11", not_after: iso(NOW() + 60 * 60_000 * 24 * 55), days_left: 55, san_match: true }],
};

const bk = (o) => ({ last_status: "succeeded", last_at: ago(60 * 5), last_error: null, last_restore_status: "ok", last_ok_at: ago(60 * 5), last_ok_size: 2.4 * GB, count: 14, schedule: "0 3 * * *", schedule_paused: false, next_run_at: iso(NOW() + 60 * 60_000 * 19), target: "s3", ...o });
const services = {
  team_acme: [
    { id: "ds_pg", name: "shop-db", kind: "postgres", version: "17", status: "running", server_id: "srv_db1", error: null, source: "managed", created_at: ago(60 * 24 * 50), backups: bk({}) },
    { id: "ds_my", name: "blog-db", kind: "mysql", version: "8.4", status: "running", server_id: "srv_db1", error: null, source: "managed", created_at: ago(60 * 24 * 50), backups: bk({ last_ok_size: 310 * 1024 ** 2, count: 9 }) },
    { id: "ds_redis", name: "cache", kind: "redis", version: "7", status: "running", server_id: "srv_edge1", error: null, source: "managed", created_at: ago(60 * 24 * 50) },
  ],
  team_studio: [],
};
const backupRows = (svc) =>
  Array.from({ length: 6 }, (_, i) => ({ id: `${svc}_b${i}`, kind: i === 0 ? "manual" : "scheduled", status: "succeeded", target: "s3", size_bytes: 2.3 * GB + i * 1e7, error: null, restore_status: i === 2 ? "ok" : null, taken_at: ago(60 * (5 + 24 * i)), created_at: ago(60 * (5 + 24 * i)) }));

let nSeq = 10;
const note = (team, kind, title, body, st, sid, minAgo, read) => ({ id: `n_${nSeq++}`, kind, title, body, subject_type: st, subject_id: sid, read_at: read ? ago(minAgo - 1) : null, created_at: ago(minAgo) });
const notifications = {
  team_acme: [
    note("team_acme", "resource.high", "Disk almost full on db-1", "Disk is at 93%.", "server", "srv_db1", 12, false),
    note("team_acme", "cert.expiring", "Certificate for shop.example.com expires in 12 days", null, "certificate", "c1", 55, false),
    note("team_acme", "deploy.succeeded", "Deploy #41 of shop is live", null, "deployment", "dep_app_shop_41", 35, false),
    note("team_acme", "deploy.failed", "Deploy #88 of api-gateway failed", "health check failed: GET /healthz returned 502\nRelease v87 is still serving.", "deployment", "dep_app_gw_88", 120, true),
    note("team_acme", "backup.failed", "Backup of cache failed", null, "data_service", "ds_redis", 60 * 30, true),
  ],
  team_studio: [note("team_studio", "deploy.succeeded", "Deploy #5 of site is live", null, "deployment", "dep_app_site_5", 480, true)],
};

const prefs = { "deploy.succeeded": true, "deploy.failed": true, "app.down": true, "app.up": true, "resource.high": true, "server.offline": true, "cert.expiring": true, "backup.failed": true };

// ── state ──────────────────────────────────────────────────────────────────────────────────────
const user = { id: "usr_demo", email: "demo@example.com" };
const accessTokens = new Map(); // token -> true
const refreshTokens = new Map(); // refresh -> device id
const devices = new Map();
const challenges = new Map();
const calls = []; // mutating calls, for checking nothing unexpected happened
const streams = new Map(); // team id -> Set<res>

const b64 = (o) => Buffer.from(JSON.stringify(o)).toString("base64url");
const mint = () => {
  const token = `${b64({ alg: "none", typ: "JWT" })}.${b64({ sub: user.id, exp: Math.floor(NOW() / 1000) + TTL, jti: randomBytes(6).toString("hex") })}.mock`;
  accessTokens.set(token, true);
  return token;
};
const unread = (team) => notifications[team].filter((n) => !n.read_at).length;
const payload = (team) => ({ notifications: [...notifications[team]].sort((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at)), unread: unread(team) });
const broadcast = (team) => {
  for (const res of streams.get(team) ?? []) res.write(`event: notifications\ndata: ${JSON.stringify(payload(team))}\n\n`);
};
const findTeam = (ref) => teams.find((t) => t.id === ref || t.slug === ref);
const publicApp = (team, a) => {
  const { _deps, _hosts, ...rest } = a;
  const s = servers[team].find((x) => x.id === a.server_id);
  return { ...rest, server_name: s?.name, latest_deployment: _deps[0] ?? null, domains: _hosts.map(dom) };
};

// ── http ───────────────────────────────────────────────────────────────────────────────────────
const readBody = (req) => new Promise((res) => { let s = ""; req.on("data", (c) => (s += c)); req.on("end", () => { try { res(s ? JSON.parse(s) : {}); } catch { res({}); } }); });
const send = (res, status, body) => { res.writeHead(status, { "Content-Type": "application/json" }); res.end(body === undefined ? "" : JSON.stringify(body)); };
const err = (res, status, code, error) => send(res, status, { error, code });

function csrfOk(req) {
  const m = /(?:^|;\s*)togo_csrf=([^;]+)/.exec(req.headers.cookie ?? "");
  return !!m && m[1] === req.headers["x-csrf-token"];
}

function loginOk(res, who) {
  const token = mint();
  send(res, 200, { token, user: { id: who === "twofa@example.com" ? "usr_2fa" : user.id, email: who } });
}

const server = createServer(async (req, res) => {
  const url = new URL(req.url ?? "/", "http://x");
  const path = url.pathname;
  const method = req.method ?? "GET";
  if (!path.startsWith("/__mock")) console.log(`${method} ${path}`);

  // helpers for the test harness
  if (path === "/__mock/calls") return send(res, 200, calls);
  if (path === "/__mock/notify" && method === "POST") {
    const b = await readBody(req);
    const team = findTeam(b.team ?? "acme").id;
    notifications[team].unshift(note(team, b.kind ?? "app.down", b.title ?? "shop is down", b.body ?? null, b.subject_type ?? "app", b.subject_id ?? "app_shop", 0, false));
    if (b.kind === "app.down" && !b.keep) apps[team].find((a) => a.id === (b.subject_id ?? "app_shop")).status = "failed";
    broadcast(team);
    return send(res, 201, { ok: true });
  }
  if (path === "/__mock/reset" && method === "POST") { calls.length = 0; return send(res, 200, { ok: true }); }

  // ── auth (CSRF double-submit like the real auth plugin)
  if (method === "POST" && path === "/api/auth/login") {
    if (!csrfOk(req)) return err(res, 403, "csrf", "csrf token mismatch");
    const b = await readBody(req);
    if (b.password !== "demo-pass" || !["demo@example.com", "twofa@example.com"].includes(b.email)) return err(res, 401, "invalid_credentials", "invalid email or password");
    if (b.email === "twofa@example.com") {
      const challenge = randomBytes(8).toString("hex");
      challenges.set(challenge, b.email);
      return send(res, 401, { error: "two-factor code required", code: "2fa_required", challenge });
    }
    return loginOk(res, b.email);
  }
  if (method === "POST" && path === "/api/auth/2fa/challenge") {
    if (!csrfOk(req)) return err(res, 403, "csrf", "csrf token mismatch");
    const b = await readBody(req);
    const who = challenges.get(b.challenge);
    if (!who || (b.code !== "123456" && b.recovery_code !== "recover-1")) return err(res, 401, "2fa_invalid", "invalid code");
    challenges.delete(b.challenge);
    return loginOk(res, who);
  }
  if (method === "POST" && path === "/api/auth/device/refresh") {
    if (!csrfOk(req)) return err(res, 403, "csrf", "csrf token mismatch");
    const b = await readBody(req);
    const deviceId = refreshTokens.get(b.refresh_token);
    if (!deviceId) return err(res, 401, "invalid_refresh", "invalid refresh token");
    refreshTokens.delete(b.refresh_token); // rotates: the old one is dead
    const refresh = `rt_${randomBytes(12).toString("hex")}`;
    refreshTokens.set(refresh, deviceId);
    return send(res, 200, { token: mint(), refresh_token: refresh, device_id: deviceId, expires_at: iso(NOW() + 90 * 86_400_000) });
  }

  // ── everything below needs a bearer token
  const bearer = /^Bearer (.+)$/.exec(req.headers.authorization ?? "")?.[1];
  if (!bearer || !accessTokens.has(bearer)) return err(res, 401, "unauthorized", "unauthorized");
  const exp = JSON.parse(Buffer.from(bearer.split(".")[1], "base64url").toString()).exp * 1000;
  if (exp < NOW()) { accessTokens.delete(bearer); return err(res, 401, "token_expired", "token expired"); }

  if (path === "/api/me" && method === "GET") return send(res, 200, { user, teams });
  if (path === "/api/me/devices" && method === "POST") {
    const b = await readBody(req);
    const device = { id: `dev_${randomBytes(4).toString("hex")}`, name: b.name, platform: b.platform, app: b.app, locale: b.locale ?? "ar", app_version: b.app_version ?? "", push: false, created_at: iso(NOW()), last_seen_at: iso(NOW()) };
    devices.set(device.id, device);
    const refresh = `rt_${randomBytes(12).toString("hex")}`;
    refreshTokens.set(refresh, device.id);
    calls.push({ method, path, body: b });
    return send(res, 201, { device, refresh_token: refresh });
  }
  if (path === "/api/me/devices" && method === "GET") return send(res, 200, { devices: [...devices.values()] });
  let m = /^\/api\/me\/devices\/([^/]+)$/.exec(path);
  if (m && method === "DELETE") { devices.delete(m[1]); for (const [k, v] of refreshTokens) if (v === m[1]) refreshTokens.delete(k); calls.push({ method, path }); return send(res, 204); }
  if (path === "/api/me/notification-prefs") {
    if (method === "PUT") { Object.assign(prefs, (await readBody(req)).events ?? {}); calls.push({ method, path, body: prefs }); }
    return send(res, 200, { events: prefs, push_available: false });
  }

  m = /^\/api\/teams\/([^/]+)\/(.*)$/.exec(path);
  if (!m) return err(res, 404, "not_found", "not found");
  const team = findTeam(decodeURIComponent(m[1]));
  if (!team) return err(res, 404, "not_found", "team not found");
  const tid = team.id;
  const rest = m[2];
  const canOperate = ["owner", "admin", "deployer"].includes(team.role);

  if (rest === "servers" && method === "GET") return send(res, 200, { servers: servers[tid], agent_version: "0.9.2" });
  let r = /^servers\/([^/]+)$/.exec(rest);
  if (r) { const s = servers[tid].find((x) => x.id === r[1]); return s ? send(res, 200, s) : err(res, 404, "not_found", "not found"); }
  r = /^servers\/([^/]+)\/metrics$/.exec(rest);
  if (r) {
    const s = servers[tid].find((x) => x.id === r[1]);
    if (!s) return err(res, 404, "not_found", "not found");
    const points = Array.from({ length: 60 }, (_, i) => {
      const wob = Math.sin(i / 5) * 8 + Math.sin(i / 2.3) * 3;
      return { at: iso(NOW() - (59 - i) * 60_000), cpu: Math.max(1, Math.min(99, s.metrics.cpu_percent + wob)), mem_used: s.metrics.mem_used_bytes * (0.97 + (i % 7) / 200), mem_total: s.metrics.mem_total_bytes, disk_used: s.metrics.disk_used_bytes, disk_total: s.metrics.disk_total_bytes, load1: s.metrics.load1 };
    });
    return send(res, 200, { range: url.searchParams.get("range") ?? "1h", step_seconds: 60, points });
  }

  if (rest === "apps" && method === "GET") return send(res, 200, { apps: apps[tid].map((a) => publicApp(tid, a)) });
  r = /^apps\/([^/]+)$/.exec(rest);
  if (r && method === "GET") {
    const a = apps[tid].find((x) => x.id === r[1] || x.name === r[1]);
    if (!a) return err(res, 404, "not_found", "not found");
    const live = a._deps.find((d) => d.status === "succeeded");
    const rel = (d, v, status) => d && { id: `rel_${d.id}`, version: v, image: `registry.example.com/${a.name}:${d.number}`, commit_sha: d.commit_sha, status, deployment_id: d.id, activated_at: d.finished_at, created_at: d.created_at };
    const succeeded = a._deps.filter((d) => d.status === "succeeded");
    return send(res, 200, { app: publicApp(tid, a), live_release: rel(live, live?.number ?? 0, "live") ?? null, previous_release: rel(succeeded[1], succeeded[1]?.number ?? 0, "standby") ?? null, domains: a._hosts.map(dom), processes: [{ name: "web", command: "node server.js" }] });
  }
  r = /^apps\/([^/]+)\/deployments$/.exec(rest);
  if (r && method === "GET") {
    const a = apps[tid].find((x) => x.id === r[1] || x.name === r[1]);
    return a ? send(res, 200, { deployments: a._deps }) : err(res, 404, "not_found", "not found");
  }
  r = /^apps\/([^/]+)\/deployments\/([^/]+)$/.exec(rest);
  if (r && method === "GET") {
    const a = apps[tid].find((x) => x.id === r[1] || x.name === r[1]);
    const d = a?._deps.find((x) => x.id === r[2]);
    if (!d) return err(res, 404, "not_found", "not found");
    const lines = [
      "Cloning github.com/example/" + a.name + " (branch main)",
      "Detected runtime: node 22",
      "npm ci --omit=dev",
      "added 312 packages in 9s",
      "Building image registry.example.com/" + a.name + ":" + d.number,
      "Image built in 31s",
      "Starting release on ephemeral port",
      d.status === "failed" ? "GET /healthz -> 502 (attempt 5/5)" : "GET /healthz -> 200",
      d.status === "failed" ? "health gate failed: release not promoted" : "Switching traffic to the new release",
      ...(d.status === "succeeded" ? ["Draining old release", "Done"] : []),
      ...(d.status === "running" ? ["Waiting for health gate..."] : []),
    ];
    return send(res, 200, { deployment: d, logs: lines.map((line, i) => ({ seq: i + 1, event: "log", stream: d.status === "failed" && i >= 7 ? "error" : "stdout", line })) });
  }
  // mutating app actions: change this process's memory only
  r = /^apps\/([^/]+)\/(deployments|restart|power)$/.exec(rest);
  if (r && method === "POST") {
    const a = apps[tid].find((x) => x.id === r[1] || x.name === r[1]);
    if (!a) return err(res, 404, "not_found", "not found");
    if (!canOperate) return err(res, 403, "forbidden", "your role cannot do this");
    const b = await readBody(req);
    calls.push({ method, path, team: team.slug, body: b });
    if (r[2] === "power") {
      a.status = b.action === "stop" ? "stopped" : "running";
      return send(res, 202, { queued: true });
    }
    const d = dep(a.id, (a._deps[0]?.number ?? 0) + 1, "running", { ago: 0, kind: r[2] === "restart" ? "restart" : "deploy", msg: r[2] === "restart" ? "Restart" : "Manual redeploy" });
    a._deps.unshift(d);
    setTimeout(() => { d.status = "succeeded"; d.finished_at = iso(NOW()); a.status = "running"; }, 4000);
    return send(res, 201, d);
  }

  if (rest === "domains" && method === "GET") return send(res, 200, { domains: apps[tid].flatMap((a) => a._hosts.map((h) => ({ ...dom(h), app_id: a.id, app_name: a.name, server_id: a.server_id }))) });
  if (rest === "certificates" && method === "GET") return send(res, 200, { certificates: certs[tid] });
  if (rest === "data-services" && method === "GET") return send(res, 200, { services: services[tid] });
  r = /^data-services\/([^/]+)\/backups$/.exec(rest);
  if (r && method === "GET") return send(res, 200, { backups: backupRows(r[1]) });

  if (rest === "notifications" && method === "GET") return send(res, 200, payload(tid));
  if (rest === "notifications/read" && method === "POST") { notifications[tid].forEach((n) => (n.read_at ??= iso(NOW()))); broadcast(tid); return send(res, 204); }
  r = /^notifications\/([^/]+)\/read$/.exec(rest);
  if (r && method === "POST") { const n = notifications[tid].find((x) => x.id === r[1]); if (n) n.read_at ??= iso(NOW()); broadcast(tid); return send(res, 204); }
  if (rest === "notifications/stream" && method === "GET") {
    res.writeHead(200, { "Content-Type": "text/event-stream", "Cache-Control": "no-cache", Connection: "keep-alive" });
    res.write(`retry: 3000\nevent: notifications\ndata: ${JSON.stringify(payload(tid))}\n\n`);
    const set = streams.get(tid) ?? new Set();
    streams.set(tid, set);
    set.add(res);
    const ka = setInterval(() => res.write(": keep-alive\n\n"), 15_000);
    req.on("close", () => { clearInterval(ka); set.delete(res); });
    return;
  }
  return err(res, 404, "not_found", `no mock for ${method} ${path}`);
});

server.listen(PORT, "127.0.0.1", () => console.log(`[mock-api] http://127.0.0.1:${PORT}  (demo@example.com / demo-pass)`));
