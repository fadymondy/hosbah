"use client"

import { Button, Skeleton, toast, useNasaq } from "@fadymondy/nasaq/web"
import "@xyflow/react/dist/style.css"
import "./canvas.css"

import Link from "next/link"
import { useRouter } from "next/navigation"
import { useCallback, useContext, useEffect, useMemo, useRef, useState, type KeyboardEvent } from "react"
import {
  Background,
  BackgroundVariant,
  ControlButton,
  Controls,
  MarkerType,
  MiniMap,
  Panel,
  ReactFlow,
  ReactFlowProvider,
  useEdgesState,
  useNodesState,
  useReactFlow,
  type Connection,
  type Edge,
  type NodeChange,
} from "@xyflow/react"
import { LayoutGridIcon, PlusIcon, RotateCcwIcon, ServerIcon } from "lucide-react"

import { RemoveDomain } from "@/components/apps/domains"
import { RemoveEnvVar } from "@/components/apps/env"
import { useErrorText } from "@/components/form-error"
import { RemoveBrowser } from "@/components/site/browsers"
import { saveCanvas, useCanvasData, type CanvasLayout } from "@/components/site/canvas/data"
import { buildGraph, layeredLayout, placeBoxes, type Box, type Link as GraphLink, type Point, type Removal } from "@/components/site/canvas/model"
import { BoxCard, CanvasCtx, KIND_ICON, LinkLine, StatusDot, type BoxNode, type CanvasActions, type LinkEdge } from "@/components/site/canvas/node"
import { AddForm, Palette, type Choice } from "@/components/site/canvas/palette"
import { useLoadedSite } from "@/components/site/context"
import { DeleteMonitor } from "@/components/site/monitors"
import { attachDataService } from "@/components/site/panels"
import { RemoveRealtime } from "@/components/site/realtime"
import { DeleteSchedule } from "@/components/site/schedules"
import { RemoveInstall } from "@/components/site/stacks"
import { RemoveBucket, RemoveVolume } from "@/components/site/storage"
import { SectionTitle } from "@/components/states"

import { useIsMobile } from "@/hooks/use-mobile"
import { isImported } from "@/lib/apps"
import { useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

const nodeTypes = { box: BoxCard }
const edgeTypes = { link: LinkLine }
/** Wait this long after the last drag or pan before saving where things are. */
const SAVE_DELAY = 800
/** Fitting keeps clear of the controls (bottom left) and, on wide screens, the minimap (bottom right). */
const fitOptions = (mobile: boolean) => ({ padding: { top: "48px", bottom: "16px", left: "56px", right: mobile ? "16px" : "176px" } as const, maxZoom: 1 })

/**
 * The site's overview canvas: how a request reaches it and everything it is joined to, as boxes
 * that open their section, drawn left to right in both languages (time and traffic flow that way).
 * Owners and admins add components from a palette, connect a database by dragging from the app,
 * and remove things through each section's own confirmation; where the boxes sit is saved per
 * app. Everyone else gets the same canvas, read-only: they may arrange their view, which is not
 * saved. Below 768px the same components are also listed plainly under the canvas.
 */
export function SiteCanvas() {
  const { t, locale } = useI18n()
  const { team, manage } = useTeam()
  const errorText = useErrorText()
  const { detail, app, deployments, path, reload: reloadSite } = useLoadedSite()
  const imported = isImported(app)
  const mobile = useIsMobile()
  const { data, reload } = useCanvasData(team.slug, path, app.server_id, imported)
  const base = `/${locale}/t/${team.slug}`
  const root = `${base}/apps/${encodeURIComponent(app.name)}`
  const editable = manage && !!data?.canSave
  const [palette, setPalette] = useState(false)
  const [adding, setAdding] = useState<Choice | null>(null)
  const [removing, setRemoving] = useState<Removal | null>(null)

  const graph = useMemo(
    () =>
      data
        ? buildGraph({
            app,
            detail,
            lastDeploy: deployments[0],
            imported,
            root,
            services: data.services,
            connections: data.connections,
            volumes: data.volumes,
            buckets: data.buckets,
            browsers: data.browsers,
            installs: data.installs,
            realtime: data.realtime,
            monitors: data.monitors,
            schedules: data.schedules,
            t,
          })
        : null,
    [data, app, detail, deployments, imported, root, t],
  )

  // After a change: the canvas's own data, and the page's (domains live there).
  const refresh = useCallback(async () => {
    reloadSite()
    await reload()
  }, [reload, reloadSite])

  const connectDb = useCallback(
    async (serviceId: string) => {
      const s = data?.services.find((x) => x.id === serviceId)
      if (!s) return
      try {
        toast.success(t("site.attached", { variable: await attachDataService(team.slug, s, app.name) }))
        await reload()
      } catch (err) {
        toast.error(errorText(err))
      }
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [data, team.slug, app.name, reload],
  )

  const actions = useMemo<CanvasActions>(
    () => ({
      manage: editable,
      openPalette: () => setPalette(true),
      remove: setRemoving,
      labels: { add: t("site.canvas.addComponent"), remove: t("site.storage.remove"), disconnect: t("site.canvas.disconnect") },
    }),
    [editable, t],
  )

  const hosts = detail.domains.map((d) => `${d.tls === "off" ? "http" : "https"}://${d.hostname}`)
  const listed = useMemo(() => {
    if (!graph) return []
    const pos = layeredLayout(graph.boxes, graph.links)
    return [...graph.boxes].sort((a, b) => pos[a.id].x - pos[b.id].x || pos[a.id].y - pos[b.id].y)
  }, [graph])

  return (
    <section aria-labelledby="flow-h">
      <SectionTitle
        id="flow-h"
        title={t("site.flow.title")}
        hint={editable ? t("site.canvas.hint") : t("site.canvas.hintReadOnly")}
        action={
          <Button variant="ghost" size="sm" nativeButton={false} render={<Link href={`${base}/servers/${app.server_id}/sites/${app.id}`} />}>
            <LayoutGridIcon />
            {t("sites.canvas")}
          </Button>
        }
      />
      <CanvasCtx.Provider value={actions}>
        <div className="border-b border-border" data-testid="site-flow">
          {graph && data ? (
            <ReactFlowProvider>
              <Flow
                key={app.id}
                boxes={graph.boxes}
                links={graph.links}
                saved={data.saved}
                editable={editable}
                path={path}
                mobile={mobile}
                onConnectDb={connectDb}
              />
            </ReactFlowProvider>
          ) : (
            <Skeleton className="h-[440px] w-full rounded-none md:h-[520px]" />
          )}
        </div>
      </CanvasCtx.Provider>

      {listed.length ? (
        <ul className="flex flex-col divide-y divide-border border-b border-border md:hidden" aria-label={t("site.canvas.list")}>
          {listed.map((b) => {
            const Icon = KIND_ICON[b.data.kind]
            return (
              <li key={b.id}>
                <Link href={b.data.href} className="flex items-center gap-3 px-6 py-3 hover:bg-secondary">
                  <Icon aria-hidden className="size-4 shrink-0 text-nq-brand" />
                  <span className="min-w-0 flex-1">
                    <span dir="auto" className="eyebrow block truncate">
                      {b.data.label}
                    </span>
                    <span dir="auto" className="block truncate text-sm text-foreground">
                      {b.data.title}
                    </span>
                  </span>
                  {b.data.status ? (
                    <span className="flex shrink-0 items-center gap-1.5 text-xs text-muted-foreground">
                      <StatusDot status={b.data.status} label={null} />
                      {b.data.statusLabel}
                    </span>
                  ) : null}
                </Link>
              </li>
            )
          })}
        </ul>
      ) : null}

      <Link href={`${base}/servers/${app.server_id}`} className="flex items-center gap-2 border-b border-border px-6 py-3 text-sm text-nq-fg-body hover:bg-secondary">
        <ServerIcon className="size-4 text-nq-brand" />
        {t("site.flow.runsOn")}
        <span dir="ltr" className="font-medium text-foreground">
          {app.server_name ?? app.server_id}
        </span>
      </Link>

      {editable ? (
        <Palette
          open={palette}
          onOpenChange={setPalette}
          onChoose={setAdding}
          imported={imported}
          hasMinio={!!data?.services.some((s) => s.kind === "minio" && !isImported(s))}
          hasRealtime={!!data?.realtime?.config}
          team={team.slug}
          newSiteBase={`${base}/apps/new`}
          mobile={mobile}
        />
      ) : null}
      {editable && adding && data ? (
        <AddForm
          choice={adding}
          onClose={() => setAdding(null)}
          onSaved={refresh}
          path={path}
          team={team.slug}
          appName={app.name}
          hosts={hosts}
          services={data.services}
          connected={new Set(data.connections.map((c) => c.service_id))}
          domainPort={imported ? (app.origin_kind === "guest" ? { required: true } : { required: false }) : undefined}
          serverId={app.server_id}
          serversDatabases={`${base}/servers/${app.server_id}/databases`}
        />
      ) : null}
      {editable && removing ? <RemoveForm removal={removing} path={path} onClose={() => setRemoving(null)} onRemoved={refresh} /> : null}
    </section>
  )
}

/** The matching removal of a box or edge: the section's own dialog, with its own confirmation rules. */
function RemoveForm({ removal: r, path, onClose, onRemoved }: { removal: Removal; path: string; onClose: () => void; onRemoved: () => Promise<void> }) {
  const c = { open: true, onOpenChange: (v: boolean) => !v && onClose() }
  switch (r.kind) {
    case "domain":
      return <RemoveDomain {...c} path={path} domain={r.domain} onRemoved={onRemoved} />
    case "detach":
      return <RemoveEnvVar {...c} path={path} name={r.variable} onRemoved={onRemoved} />
    case "volume":
      return <RemoveVolume {...c} path={path} volume={r.volume} onRemoved={onRemoved} />
    case "bucket":
      return <RemoveBucket {...c} path={path} bucket={r.bucket} onRemoved={onRemoved} />
    case "browser":
      return <RemoveBrowser {...c} path={path} browser={r.browser} onRemoved={onRemoved} />
    case "install":
      return <RemoveInstall {...c} path={path} install={r.install} onRemoved={onRemoved} />
    case "monitor":
      return <DeleteMonitor {...c} path={path} monitor={r.monitor} onDeleted={onRemoved} />
    case "schedule":
      return <DeleteSchedule {...c} path={path} schedule={r.schedule} onDeleted={onRemoved} />
    case "realtime":
      return <RemoveRealtime {...c} path={path} onRemoved={onRemoved} />
  }
}

function toNode(b: Box, position: Point, selected = false): BoxNode {
  const d = b.data
  return {
    id: b.id,
    type: "box",
    position,
    data: d,
    selected,
    deletable: false,
    ariaLabel: [d.label, d.title, d.statusLabel].filter(Boolean).join(" — "),
  }
}

function toEdges(links: GraphLink[], selected: Set<string> = new Set()): LinkEdge[] {
  return links.map((l) => ({
    id: l.id,
    source: l.source,
    target: l.target,
    type: "link",
    animated: l.traffic,
    selectable: !!l.removal,
    deletable: false,
    selected: selected.has(l.id),
    data: { removal: l.removal },
    markerEnd: { type: MarkerType.ArrowClosed, width: 16, height: 16, color: l.traffic ? "var(--nq-brand)" : "var(--nq-fg-muted)" },
  }))
}

const clamp = (v: number, lim: number) => Math.max(-lim, Math.min(lim, Math.round(v)))

function Flow({
  boxes,
  links,
  saved,
  editable,
  path,
  mobile,
  onConnectDb,
}: {
  boxes: Box[]
  links: GraphLink[]
  saved: CanvasLayout | null
  editable: boolean
  path: string
  mobile: boolean
  onConnectDb: (serviceId: string) => void
}) {
  const { t } = useI18n()
  const router = useRouter()
  const { resolvedTheme } = useNasaq()
  const rf = useReactFlow<BoxNode, LinkEdge>()
  const actions = useCanvas()
  const savedPos = useRef<Record<string, Point>>(saved?.nodes ?? {})
  const [initialNodes] = useState(() => {
    const pos = placeBoxes(boxes, links, savedPos.current)
    return boxes.map((b) => toNode(b, pos[b.id]))
  })
  const [nodes, setNodes, onNodesChange] = useNodesState<BoxNode>(initialNodes)
  const [edges, setEdges, onEdgesChange] = useEdgesState<LinkEdge>(toEdges(links))
  const [connecting, setConnecting] = useState(false)

  // Live data, additions and removals: boxes stay where they are, new ones go next to a neighbour.
  useEffect(() => {
    setNodes((prev) => {
      const current = Object.fromEntries(prev.map((n) => [n.id, n.position]))
      const selected = new Set(prev.filter((n) => n.selected).map((n) => n.id))
      const pos = placeBoxes(boxes, links, savedPos.current, current)
      return boxes.map((b) => toNode(b, pos[b.id], selected.has(b.id)))
    })
    setEdges((prev) => toEdges(links, new Set(prev.filter((e) => e.selected).map((e) => e.id))))
  }, [boxes, links, setNodes, setEdges])

  // Saving: debounced after the last move; whatever is pending is sent when the canvas goes away.
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const flush = useCallback(() => {
    timer.current = null
    const layout: CanvasLayout = {
      nodes: Object.fromEntries(rf.getNodes().map((n) => [n.id, { x: clamp(n.position.x, 100_000), y: clamp(n.position.y, 100_000) }])),
    }
    const vp = rf.getViewport()
    layout.viewport = { x: clamp(vp.x, 1_000_000), y: clamp(vp.y, 1_000_000), zoom: Math.min(4, Math.max(0.05, Math.round(vp.zoom * 1000) / 1000)) }
    saveCanvas(path, layout)
      .then(() => {
        savedPos.current = layout.nodes
      })
      .catch(() => toast.error(t("site.canvas.saveFailed")))
  }, [rf, path, t])
  const scheduleSave = useCallback(() => {
    if (!editable) return
    if (timer.current) clearTimeout(timer.current)
    timer.current = setTimeout(flush, SAVE_DELAY)
  }, [editable, flush])
  useEffect(
    () => () => {
      if (timer.current) {
        clearTimeout(timer.current)
        flush()
      }
    },
    [flush],
  )

  const onNodesChangeSaving = useCallback(
    (changes: NodeChange<BoxNode>[]) => {
      onNodesChange(changes)
      // A drag's last change, or a keyboard move (arrow keys), is a move to keep.
      if (changes.some((c) => c.type === "position" && !c.dragging && c.position)) scheduleSave()
    },
    [onNodesChange, scheduleSave],
  )

  function resetLayout() {
    const pos = layeredLayout(boxes, links)
    setNodes((prev) => prev.map((n) => ({ ...n, position: pos[n.id] ?? n.position })))
    requestAnimationFrame(() => void rf.fitView({ ...fitOptions(mobile), duration: 300 }))
    scheduleSave()
  }

  const isValidConnection = useCallback(
    (c: Connection | Edge) => {
      if (!editable || c.source !== "app" || !c.target.startsWith("db:")) return false
      return rf.getNode(c.target)?.data.connect === "target"
    },
    [editable, rf],
  )

  function onKeyDown(e: KeyboardEvent<HTMLDivElement>) {
    const target = e.target as HTMLElement
    if (target.closest("input, textarea, select, [contenteditable]")) return
    const el = target.closest<HTMLElement>(".react-flow__node, .react-flow__edge")
    const id = el?.dataset.id
    if (!el || !id) return
    const del = e.key === "Delete" || e.key === "Backspace"
    if (el.classList.contains("react-flow__node")) {
      const node = rf.getNode(id)
      if (!node) return
      if (e.key === "Enter") {
        e.preventDefault()
        router.push(node.data.href)
      } else if (del && editable && node.data.removal) {
        e.preventDefault()
        actions.remove(node.data.removal)
      }
    } else if (del && editable) {
      const removal = rf.getEdge(id)?.data?.removal
      if (removal) {
        e.preventDefault()
        actions.remove(removal)
      }
    }
  }

  const viewport = saved?.viewport ?? undefined

  return (
    <div dir="ltr" className={`hosbah-canvas relative h-[440px] w-full md:h-[520px]${connecting ? " is-connecting" : ""}`} onKeyDown={onKeyDown}>
      <ReactFlow<BoxNode, LinkEdge>
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        edgeTypes={edgeTypes}
        onNodesChange={onNodesChangeSaving}
        onEdgesChange={onEdgesChange}
        onNodeClick={(_, n) => router.push(n.data.href)}
        onMoveEnd={(e) => {
          if (e) scheduleSave()
        }}
        onConnect={(c) => {
          if (c.target?.startsWith("db:")) onConnectDb(c.target.slice(3))
        }}
        onConnectStart={() => setConnecting(true)}
        onConnectEnd={() => setConnecting(false)}
        isValidConnection={isValidConnection}
        nodesConnectable={editable}
        deleteKeyCode={null}
        colorMode={resolvedTheme === "dark" ? "dark" : "light"}
        defaultViewport={viewport}
        fitView={!viewport}
        fitViewOptions={fitOptions(mobile)}
        minZoom={0.2}
        maxZoom={2}
        attributionPosition="top-left"
        ariaLabelConfig={{
          "node.a11yDescription.default": editable ? t("site.canvas.a11y.node") : t("site.canvas.a11y.nodeReadOnly"),
          "node.a11yDescription.keyboardDisabled": t("site.canvas.a11y.node"),
          "edge.a11yDescription.default": t("site.canvas.a11y.edge"),
          "controls.ariaLabel": t("site.canvas.a11y.controls"),
          "controls.zoomIn.ariaLabel": t("site.canvas.zoomIn"),
          "controls.zoomOut.ariaLabel": t("site.canvas.zoomOut"),
          "controls.fitView.ariaLabel": t("site.canvas.fitView"),
          "minimap.ariaLabel": t("site.canvas.minimap"),
          "handle.ariaLabel": t("site.canvas.a11y.handle"),
        }}
        aria-label={t("site.canvas.label")}
      >
        <Background variant={BackgroundVariant.Dots} gap={20} size={1.2} />
        <Controls showInteractive={false} position="bottom-left">
          <ControlButton onClick={resetLayout} title={t("site.canvas.resetLayout")} aria-label={t("site.canvas.resetLayout")}>
            <RotateCcwIcon />
          </ControlButton>
        </Controls>
        {mobile ? null : (
          <MiniMap pannable zoomable position="bottom-right" nodeBorderRadius={0} style={{ width: 144, height: 96 }} ariaLabel={t("site.canvas.minimap")} />
        )}
        {editable ? (
          <Panel position="top-right">
            <Button variant="primary" size="sm" onClick={actions.openPalette}>
              <PlusIcon />
              <span dir="auto">{t("site.canvas.addComponent")}</span>
            </Button>
          </Panel>
        ) : null}
      </ReactFlow>
    </div>
  )
}

function useCanvas() {
  const v = useContext(CanvasCtx)
  if (!v) throw new Error("canvas actions outside CanvasCtx")
  return v
}
