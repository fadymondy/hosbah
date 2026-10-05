"use client"

import { Button, Checkbox, EmptyState, Field, FieldLabel, Input, type DataTableColumn } from "@fadymondy/nasaq/web"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { useCallback, useEffect, useState } from "react"
import { BoxesIcon, PanelsTopLeftIcon, LayoutDashboardIcon, PencilIcon, PlusIcon, Trash2Icon } from "lucide-react"

import { ServerMark } from "@/components/server-mark"
import { ConfirmButton } from "@/components/confirm-button"
import type { Controlled } from "@/components/dialog-state"
import { NasaqTable } from "@/components/lists/nasaq-table"
import { EditDialog } from "@/components/server-tabs/list-tools"
import { EditUser } from "@/components/server-tabs/edit-user"
import { useLoadedServer } from "@/components/server-tabs/context"




import { AppStatusBadge, DeploymentStatusBadge } from "@/components/app-status"
import { DomainsSummary } from "@/components/apps/domains"
import { ImportedBadge, originSummary } from "@/components/imported"
import { useErrorText } from "@/components/form-error"
import { ErrorBand, LoadingRows, SectionTitle } from "@/components/states"



import { apiFetch } from "@/lib/api"
import { isImported, runtimeLabel, shortSha, type App } from "@/lib/apps"
import { useFormatDate, useI18n } from "@/lib/i18n"
import { formatBytes, type Guest, type Server } from "@/lib/servers"
import { useTeam, useTeamBase } from "@/lib/team"

/** A row of a server's apps: an app record, or an LXC/VM on this node that runs its own agent (its
 * panel is its server page). One list, whatever runs the app. */
type Row = { id: string; app?: App; guest?: Guest }
const guestRunning = (s: string) => /^(running|active|up)/i.test(s)

/** The apps (sites) on one server. */
/** The apps on one server, or (mode "panels") the panels on it: its LXCs/VMs that run their own agent. */
export function ServerApps({ serverId, mode = "apps" }: { serverId: string; mode?: "apps" | "panels" }) {
  const { t, locale } = useI18n()
  const { team, manage } = useTeam()
  const fmt = useFormatDate()
  const errorText = useErrorText()
  const [apps, setApps] = useState<App[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const router = useRouter()
  const [editing, setEditing] = useState<App | null>(null)
  const [editingUser, setEditingUser] = useState<Guest | null>(null)
  const [deleting, setDeleting] = useState<App | null>(null)
  const [nested, setNested] = useState<Record<string, Server>>({})
  const base = useTeamBase()
  const guests = useLoadedServer().server.guests ?? []

  const load = useCallback(async () => {
    try {
      const res = await apiFetch<{ apps: App[] }>(`/api/teams/${team.slug}/apps`)
      setApps((res.apps ?? []).filter((a) => a.server_id === serverId))
      // The LXC/VM apps are servers of their own: their title, colour, icon and logo live there.
      const srv = await apiFetch<{ servers: Server[] }>(`/api/teams/${team.slug}/servers`).catch(() => ({ servers: [] as Server[] }))
      setNested(Object.fromEntries((srv.servers ?? []).filter((x) => x.parent_server_id === serverId).map((x) => [x.id, x])))
      setError(null)
    } catch (err) {
      setError(errorText(err))
      setApps((a) => a ?? [])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [team.slug, serverId])

  useEffect(() => {
    void load()
    const id = setInterval(load, 10_000) // the detail page is live; the list refreshes
    return () => clearInterval(id)
  }, [load])


  const rows: Row[] = [
    ...(mode === "apps" ? (apps ?? []) : []).map((a) => ({ id: a.id, app: a })),
    ...(mode === "panels" ? guests : []).filter((g) => g.server_id).map((g) => ({ id: `guest-${g.server_id}`, guest: g })),
  ]
  const guestHref = (g: Guest) => `/${locale}/panel/${g.server_id}`
  const hrefOf = (r: Row) => (r.guest ? guestHref(r.guest) : `${base}/apps/${r.app!.name}`)
  // The slug is fixed (the guest's name on the node); the title is the server's name, which the user can change.
  const slugOf = (g: Guest) => g.name || `${g.type}-${g.vmid}`
  const nameOf = (r: Row) => (r.app ? r.app.title || r.app.name : nested[r.guest!.server_id!]?.name || slugOf(r.guest!))
  const statusOf = (r: Row) => (r.app ? r.app.status : guestRunning(r.guest!.status) ? "running" : "stopped")

  const add = manage ? (
    <Button variant="primary" nativeButton={false} render={<Link href={`${base}/apps/new?server=${serverId}`} />}>
      <PlusIcon />
      {t("apps.add")}
    </Button>
  ) : null

  return (
    <section aria-labelledby="tab-h">
      {mode === "panels" ? (
        <SectionTitle id="tab-h" title={t("servers.panels.title")} hint={t("servers.panels.hint")} />
      ) : (
        <SectionTitle id="tab-h" title={t("nav.apps")} hint={t("apps.description")} action={add} />
      )}
      <ErrorBand message={error} />
      {apps === null ? (
        <LoadingRows />
      ) : rows.length === 0 ? (
        mode === "panels" ? (
        <EmptyState className="mt-3 rounded-lg border border-border bg-card" icon={PanelsTopLeftIcon} title={t("servers.panels.emptyTitle")} description={t("servers.panels.emptyHint")} />
      ) : (
        <EmptyState className="mt-3 rounded-lg border border-border bg-card" icon={BoxesIcon} title={t("apps.emptyTitle")} description={manage ? t("apps.emptyHint") : t("common.readOnly")} actions={<>{add}</>} />
      )
      ) : (
        <div className="mt-3">
          <NasaqTable
            data={rows}
            rowId={(r) => r.id}
            label={mode === "panels" ? t("servers.panels.title") : t("nav.apps")}
            rowLabel={nameOf}
            search={t("lists.searchApps")}
            empty={t("home.noMatch")}
            facets={[{ column: "status", title: t("apps.status"), options: [...new Set(rows.map(statusOf))].map((v) => ({ value: v, label: t(`apps.statuses.${v}`, undefined, v) })) }]}
            columns={[
              {
                id: "name",
                header: t("apps.name"),
                label: t("apps.name"),
                sortValue: nameOf,
                searchValue: (r) =>
                  r.guest ? `${nameOf(r)} ${slugOf(r.guest)} ${r.guest.vmid} ${r.guest.address ?? ""}` :
                  r.app
                    ? [r.app.name, r.app.title, r.app.branch, r.app.repo_url, r.app.runtime, r.app.status, ...(r.app.domains ?? []).map((d) => d.hostname)].join(" ")
                    : `${r.guest!.name} ${r.guest!.vmid} ${r.guest!.address ?? ""}`,
                cell: ({ app: a, guest: g }) =>
                  !a ? (
                    <span className="flex min-w-0 items-center gap-3">
                      <ServerMark server={nested[g!.server_id!] ?? { color: null, icon: "server", logo_url: null }} size={8} />
                      <span className="min-w-0">
                        <Link href={guestHref(g!)} dir="auto" className="block truncate font-medium underline-offset-4 hover:underline">
                          {nameOf({ id: "", guest: g })}
                        </Link>
                        <span dir="ltr" className="block truncate text-start font-mono text-caption text-muted-foreground">
                          {[slugOf(g!), g!.address].filter(Boolean).join(" · ")}
                        </span>
                      </span>
                    </span>
                  ) : (
                  <span className="flex min-w-0 items-center gap-3" data-app={a.name}>
                    <ServerMark server={{ color: a.color, icon: a.icon ?? "globe", logo_url: a.logo_url }} size={8} />
                    <span className="min-w-0">
                      <Link href={`${base}/apps/${a.name}`} className="block truncate font-medium underline-offset-4 hover:underline" dir="auto">
                        {a.title || a.name}
                      </Link>
                      <span dir="ltr" className="block max-w-64 truncate text-start font-mono text-caption text-muted-foreground">
                        {[a.name, isImported(a) ? originSummary(a, t) : a.branch].filter(Boolean).join(" · ")}
                      </span>
                    </span>
                  </span>
                ),
              },
              {
                id: "status",
                header: t("apps.status"),
                label: t("apps.status"),
                sortValue: statusOf,
                filterValue: statusOf,
                cell: ({ app: a, guest: g }) => (g ? <AppStatusBadge status={statusOf({ id: "", guest: g })} /> : isImported(a!) ? <ImportedBadge item={a!} /> : <AppStatusBadge status={a!.status} />),
              },
              {
                id: "runtime",
                header: t("apps.runtime"),
                label: t("apps.runtime"),
                sortValue: (r) => (r.app ? (r.app.runtime ?? "") : r.guest!.type),
                className: "max-md:hidden",
                headerClassName: "max-md:hidden",
                cell: ({ app: a, guest: g }) => (
                  <span dir="ltr" className="font-mono text-caption">
                    {g ? `${g.type.toUpperCase()} ${g.vmid}` : (runtimeLabel(a!.runtime) ?? "—")}
                  </span>
                ),
              },
              {
                id: "deploy",
                header: t("apps.latestDeploy"),
                label: t("apps.latestDeploy"),
                sortValue: (r) => r.app?.latest_deployment?.created_at ?? "",
                cell: ({ app: a, guest: g }) => {
                  if (g)
                    return (
                      <span dir="ltr" className="text-caption text-muted-foreground">
                        {[g.cpus ? `${g.cpus} vCPU` : "", g.mem_bytes ? formatBytes(g.mem_bytes) : "", g.disk_bytes ? formatBytes(g.disk_bytes) : ""].filter(Boolean).join(" · ") || "—"}
                      </span>
                    )
                  const d = a!.latest_deployment
                  return isImported(a!) ? (
                    <span className="text-caption text-muted-foreground">{t("imported.notDeployed")}</span>
                  ) : d ? (
                    <span className="flex flex-col items-start gap-1">
                      <span className="flex items-center gap-2">
                        <span dir="ltr" className="font-mono text-caption">
                          #{d.number} · {shortSha(d.commit_sha)}
                        </span>
                        <DeploymentStatusBadge status={d.status} />
                      </span>
                      <span dir="ltr" className="text-caption text-muted-foreground">
                        {fmt(d.created_at)}
                      </span>
                    </span>
                  ) : (
                    <span className="text-caption text-muted-foreground">{t("apps.neverDeployed")}</span>
                  )
                },
              },
              {
                id: "domains",
                header: t("apps.domainsLabel"),
                label: t("apps.domainsLabel"),
                className: "max-lg:hidden",
                headerClassName: "max-lg:hidden",
                cell: ({ app: a }) => (
                  <span dir="ltr" className="font-mono text-caption">
                    {a ? <DomainsSummary domains={a.domains ?? []} /> : "—"}
                  </span>
                ),
              },
            ] satisfies DataTableColumn<Row>[]}
            onRowClick={(r) => router.push(hrefOf(r))}
            rowActions={(r) => {
              const open = { id: "open", label: t("servers.apps.openDashboard"), icon: LayoutDashboardIcon, onSelect: () => router.push(hrefOf(r)) }
              if (r.guest) {
                return manage
                  ? [open, { id: "edit", label: t("lists.edit"), icon: PencilIcon, onSelect: () => setEditingUser(r.guest!) }]
                  : [open]
              }
              const a = r.app!
              if (!manage) return [open]
              return [
                open,
                {
                  id: "edit",
                  label: t("lists.edit"),
                  icon: PencilIcon,
                  onSelect: () => (isImported(a) ? router.push(`${base}/apps/${a.name}/settings`) : setEditing(a)),
                },
                { id: "delete", label: t("common.delete"), icon: Trash2Icon, danger: true, onSelect: () => setDeleting(a) },
              ]
            }}
          />
          {editing ? (
            <EditApp
              key={editing.id}
              app={editing}
              team={team.slug}
              onSaved={load}
              open
              onOpenChange={(v) => !v && setEditing(null)}
            />
          ) : null}
          {editingUser && editingUser.server_id && nested[editingUser.server_id] ? (
            <EditUser
              key={editingUser.server_id}
              team={team.slug}
              user={nested[editingUser.server_id]}
              slug={slugOf(editingUser)}
              onSaved={load}
              open
              onOpenChange={(v) => !v && setEditingUser(null)}
            />
          ) : null}
          {deleting ? (
            <ConfirmButton
              open
              onOpenChange={(v) => !v && setDeleting(null)}
              label={t("common.delete")}
              title={t("lists.deleteApp", { name: deleting.name })}
              description={t("lists.deleteAppBody")}
              confirmLabel={t("common.delete")}
              onConfirm={async () => {
                try {
                  await apiFetch(`/api/teams/${team.slug}/apps/${deleting.name}`, { method: "DELETE" })
                } catch (err) {
                  throw new Error(errorText(err))
                }
                await load()
              }}
            />
          ) : null}
        </div>
      )}
    </section>
  )
}

/** Edit what an app deploys with: branch, port, health check, auto-deploy (applies from the next deploy). */
function EditApp({ app, team, onSaved, ...controlled }: { app: App; team: string; onSaved: () => Promise<void> } & Controlled) {
  const { t } = useI18n()
  const [branch, setBranch] = useState("")
  const [port, setPort] = useState("")
  const [health, setHealth] = useState("")
  const [paused, setPaused] = useState(false)
  return (
    <EditDialog
      {...controlled}
      title={t("lists.editApp", { name: app.name })}
      description={t("lists.editAppHint")}
      onOpen={() => {
        setBranch(app.branch ?? "")
        setPort(String(app.port))
        setHealth(app.healthcheck_path)
        setPaused(app.autodeploy_paused)
      }}
      onSave={async () => {
        await apiFetch(`/api/teams/${team}/apps/${app.name}`, {
          method: "PATCH",
          body: { branch: branch.trim(), port: Number(port), healthcheck_path: health.trim(), autodeploy_paused: paused },
        })
        await onSaved()
      }}
    >
      <div className="flex w-full flex-col gap-5">
        <Field>
          <FieldLabel htmlFor="ea-branch">{t("lists.branch")}</FieldLabel>
          <Input id="ea-branch" dir="ltr" value={branch} onChange={(e) => setBranch(e.target.value)} />
        </Field>
        <Field>
          <FieldLabel htmlFor="ea-port">{t("lists.port")}</FieldLabel>
          <Input id="ea-port" dir="ltr" type="number" min={1} max={65535} required value={port} onChange={(e) => setPort(e.target.value)} />
        </Field>
        <Field>
          <FieldLabel htmlFor="ea-health">{t("lists.healthPath")}</FieldLabel>
          <Input id="ea-health" dir="ltr" value={health} onChange={(e) => setHealth(e.target.value)} />
        </Field>
        <label className="flex items-center gap-2 text-sm">
          <Checkbox checked={paused} onCheckedChange={(v) => setPaused(v === true)} />
          {t("lists.pauseAutodeploy")}
        </label>
      </div>
    </EditDialog>
  )
}
