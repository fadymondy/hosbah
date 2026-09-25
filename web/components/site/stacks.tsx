"use client"

import Link from "next/link"
import { useCallback, useEffect, useState, type FormEvent } from "react"
import { BlocksIcon, BoxIcon, ExternalLinkIcon, LayersIcon, PlusIcon, Trash2Icon } from "lucide-react"
import { toast } from "sonner"

import { useDialogOpen, type Controlled } from "@/components/dialog-state"
import { FormError, useErrorText } from "@/components/form-error"
import { ListSearch, matches } from "@/components/server-tabs/list-tools"
import { useLoadedSite } from "@/components/site/context"
import { ErrorBand, LoadingRows, SectionTitle, TableBand } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { apiFetch } from "@/lib/api"
import { isImported } from "@/lib/apps"
import type { DataService } from "@/lib/databases"
import { useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

export type Recipe = {
  id: string
  name: string
  summary: string
  kind: "app" | "stack"
  licence: string
  homepage: string
  version: string
  verified: boolean
  needs: { postgres?: boolean; mysql?: boolean; redis?: boolean }
  has_web_ui: boolean
  /** Set when the recipe also publishes a second port (e.g. an admin console); its label. */
  extra_port_name?: string
}
export type Install = {
  id: string
  name: string
  recipe: string
  recipe_name: string
  recipe_version: string
  image: string
  has_web_ui: boolean
  hostname: string | null
  env_injected: string[]
  status: "creating" | "ready" | "failed" | "removing"
  error: string | null
  running: boolean | null
  restarts: number | null
  /** The recipe's extra_port on the server's 127.0.0.1 (e.g. MinIO's console); null when it has none or isn't known yet. */
  extra_host_port: number | null
}

/** The data services an installed app may use: running, Hosbah-managed ones on the site's server. */
export function installServices(all: DataService[], serverId: string) {
  return all.filter((s) => s.server_id === serverId && !isImported(s) && s.status === "running")
}

/** The install's recipe's extra_port_name (e.g. "console"), or a generic fallback once the recipe itself isn't loaded yet. */
function extraPortName(recipes: Recipe[] | null, recipeId: string) {
  return recipes?.find((r) => r.id === recipeId)?.extra_port_name || "port"
}

/**
 * Apps & stacks: a built-in catalog of recipes. An "app" recipe installs a ready service next to
 * the site (its own limited container with generated secrets, optional database and hostname) and
 * links it to the site through sealed variables. A "stack" recipe prefills a new site.
 */
export function SiteStacks() {
  const { t, locale } = useI18n()
  const { team, manage } = useTeam()
  const errorText = useErrorText()
  const { app, path } = useLoadedSite()
  const base = `/${locale}/t/${team.slug}`
  const [recipes, setRecipes] = useState<Recipe[] | null>(null)
  const [installs, setInstalls] = useState<Install[] | null>(null)
  const [services, setServices] = useState<DataService[]>([])
  const [error, setError] = useState<string | null>(null)
  const [q, setQ] = useState("")

  const load = useCallback(async () => {
    try {
      const r = await apiFetch<{ installs: Install[] }>(`${path}/installs`)
      setInstalls(r.installs ?? [])
      setError(null)
    } catch (err) {
      setError(errorText(err))
      setInstalls((x) => x ?? [])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path])

  useEffect(() => {
    void load()
    apiFetch<{ recipes: Recipe[] }>(`/api/teams/${team.slug}/catalog`)
      .then((r) => setRecipes(r.recipes ?? []))
      .catch((err) => {
        setError(errorText(err))
        setRecipes([])
      })
    apiFetch<{ services: DataService[] }>(`/api/teams/${team.slug}/data-services`)
      .then((r) => setServices(installServices(r.services ?? [], app.server_id)))
      .catch(() => setServices([]))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [load, team.slug, app.server_id])

  useEffect(() => {
    if (!installs?.some((i) => i.status === "creating" || i.status === "removing")) return
    const id = setInterval(load, 5000)
    return () => clearInterval(id)
  }, [installs, load])

  const shown = (recipes ?? []).filter((r) => matches(q, r.id, r.name, r.summary, r.kind, r.licence))

  return (
    <>
      <section aria-labelledby="inst-h">
        <SectionTitle id="inst-h" title={t("site.stacks.installed")} hint={t("site.stacks.installedHint")} />
        <ErrorBand message={error} />
        {installs === null ? (
          <LoadingRows rows={2} />
        ) : installs.length === 0 ? (
          <p className="flex items-center gap-2 border-b border-line px-6 py-6 text-sm text-grid-muted">
            <BoxIcon className="size-4" />
            {t("site.stacks.none")}
          </p>
        ) : (
          <TableBand>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("site.monitors.name")}</TableHead>
                  <TableHead>{t("lists.status")}</TableHead>
                  <TableHead>{t("proxy.hostname")}</TableHead>
                  <TableHead>{t("site.storage.variables")}</TableHead>
                  {manage ? <TableHead className="text-end">{t("common.actions")}</TableHead> : null}
                </TableRow>
              </TableHeader>
              <TableBody>
                {installs.map((i) => (
                  <TableRow key={i.id}>
                    <TableCell>
                      <span dir="ltr" className="font-medium">
                        {i.name}
                      </span>
                      <div dir="ltr" className="text-start text-xs text-grid-muted">
                        {i.recipe_name} · {i.recipe_version}
                      </div>
                    </TableCell>
                    <TableCell>
                      <span className="flex flex-wrap items-center gap-1.5">
                        <Badge variant={i.status === "ready" ? "default" : i.status === "failed" ? "destructive" : "secondary"}>{t(`site.storage.status.${i.status}`)}</Badge>
                        {i.status === "ready" && i.running != null ? (
                          <Badge variant={i.running ? "secondary" : "outline"}>{i.running ? t("site.browsers.running") : t("site.browsers.stopped")}</Badge>
                        ) : null}
                      </span>
                      {i.error ? (
                        <p dir="auto" className="mt-1 max-w-xs text-xs text-grid-danger">
                          {i.error}
                        </p>
                      ) : null}
                    </TableCell>
                    <TableCell dir="ltr" className="text-start font-mono text-xs">
                      {i.hostname ? (
                        <a href={`https://${i.hostname}`} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 hover:underline">
                          {i.hostname}
                          <ExternalLinkIcon className="size-3" />
                        </a>
                      ) : (
                        "—"
                      )}
                      {i.extra_host_port ? (
                        <div className="mt-1 text-grid-muted">
                          {t("site.stacks.extraPort", { name: extraPortName(recipes, i.recipe), port: String(i.extra_host_port) })}
                        </div>
                      ) : null}
                    </TableCell>
                    <TableCell dir="ltr" className="max-w-64 text-start font-mono text-xs text-grid-muted">
                      {i.env_injected.join(" ")}
                    </TableCell>
                    {manage ? (
                      <TableCell>
                        <div className="flex justify-end">
                          <RemoveInstall path={path} install={i} onRemoved={load} />
                        </div>
                      </TableCell>
                    ) : null}
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </TableBand>
        )}
      </section>

      <section aria-labelledby="cat-h">
        <SectionTitle id="cat-h" title={t("site.stacks.catalog")} hint={t("site.stacks.catalogHint")} />
        {recipes === null ? (
          <LoadingRows rows={3} />
        ) : (
          <>
            <ListSearch value={q} onChange={setQ} placeholder={t("site.stacks.search")} shown={shown.length} total={recipes.length} />
            {shown.length === 0 ? (
              <p className="border-b border-line px-6 py-8 text-center text-sm text-grid-muted">{t("home.noMatch")}</p>
            ) : (
              <ul className="grid gap-4 border-b border-line px-6 py-6 md:grid-cols-2 xl:grid-cols-3">
                {shown.map((r) => (
                  <li key={r.id} className="flex min-w-0 flex-col border border-line bg-grid-card">
                    <div className="flex items-start gap-3 px-4 pt-4 pb-3">
                      <span className="flex size-9 shrink-0 items-center justify-center border border-line bg-background text-grid-brand [&_svg]:size-4">
                        {r.kind === "stack" ? <LayersIcon /> : <BlocksIcon />}
                      </span>
                      <span className="min-w-0 flex-1">
                        <span className="flex flex-wrap items-center gap-2">
                          <span dir="auto" className="font-medium text-grid-fg">
                            {r.name}
                          </span>
                          <Badge variant="outline">{t(`site.stacks.kinds.${r.kind}`)}</Badge>
                          {!r.verified ? <Badge variant="secondary">{t("site.stacks.unverified")}</Badge> : null}
                          {r.needs.postgres ? (
                            <Badge variant="outline" dir="ltr">
                              {t("site.stacks.needs", { service: "Postgres" })}
                            </Badge>
                          ) : null}
                          {r.needs.mysql ? (
                            <Badge variant="outline" dir="ltr">
                              {t("site.stacks.needs", { service: "MySQL" })}
                            </Badge>
                          ) : null}
                          {r.needs.redis ? (
                            <Badge variant="outline" dir="ltr">
                              {t("site.stacks.needs", { service: "Redis" })}
                            </Badge>
                          ) : null}
                        </span>
                        <span dir="auto" className="block text-xs text-grid-muted">
                          {r.summary}
                        </span>
                      </span>
                    </div>
                    <div className="flex flex-wrap items-center gap-x-3 gap-y-1 border-t border-line px-4 py-2 text-xs text-grid-muted">
                      <span dir="ltr">{r.licence}</span>
                      <span dir="ltr">v{r.version}</span>
                      <a href={r.homepage} target="_blank" rel="noreferrer" className="ms-auto inline-flex items-center gap-1 hover:underline">
                        {t("site.stacks.homepage")}
                        <ExternalLinkIcon className="size-3" />
                      </a>
                    </div>
                    <div className="mt-auto border-t border-line px-4 py-3">
                      {r.kind === "stack" ? (
                        <Button size="sm" variant="outline" nativeButton={false} render={<Link href={`${base}/apps/new?stack=${encodeURIComponent(r.id)}`} />}>
                          <PlusIcon />
                          {t("site.stacks.newSite")}
                        </Button>
                      ) : manage ? (
                        <InstallDialog path={path} recipe={r} services={services} onSaved={load} />
                      ) : null}
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </>
        )}
        <p className="border-b border-line px-6 py-3 text-xs text-grid-muted">{t("site.stacks.publishLater")}</p>
      </section>
    </>
  )
}

/**
 * Install an "app" recipe next to the site. services: the running, Hosbah-managed data services on
 * the site's server (for recipes that need Postgres or Redis). Also opened from the overview canvas.
 */
export function InstallDialog({
  path,
  recipe,
  services,
  onSaved,
  ...controlled
}: { path: string; recipe: Recipe; services: DataService[]; onSaved: () => Promise<void> | void } & Controlled) {
  const { t, locale } = useI18n()
  const { team } = useTeam()
  const { app } = useLoadedSite()
  const errorText = useErrorText()
  const { open, setOpen, controlled: fromCaller } = useDialogOpen(controlled)
  const [name, setName] = useState(recipe.id)
  const [hostname, setHostname] = useState("")
  const [postgres, setPostgres] = useState("")
  const [mysql, setMysql] = useState("")
  const [redis, setRedis] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const pg = services.filter((s) => s.kind === "postgres")
  const my = services.filter((s) => s.kind === "mysql")
  const rd = services.filter((s) => s.kind === "redis")
  const databasesHref = `/${locale}/t/${team.slug}/servers/${app.server_id}/databases`

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await apiFetch(`${path}/installs`, {
        method: "POST",
        body: {
          recipe: recipe.id,
          name: name.trim(),
          ...(recipe.needs.postgres ? { postgres } : {}),
          ...(recipe.needs.mysql ? { mysql } : {}),
          ...(recipe.needs.redis ? { redis } : {}),
          ...(hostname.trim() ? { hostname: hostname.trim() } : {}),
        },
      })
      toast.success(t("site.stacks.installing", { name: recipe.name }))
      setOpen(false)
      await onSaved()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  const missing = (recipe.needs.postgres && pg.length === 0) || (recipe.needs.mysql && my.length === 0) || (recipe.needs.redis && rd.length === 0)

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      {fromCaller ? null : (
        <DialogTrigger render={<Button size="sm" />}>
          <PlusIcon />
          {t("site.stacks.install")}
        </DialogTrigger>
      )}
      <DialogContent>
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{recipe.name}</DialogTitle>
            <DialogDescription>{t("site.stacks.installHint")}</DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="in-name">{t("site.monitors.name")}</FieldLabel>
              <Input id="in-name" dir="ltr" required pattern="[a-z][a-z0-9\-]{0,30}" value={name} onChange={(e) => setName(e.target.value)} />
            </Field>
            {recipe.needs.postgres ? (
              <ServicePick label="Postgres" items={pg} value={postgres} onChange={setPostgres} />
            ) : null}
            {recipe.needs.mysql ? (
              my.length === 0 ? (
                <p className="text-sm text-grid-danger">
                  {t("site.stacks.needsMySQL")}{" "}
                  <Link href={databasesHref} className="underline underline-offset-4 hover:no-underline">
                    {t("nav.databases")}
                  </Link>
                </p>
              ) : (
                <ServicePick label="MySQL/MariaDB" items={my} value={mysql} onChange={setMysql} />
              )
            ) : null}
            {recipe.needs.redis ? <ServicePick label="Redis" items={rd} value={redis} onChange={setRedis} /> : null}
            {recipe.has_web_ui ? (
              <Field>
                <FieldLabel htmlFor="in-host">{t("proxy.hostname")}</FieldLabel>
                <Input id="in-host" dir="ltr" placeholder="search.example.com" value={hostname} onChange={(e) => setHostname(e.target.value)} />
                <FieldDescription>{t("site.stacks.hostnameHint")}</FieldDescription>
              </Field>
            ) : null}
            {(recipe.needs.postgres && pg.length === 0) || (recipe.needs.redis && rd.length === 0) ? (
              <FormError message={t("site.stacks.needsService")} />
            ) : null}
          </FieldGroup>
          <FormError message={error} />
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button
              type="submit"
              disabled={busy || missing || (recipe.needs.postgres && !postgres) || (recipe.needs.mysql && !mysql) || (recipe.needs.redis && !redis)}
            >
              {busy ? t("common.working") : t("site.stacks.install")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function ServicePick({ label, items, value, onChange }: { label: string; items: DataService[]; value: string; onChange: (v: string) => void }) {
  return (
    <Field>
      <FieldLabel>{label}</FieldLabel>
      <Select value={value} onValueChange={(v) => v && onChange(v as string)}>
        <SelectTrigger aria-label={label}>
          <SelectValue>{(v: string) => v || "—"}</SelectValue>
        </SelectTrigger>
        <SelectContent>
          {items.map((s) => (
            <SelectItem key={s.id} value={s.name}>
              {s.name}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </Field>
  )
}

/** Remove an installed app: type its name; its data only on request. Also opened from the overview canvas. */
export function RemoveInstall({ path, install, onRemoved, ...controlled }: { path: string; install: Install; onRemoved: () => Promise<void> | void } & Controlled) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const name = install.name
  const [typed, setTyped] = useState("")
  const [data, setData] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const { open, setOpen, controlled: fromCaller } = useDialogOpen(controlled, () => {
    setTyped("")
    setData(false)
    setError(null)
  })

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await apiFetch(`${path}/installs/${install.id}`, { method: "DELETE", body: { confirm: name, delete_data: data } })
      await onRemoved()
      setOpen(false)
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      {fromCaller ? null : (
        <DialogTrigger render={<Button size="sm" variant="outline" />}>
          <Trash2Icon />
          {t("site.storage.remove")}
        </DialogTrigger>
      )}
      <DialogContent>
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle dir="ltr">{name}</DialogTitle>
            <DialogDescription>{t("site.stacks.removeHint")}</DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <label className="flex items-center gap-2 text-sm">
              <Checkbox checked={data} onCheckedChange={(v) => setData(v === true)} />
              {t("site.storage.deleteData")}
            </label>
            <Field>
              <FieldLabel htmlFor="rmi-confirm">{t("site.storage.typeName", { name })}</FieldLabel>
              <Input id="rmi-confirm" dir="ltr" autoComplete="off" value={typed} onChange={(e) => setTyped(e.target.value)} />
            </Field>
          </FieldGroup>
          <FormError message={error} />
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" variant="destructive" disabled={busy || typed !== name}>
              {busy ? t("common.working") : t("site.storage.remove")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
