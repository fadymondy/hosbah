import { runtimeLabel, shortSha, type App, type AppDetail, type Deployment } from "@/lib/apps"
import type { DataService } from "@/lib/databases"
import type { Browser } from "@/components/site/browsers"
import { attachable } from "@/components/site/panels"
import type { Monitor } from "@/components/site/monitors"
import type { RealtimeStatus } from "@/components/site/realtime"
import type { Schedule } from "@/components/site/schedules"
import type { Install } from "@/components/site/stacks"
import type { Bucket, Volume } from "@/components/site/storage"

/*
The site overview canvas as data: which boxes there are, what joins them, and where they go the
first time. Pure functions; canvas.tsx draws the result with React Flow.

Node keys are stable across reloads ("app", "domains", "db:<service id>", "worker:<name>",
…): they are what saved positions are stored under.
*/

export type Status = "ok" | "warn" | "error" | "busy" | "off"

/** The site's one domains box (a stack of cards, not one node per hostname — see BoxData.domains). */
export const DOMAINS_BOX_ID = "domains"

export type NodeKind =
  | "visitors"
  | "domain"
  | "caddy"
  | "app"
  | "worker"
  | "realtime"
  | "db"
  | "volume"
  | "bucket"
  | "browser"
  | "install"
  | "monitor"
  | "schedule"

/** What a box's removal does (canvas.tsx opens the matching dialog of that section). */
export type Removal =
  | { kind: "domain"; domain: AppDetail["domains"][number] }
  | { kind: "detach"; variable: string; service: DataService }
  | { kind: "volume"; volume: Volume }
  | { kind: "bucket"; bucket: Bucket }
  | { kind: "browser"; browser: Browser }
  | { kind: "install"; install: Install }
  | { kind: "monitor"; monitor: Monitor }
  | { kind: "schedule"; schedule: Schedule }
  | { kind: "realtime" }

export type BoxData = {
  kind: NodeKind
  /** The box's small caps label: what it is. */
  label: string
  /** Its name. */
  title: string
  /** Facts, shown in monospace. */
  lines: string[]
  status: Status | null
  statusLabel: string | null
  /** The section that manages it. */
  href: string
  removal: Removal | null
  /** The app is where connections start; a database not connected yet is where one may end. */
  connect: "source" | "target" | null
  /** Shown but not part of the site yet (a database on the server it is not connected to). */
  dimmed: boolean
  /** Its column when it has no edge to place it by. */
  rank?: number
  /** Traffic path boxes sit on the centre line of their column; the rest go below them. */
  primary: boolean
  /**
   * kind "domain" only: every domain this box stands for. There is one "domains" box for the
   * whole site (not one per hostname) so it can draw as a stack of offset cards — the front card
   * is domains[0], and the rest expand into a list on hover/click. Undefined for every other kind.
   */
  domains?: AppDetail["domains"]
}

export type Box = { id: string; data: BoxData }
export type Link = { id: string; source: string; target: string; traffic: boolean; removal: Removal | null; label: string }

export type Point = { x: number; y: number }

export type CanvasInputs = {
  app: App
  detail: AppDetail
  lastDeploy: Deployment | undefined
  imported: boolean
  /** /{locale}/t/{team}/apps/{name} */
  root: string
  services: DataService[]
  connections: { service_id: string; variable: string }[]
  volumes: Volume[]
  buckets: Bucket[]
  browsers: Browser[]
  installs: Install[]
  realtime: RealtimeStatus | null
  monitors: Monitor[]
  schedules: Schedule[]
  t: (key: string, vars?: Record<string, string | number>, fallback?: string) => string
}

/** Every box and edge of the site, from what the page and the sections' APIs say. */
export function buildGraph(i: CanvasInputs): { boxes: Box[]; links: Link[] } {
  const { t, detail, app, root } = i
  const boxes: Box[] = []
  const links: Link[] = []
  const st = (s: Status | null, label?: string | null): Pick<BoxData, "status" | "statusLabel"> => ({
    status: s,
    statusLabel: s ? (label ?? t(`site.canvas.status.${s}`)) : null,
  })
  const box = (id: string, d: Omit<BoxData, "status" | "statusLabel" | "dimmed" | "connect" | "removal" | "primary"> & Partial<BoxData>) =>
    boxes.push({ id, data: { status: null, statusLabel: null, dimmed: false, connect: null, removal: null, primary: false, ...d } })
  const link = (source: string, target: string, traffic = false, removal: Removal | null = null) =>
    links.push({ id: `${source}->${target}`, source, target, traffic, removal, label: "" })

  // Traffic: visitors → the site's domains (one stacked-card box for all of them) → Caddy → the app.
  box("visitors", { kind: "visitors", label: t("site.flow.visitors"), title: t("site.canvas.internet"), lines: [], href: `${root}/monitors`, primary: true })
  if (detail.domains.length > 0) {
    const first = detail.domains[0]
    const allVerified = detail.domains.every((d) => d.verified)
    box(DOMAINS_BOX_ID, {
      kind: "domain",
      label: t("site.canvas.kinds.domain"),
      title: first.hostname,
      lines: detail.domains.length === 1 ? [`TLS ${t(`apps.domains.tls.${first.tls}`)}`] : [],
      ...st(allVerified ? "ok" : "warn", allVerified ? t("apps.domains.verified") : t("apps.domains.unverified")),
      href: `${root}/dns`,
      removal: detail.domains.length === 1 ? { kind: "domain", domain: first } : null,
      domains: detail.domains,
      primary: true,
    })
    link("visitors", DOMAINS_BOX_ID, true)
    link(DOMAINS_BOX_ID, "caddy", true)
  } else {
    link("visitors", "caddy", false)
  }
  box("caddy", {
    kind: "caddy",
    label: t("site.flow.proxy"),
    title: detail.domains.length ? t("site.flow.hosts", { n: detail.domains.length }) : t("site.canvas.noDomain"),
    lines: [],
    href: `${root}/proxy`,
    primary: true,
  })
  link("caddy", "app", detail.domains.length > 0)

  const live = detail.live_release
  const last = i.lastDeploy
  let appStatus: Status | null = null
  let appStatusLabel: string | null = null
  if (i.imported) {
    appStatus = app.imported_state === "running" ? "ok" : app.imported_state === "missing" ? "error" : "off"
    appStatusLabel = app.imported_state ? t(`imported.states.${app.imported_state}`, undefined, app.imported_state) : null
  } else if (last && (last.status === "queued" || last.status === "running")) {
    appStatus = "busy"
    appStatusLabel = t(`apps.deploy.statuses.${last.status}`, undefined, last.status)
  } else if (live) {
    appStatus = last?.status === "failed" ? "warn" : "ok"
  } else {
    appStatus = last?.status === "failed" ? "error" : "off"
    appStatusLabel = t("apps.noLive")
  }
  box("app", {
    kind: "app",
    label: t("site.flow.app"),
    title: app.title || app.name,
    lines: i.imported
      ? [app.name, app.origin_kind ?? ""].filter(Boolean)
      : [live ? `v${live.version} · ${shortSha(live.commit_sha)}` : t("apps.noLive"), [runtimeLabel(app.runtime) ?? "", `:${app.port}`].filter(Boolean).join(" · ")],
    ...st(appStatus, appStatusLabel),
    href: i.imported ? `${root}/settings` : `${root}/deployments`,
    connect: i.imported ? null : "source",
    primary: true,
  })

  if (!i.imported) {
    // Workers: the processes besides web.
    for (const p of detail.processes) {
      if (p.name === "web") continue
      const name = p.name.replace(/^worker:/, "")
      const id = `worker:${name}`
      box(id, { kind: "worker", label: t("site.canvas.kinds.worker"), title: name, lines: p.command ? [p.command] : [], href: `${root}/workers` })
      link("app", id)
    }

    const rt = i.realtime?.config
    if (rt) {
      const s: Status = !rt.enabled ? "off" : i.realtime?.routed && i.realtime.process_running ? "ok" : "warn"
      box("realtime", {
        kind: "realtime",
        label: t("site.nav.realtime"),
        title: t(`site.realtime.kinds.${rt.kind}`),
        lines: [`${rt.process} :${rt.port}`, rt.path],
        ...st(s, !rt.enabled ? t("site.canvas.status.off") : i.realtime?.routed ? t("site.realtime.routed") : t("site.realtime.notRouted")),
        href: `${root}/realtime`,
        removal: { kind: "realtime" },
      })
      link("app", "realtime", false, { kind: "realtime" })
    }

    // Data services on the server: connected ones are joined to the app; databases and caches that
    // are not yet are shown, dimmed, as places a connection can be dragged to.
    const connected = new Map(i.connections.map((c) => [c.service_id, c.variable]))
    for (const s of i.services) {
      const variable = connected.get(s.id)
      if (!variable && !attachable(s)) continue
      const id = `db:${s.id}`
      const status: Status = s.status === "running" ? "ok" : s.status === "failed" ? "error" : s.status === "stopped" ? "off" : "busy"
      const removal: Removal | null = variable && s.kind !== "minio" ? { kind: "detach", variable, service: s } : null
      box(id, {
        kind: "db",
        label: t("site.canvas.kinds.db"),
        title: s.name,
        lines: [`${s.kind} ${s.version}`, variable ?? t("site.canvas.notConnected")],
        ...st(status, t(`databases.statuses.${s.status}`, undefined, s.status)),
        href: `${root}/databases`,
        removal,
        connect: variable ? null : "target",
        dimmed: !variable,
        rank: 4,
      })
      if (variable) link("app", id, false, removal)
    }

    for (const v of i.volumes) {
      const id = `volume:${v.id}`
      const removal: Removal = { kind: "volume", volume: v }
      box(id, {
        kind: "volume",
        label: t("site.canvas.kinds.volume"),
        title: v.name,
        lines: [v.mount_path],
        ...st(v.mounted ? "ok" : "warn", v.mounted ? t("site.storage.mounted") : t("site.storage.nextDeploy")),
        href: `${root}/storage`,
        removal,
      })
      link("app", id, false, removal)
    }
    for (const b of i.buckets) {
      const id = `bucket:${b.id}`
      const removal: Removal = { kind: "bucket", bucket: b }
      box(id, {
        kind: "bucket",
        label: t("site.canvas.kinds.bucket"),
        title: b.bucket,
        lines: [b.service, `${b.env_prefix}_*`],
        ...st(itemStatus(b.status, null), t(`site.storage.status.${b.status}`)),
        href: `${root}/storage`,
        removal,
      })
      link("app", id, false, removal)
    }
    for (const b of i.browsers) {
      const id = `browser:${b.id}`
      const removal: Removal = { kind: "browser", browser: b }
      box(id, {
        kind: "browser",
        label: t("site.canvas.kinds.browser"),
        title: b.name,
        lines: [`${b.memory_mb} MB · ${b.cpus} CPU`],
        ...st(itemStatus(b.status, b.running), runLabel(t, b.status, b.running)),
        href: `${root}/browsers`,
        removal,
      })
      link("app", id, false, removal)
    }
    for (const x of i.installs) {
      const id = `install:${x.id}`
      const removal: Removal = { kind: "install", install: x }
      box(id, {
        kind: "install",
        label: t("site.canvas.kinds.install"),
        title: x.name,
        lines: [`${x.recipe_name} ${x.recipe_version}`, x.hostname ?? ""].filter(Boolean),
        ...st(itemStatus(x.status, x.running), runLabel(t, x.status, x.running)),
        href: `${root}/stacks`,
        removal,
      })
      link("app", id, false, removal)
    }
    for (const s of i.schedules) {
      const id = `schedule:${s.id}`
      const removal: Removal = { kind: "schedule", schedule: s }
      const last = s.last_run?.status ?? s.last_status
      const status: Status | null = !s.enabled
        ? "off"
        : last === "succeeded"
          ? "ok"
          : last === "failed" || last === "timeout"
            ? "error"
            : last === "running" || last === "queued"
              ? "busy"
              : last === "skipped"
                ? "warn"
                : null
      box(id, {
        kind: "schedule",
        label: t("site.canvas.kinds.schedule"),
        title: s.name,
        lines: [s.cron, s.command],
        ...st(status, !s.enabled ? t("site.monitors.paused") : last ? t(`site.schedules.status.${last}`) : null),
        href: `${root}/schedules`,
        removal,
      })
      link(id, "app", false, removal)
    }
  }

  // Monitors watch a domain (an http check of one of the site's hostnames) or else the app.
  for (const m of i.monitors) {
    const id = `monitor:${m.id}`
    const removal: Removal = { kind: "monitor", monitor: m }
    let target = "app"
    if (m.kind === "http") {
      const host = hostOf(m.target)
      if (host && detail.domains.some((d) => d.hostname === host)) target = DOMAINS_BOX_ID
    }
    const status: Status | null = !m.enabled ? "off" : m.state === "failing" ? "error" : m.state === "ok" ? "ok" : null
    box(id, {
      kind: "monitor",
      label: t("site.canvas.kinds.monitor"),
      title: m.name,
      lines: [m.kind === "http" ? m.target : t(`site.monitors.kinds.${m.kind}`)],
      ...st(status, !m.enabled ? t("site.monitors.paused") : t(`site.monitors.state.${m.state}`)),
      href: `${root}/monitors`,
      removal,
    })
    link(id, target, false, removal)
  }

  return { boxes, links }
}

function itemStatus(status: "creating" | "ready" | "failed" | "removing", running: boolean | null): Status {
  if (status === "failed") return "error"
  if (status !== "ready") return "busy"
  return running === false ? "warn" : "ok"
}

function runLabel(t: CanvasInputs["t"], status: "creating" | "ready" | "failed" | "removing", running: boolean | null) {
  if (status === "ready" && running != null) return running ? t("site.browsers.running") : t("site.browsers.stopped")
  return t(`site.storage.status.${status}`)
}

function hostOf(url: string) {
  try {
    return new URL(url).hostname.toLowerCase()
  } catch {
    return null
  }
}

// ---- layout ------------------------------------------------------------------------------------

export const BOX_W = 216
const COL_GAP = 96
const ROW_GAP = 24
const SECONDARY_GAP = 48

/** A box's height before React Flow has measured it: the label row, the title and its facts. */
export function boxHeight(b: Box) {
  return 62 + 17 * b.data.lines.length
}

/**
 * A left-to-right layered layout. Columns are the longest path from the boxes nothing points at,
 * except that such a box sits just left of whatever it points at (a monitor next to its domain, a
 * schedule next to the app); a box with no edge takes its rank hint. Within a column, boxes are
 * ordered by where their neighbours in the previous column are (two sweeps), and the traffic path
 * is centred on the app's line with everything else below it.
 */
export function layeredLayout(boxes: Box[], links: Link[]): Record<string, Point> {
  const ids = new Set(boxes.map((b) => b.id))
  const edges = links.filter((l) => ids.has(l.source) && ids.has(l.target) && l.source !== l.target)
  const out = new Map<string, string[]>()
  const inc = new Map<string, string[]>()
  for (const id of ids) {
    out.set(id, [])
    inc.set(id, [])
  }
  for (const e of edges) {
    out.get(e.source)!.push(e.target)
    inc.get(e.target)!.push(e.source)
  }

  // Longest path, in topological order (a cycle's boxes keep rank 0).
  const rank = new Map<string, number>()
  const indeg = new Map([...ids].map((id) => [id, inc.get(id)!.length]))
  const queue = [...ids].filter((id) => indeg.get(id) === 0)
  for (const id of ids) rank.set(id, 0)
  while (queue.length) {
    const id = queue.shift()!
    for (const n of out.get(id)!) {
      rank.set(n, Math.max(rank.get(n)!, rank.get(id)! + 1))
      indeg.set(n, indeg.get(n)! - 1)
      if (indeg.get(n) === 0) queue.push(n)
    }
  }
  const hint = new Map(boxes.map((b) => [b.id, b.data.rank]))
  for (const id of ids) {
    const targets = out.get(id)!
    if (inc.get(id)!.length === 0 && targets.length) rank.set(id, Math.min(...targets.map((n) => rank.get(n)!)) - 1)
    if (inc.get(id)!.length === 0 && targets.length === 0) rank.set(id, hint.get(id) ?? 0)
  }
  const low = Math.min(...[...rank.values()])
  for (const [id, r] of rank) rank.set(id, r - low)

  // Columns, then ordering by barycentre.
  const byId = new Map(boxes.map((b) => [b.id, b]))
  const cols: string[][] = []
  for (const b of boxes) (cols[rank.get(b.id)!] ??= []).push(b.id)
  for (let c = 0; c < cols.length; c++) cols[c] ??= []
  const index = new Map<string, number>()
  const reindex = () => cols.forEach((col) => col.forEach((id, n) => index.set(id, n)))
  reindex()
  const neighbours = (id: string) => [...out.get(id)!, ...inc.get(id)!]
  const sweep = (from: number, to: number, step: number) => {
    for (let c = from; c !== to; c += step) {
      const adj = c - step
      const bary = (id: string) => {
        const ns = neighbours(id).filter((n) => rank.get(n) === adj)
        return ns.length ? ns.reduce((a, n) => a + index.get(n)!, 0) / ns.length : index.get(id)!
      }
      const keyed = cols[c].map((id, n) => ({ id, n, b: bary(id), p: byId.get(id)!.data.primary ? 0 : 1, d: byId.get(id)!.data.dimmed ? 1 : 0 }))
      keyed.sort((a, b) => a.p - b.p || a.d - b.d || a.b - b.b || a.n - b.n)
      cols[c] = keyed.map((k) => k.id)
      reindex()
    }
  }
  sweep(1, cols.length, 1)
  sweep(cols.length - 2, -1, -1)

  // Positions: the primary block of each column centred on y = 0, the rest below.
  const pos: Record<string, Point> = {}
  cols.forEach((col, c) => {
    const x = c * (BOX_W + COL_GAP)
    const primary = col.filter((id) => byId.get(id)!.data.primary)
    const rest = col.filter((id) => !byId.get(id)!.data.primary)
    const stack = (list: string[]) => list.reduce((h, id) => h + boxHeight(byId.get(id)!), 0) + ROW_GAP * Math.max(0, list.length - 1)
    let y: number
    if (primary.length) {
      y = -stack(primary) / 2
      for (const id of primary) {
        pos[id] = { x, y }
        y += boxHeight(byId.get(id)!) + ROW_GAP
      }
      y += SECONDARY_GAP - ROW_GAP
    } else {
      y = -stack(rest) / 2
    }
    for (const id of rest) {
      pos[id] = { x, y }
      y += boxHeight(byId.get(id)!) + ROW_GAP
    }
  })
  return pos
}

/**
 * Positions for the boxes: saved ones where there are, and new boxes put next to a neighbour with
 * the offset the automatic layout gives them, moved down while they overlap another box.
 */
export function placeBoxes(boxes: Box[], links: Link[], saved: Record<string, Point>, current: Record<string, Point> = {}): Record<string, Point> {
  const auto = layeredLayout(boxes, links)
  const known = (id: string) => current[id] ?? saved[id]
  if (!boxes.some((b) => known(b.id))) return auto
  const pos: Record<string, Point> = {}
  for (const b of boxes) if (known(b.id)) pos[b.id] = { ...known(b.id)! }
  const byId = new Map(boxes.map((b) => [b.id, b]))
  const overlaps = (id: string, p: Point) =>
    Object.entries(pos).some(([o, q]) => {
      if (o === id) return false
      const h1 = boxHeight(byId.get(id)!)
      const h2 = byId.get(o) ? boxHeight(byId.get(o)!) : 80
      return p.x < q.x + BOX_W && q.x < p.x + BOX_W && p.y < q.y + h2 && q.y < p.y + h1
    })
  // Anchor: any placed neighbour, else the app, else the first placed box.
  const anchorOf = (id: string) => {
    for (const l of links) {
      if (l.source === id && pos[l.target]) return l.target
      if (l.target === id && pos[l.source]) return l.source
    }
    return pos.app ? "app" : Object.keys(pos)[0]
  }
  for (const b of boxes) {
    if (pos[b.id]) continue
    const a = anchorOf(b.id)
    const p = a && auto[a] ? { x: pos[a].x + auto[b.id].x - auto[a].x, y: pos[a].y + auto[b.id].y - auto[a].y } : { ...auto[b.id] }
    for (let n = 0; n < 60 && overlaps(b.id, p); n++) p.y += boxHeight(b) + ROW_GAP
    pos[b.id] = p
  }
  return pos
}
