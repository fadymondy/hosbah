"use client"

import { Button, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, Input, Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle, toast } from "@fadymondy/nasaq/web"
import Link from "next/link"
import { useEffect, useMemo, useRef, useState } from "react"
import { BlocksIcon, LayersIcon, LinkIcon, PlusIcon, SearchIcon, type LucideIcon } from "lucide-react"

import { AddDomainDialog, type DomainPort } from "@/components/apps/domains"
import { useDialogOpen, type Controlled } from "@/components/dialog-state"
import { FormError, useErrorText } from "@/components/form-error"
import { AddBrowser } from "@/components/site/browsers"
import { KIND_ICON, StatusDot } from "@/components/site/canvas/node"
import { MonitorDialog } from "@/components/site/monitors"
import { attachable, attachDataService } from "@/components/site/panels"
import { RealtimeDialog } from "@/components/site/realtime"
import { ScheduleDialog } from "@/components/site/schedules"
import { InstallDialog, installServices, type Recipe } from "@/components/site/stacks"
import { AddBucket, AddVolume } from "@/components/site/storage"




import { apiFetch } from "@/lib/api"
import { isImported } from "@/lib/apps"
import type { DataService } from "@/lib/databases"
import { useI18n } from "@/lib/i18n"
import { cn } from "@/lib/utils"

export type Choice =
  | { kind: "domain" }
  | { kind: "database" }
  | { kind: "volume" }
  | { kind: "bucket" }
  | { kind: "browser" }
  | { kind: "monitor" }
  | { kind: "schedule" }
  | { kind: "realtime" }
  | { kind: "recipe"; recipe: Recipe }

type Item = { id: string; choice: Choice | null; href?: string; icon: LucideIcon; label: string; hint: string; disabled?: string; group: "site" | "catalog" }

/**
 * "Add component", like n8n's node panel: a searchable list of what can be added to the site. A
 * side sheet on wide screens (at the page's inline end), a bottom sheet on phones.
 */
export function Palette({
  open,
  onOpenChange,
  onChoose,
  imported,
  hasMinio,
  hasRealtime,
  team,
  newSiteBase,
  mobile,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onChoose: (c: Choice) => void
  imported: boolean
  hasMinio: boolean
  hasRealtime: boolean
  team: string
  /** /{locale}/t/{team}/apps/new — where a "stack" recipe starts a new site. */
  newSiteBase: string
  mobile: boolean
}) {
  const { t, locale } = useI18n()
  const [q, setQ] = useState("")
  const [recipes, setRecipes] = useState<Recipe[] | null>(null)
  const search = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (!open) return
    setQ("")
    if (!imported && recipes === null)
      apiFetch<{ recipes: Recipe[] }>(`/api/teams/${team}/catalog`)
        .then((r) => setRecipes(r.recipes ?? []))
        .catch(() => setRecipes([]))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  const items = useMemo<Item[]>(() => {
    const base: Item[] = [
      { id: "domain", choice: { kind: "domain" }, icon: KIND_ICON.domain, label: t("site.canvas.kinds.domain"), hint: t("site.canvas.add.domain"), group: "site" },
      { id: "monitor", choice: { kind: "monitor" }, icon: KIND_ICON.monitor, label: t("site.canvas.kinds.monitor"), hint: t("site.canvas.add.monitor"), group: "site" },
    ]
    if (imported) return base
    const recipeItems: Item[] = (recipes ?? []).map((r) =>
      r.kind === "stack"
        ? { id: `recipe:${r.id}`, choice: null, href: `${newSiteBase}?stack=${encodeURIComponent(r.id)}`, icon: LayersIcon, label: r.name, hint: t("site.canvas.add.stack"), group: "catalog" }
        : { id: `recipe:${r.id}`, choice: { kind: "recipe", recipe: r }, icon: BlocksIcon, label: r.name, hint: r.summary, group: "catalog" },
    )
    return [
      base[0],
      { id: "database", choice: { kind: "database" }, icon: KIND_ICON.db, label: t("site.canvas.kinds.db"), hint: t("site.canvas.add.database"), group: "site" },
      { id: "volume", choice: { kind: "volume" }, icon: KIND_ICON.volume, label: t("site.canvas.kinds.volume"), hint: t("site.canvas.add.volume"), group: "site" },
      {
        id: "bucket",
        choice: { kind: "bucket" },
        icon: KIND_ICON.bucket,
        label: t("site.canvas.kinds.bucket"),
        hint: t("site.canvas.add.bucket"),
        disabled: hasMinio ? undefined : t("site.canvas.add.needsMinio"),
        group: "site",
      },
      { id: "browser", choice: { kind: "browser" }, icon: KIND_ICON.browser, label: t("site.canvas.kinds.browser"), hint: t("site.canvas.add.browser"), group: "site" },
      base[1],
      { id: "schedule", choice: { kind: "schedule" }, icon: KIND_ICON.schedule, label: t("site.canvas.kinds.schedule"), hint: t("site.canvas.add.schedule"), group: "site" },
      {
        id: "realtime",
        choice: { kind: "realtime" },
        icon: KIND_ICON.realtime,
        label: t("site.nav.realtime"),
        hint: hasRealtime ? t("site.canvas.add.realtimeEdit") : t("site.canvas.add.realtime"),
        group: "site",
      },
      ...recipeItems,
    ]
  }, [t, imported, recipes, hasMinio, hasRealtime, newSiteBase])

  const needle = q.trim().toLowerCase()
  const shown = needle ? items.filter((i) => `${i.label} ${i.hint}`.toLowerCase().includes(needle)) : items
  const groups: Item["group"][] = ["site", "catalog"]
  const side = mobile ? "bottom" : "end"

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side={side} className={cn("gap-0 p-0", mobile ? "max-h-[80dvh]" : "w-full sm:max-w-sm")} initialFocus={search}>
        <SheetHeader className="nq-box px-4 py-4">
          <SheetTitle>{t("site.canvas.addComponent")}</SheetTitle>
          <SheetDescription>{t("site.canvas.addHint")}</SheetDescription>
        </SheetHeader>
        <div className="flex items-center gap-2 nq-box px-4 py-2">
          <SearchIcon aria-hidden className="size-4 text-muted-foreground" />
          <Input
            ref={search}
            type="search"
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder={t("site.canvas.search")}
            aria-label={t("site.canvas.search")}
            className="border-0 bg-transparent px-0 shadow-none focus-visible:ring-0"
          />
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto">
          {shown.length === 0 ? <p className="px-4 py-6 text-center text-sm text-muted-foreground">{t("home.noMatch")}</p> : null}
          {groups.map((g) => {
            const list = shown.filter((i) => i.group === g)
            if (!list.length) return null
            return (
              <div key={g} role="group" aria-label={t(`site.canvas.groups.${g}`)}>
                <p className="eyebrow px-4 pt-4 pb-1">{t(`site.canvas.groups.${g}`)}</p>
                <ul className="flex flex-col">
                  {list.map((i) => {
                    const body = (
                      <>
                        <span className="flex size-8 shrink-0 items-center justify-center border border-border bg-card text-nq-brand [&_svg]:size-4">
                          <i.icon aria-hidden />
                        </span>
                        <span className="min-w-0 flex-1">
                          <span dir="auto" className="block truncate text-sm font-medium text-foreground">
                            {i.label}
                          </span>
                          <span dir="auto" className="line-clamp-2 block text-xs text-muted-foreground">
                            {i.disabled ?? i.hint}
                          </span>
                        </span>
                      </>
                    )
                    const cls = "flex w-full items-center gap-3 px-4 py-2.5 text-start hover:bg-secondary focus-visible:bg-secondary focus-visible:outline-none disabled:cursor-not-allowed disabled:opacity-60"
                    return (
                      <li key={i.id}>
                        {i.href ? (
                          <Link href={i.href} className={cls} onClick={() => onOpenChange(false)}>
                            {body}
                          </Link>
                        ) : (
                          <button
                            type="button"
                            className={cls}
                            disabled={!!i.disabled}
                            onClick={() => {
                              onOpenChange(false)
                              if (i.choice) onChoose(i.choice)
                            }}
                          >
                            {body}
                          </button>
                        )}
                      </li>
                    )
                  })}
                </ul>
              </div>
            )
          })}
          {!imported && recipes === null ? <p className="px-4 py-3 text-xs text-muted-foreground">{t("common.loading")}</p> : null}
        </div>
      </SheetContent>
    </Sheet>
  )
}

/**
 * The form for a palette choice: the section's own dialog, opened here. onSaved reloads the canvas,
 * where the new box appears already joined to the site.
 */
export function AddForm({
  choice,
  onClose,
  onSaved,
  path,
  team,
  appName,
  hosts,
  services,
  connected,
  domainPort,
  serverId,
  serversDatabases,
}: {
  choice: Choice
  onClose: () => void
  onSaved: () => Promise<void> | void
  path: string
  team: string
  appName: string
  /** The site's own URLs (monitor targets). */
  hosts: string[]
  /** Data services on the site's server. */
  services: DataService[]
  /** Ids of the services the app is connected to. */
  connected: Set<string>
  domainPort?: DomainPort
  serverId: string
  /** The server's databases page (to create one). */
  serversDatabases: string
}) {
  const controlled: Controlled = { open: true, onOpenChange: (v) => !v && onClose() }
  switch (choice.kind) {
    case "domain":
      return <AddDomainDialog {...controlled} path={path} port={domainPort} onSaved={onSaved} />
    case "volume":
      return <AddVolume {...controlled} path={path} onSaved={onSaved} />
    case "bucket":
      return <AddBucket {...controlled} path={path} services={services.filter((s) => s.kind === "minio" && !isImported(s))} onSaved={onSaved} />
    case "browser":
      return <AddBrowser {...controlled} path={path} onSaved={onSaved} />
    case "monitor":
      return <MonitorDialog {...controlled} path={path} hosts={hosts} onSaved={onSaved} />
    case "schedule":
      return <ScheduleDialog {...controlled} path={path} onSaved={onSaved} />
    case "realtime":
      return <RealtimeDialog {...controlled} path={path} onSaved={onSaved} />
    case "recipe":
      return <InstallDialog {...controlled} path={path} recipe={choice.recipe} services={installServices(services, serverId)} onSaved={onSaved} />
    case "database":
      return (
        <AttachDatabase
          {...controlled}
          team={team}
          appName={appName}
          services={services.filter((s) => attachable(s) && !connected.has(s.id))}
          serversDatabases={serversDatabases}
          onSaved={onSaved}
        />
      )
  }
}

/** Pick a database or cache on the server to connect the site to (the same attach as the Databases page). */
function AttachDatabase({
  team,
  appName,
  services,
  serversDatabases,
  onSaved,
  ...controlled
}: { team: string; appName: string; services: DataService[]; serversDatabases: string; onSaved: () => Promise<void> | void } & Controlled) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [busy, setBusy] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const { open, setOpen } = useDialogOpen(controlled, () => setError(null))

  async function attach(s: DataService) {
    setBusy(s.id)
    setError(null)
    try {
      toast.success(t("site.attached", { variable: await attachDataService(team, s, appName) }))
      setOpen(false)
      await onSaved()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(null)
    }
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("site.canvas.attachTitle")}</DialogTitle>
          <DialogDescription>{t("site.canvas.attachHint")}</DialogDescription>
        </DialogHeader>
        {services.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t("site.canvas.noAttachable")}</p>
        ) : (
          <ul className="flex flex-col divide-y divide-border border border-border">
            {services.map((s) => (
              <li key={s.id} className="flex items-center gap-3 px-3 py-2">
                <StatusDot status="ok" label={t(`databases.statuses.${s.status}`, undefined, s.status)} />
                <span className="min-w-0 flex-1">
                  <span dir="ltr" className="block truncate text-sm font-medium">
                    {s.name}
                  </span>
                  <span dir="ltr" className="block text-xs text-muted-foreground">
                    {s.kind} {s.version}
                  </span>
                </span>
                <Button variant="primary" size="sm" disabled={busy !== null} onClick={() => void attach(s)}>
                  <LinkIcon />
                  {t("site.attach")}
                </Button>
              </li>
            ))}
          </ul>
        )}
        <FormError message={error} />
        <DialogFooter>
          <Button variant="secondary" nativeButton={false} render={<Link href={serversDatabases} />}>
            <PlusIcon />
            {t("site.newDatabase")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
