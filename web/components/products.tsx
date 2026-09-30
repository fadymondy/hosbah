"use client"

import { Badge, Button, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger, EmptyState, Field, FieldDescription, FieldLabel, Input, Progress, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Table, TableBody, TableCell, TableHead, TableHeader, TableRow, Tabs, TabsList, TabsPanel, TabsTab, Textarea, toast } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useMemo, useState, type FormEvent } from "react"
import { ArchiveIcon, KeyRoundIcon, PackageIcon, PlusIcon, RefreshCwIcon, Trash2Icon } from "lucide-react"

import { ConfirmButton } from "@/components/confirm-button"
import { CopyField } from "@/components/copy-field"
import { useDialogOpen, type Controlled } from "@/components/dialog-state"
import { FormError, useErrorText } from "@/components/form-error"
import { ListSearch, matches } from "@/components/server-tabs/list-tools"

import { ErrorBand, LoadingRows, SectionTitle, TableBand } from "@/components/states"









import { apiFetch } from "@/lib/api"
import { useFormatDate, useI18n } from "@/lib/i18n"
import type { MintedProductKey, Product, ProductDetail, ProductInstance, ProductKey, ProductUpgrade } from "@/lib/products"
import { canOperate, type Server } from "@/lib/servers"
import { useTeam } from "@/lib/team"

function statusVariant(status: string): "success" | "neutral" | "danger" | "outline" {
  if (["active", "ok", "running", "provisioned"].includes(status)) return "success"
  if (["suspended", "paused", "archived"].includes(status)) return "neutral"
  if (["failed", "error", "timeout"].includes(status)) return "danger"
  return "outline"
}

/**
 * SaaS products (MH-576 D7): a team turns an app/recipe/repo it runs into a product, then
 * provisions per-customer instances of it, suspends/resumes/destroys them, and rolls every
 * instance to a new ref one at a time with a health gate. A product also mints scoped API keys
 * for the team's own billing/signup system, with a signed webhook on every instance state change.
 * Reading is any member; managing products and instances is owner/admin/deployer; archiving,
 * destroying and minting keys is owner/admin only.
 */
export function TeamProducts() {
  const { t } = useI18n()
  const { team, manage } = useTeam()
  const operate = canOperate(team.role)
  const errorText = useErrorText()
  const base = `/api/teams/${team.slug}/products`

  const [products, setProducts] = useState<Product[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [q, setQ] = useState("")
  const [openProduct, setOpenProduct] = useState<Product | null>(null)

  const load = useCallback(async () => {
    try {
      const r = await apiFetch<{ products: Product[] }>(base)
      setProducts(r.products ?? [])
      setError(null)
    } catch (err) {
      setError(errorText(err))
      setProducts((x) => x ?? [])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [base])

  useEffect(() => {
    void load()
  }, [load])

  const shown = (products ?? []).filter((p) => matches(q, p.name, p.title ?? "", p.description ?? ""))

  return (
    <>
      <section aria-labelledby="products-h">
        <SectionTitle
          id="products-h"
          title={t("products.title")}
          hint={t("products.hint")}
          action={operate ? <ProductDialog path={base} onSaved={load} /> : null}
        />
        <ErrorBand message={error} />
        {products === null ? (
          <LoadingRows rows={3} />
        ) : products.length === 0 ? (
          <EmptyState className="border-b" icon={PackageIcon} title={t("products.emptyTitle")} description={t("products.emptyHint")} actions={<>{operate ? <ProductDialog path={base} onSaved={load} /> : null}</>} />
        ) : (
          <>
            <ListSearch value={q} onChange={setQ} placeholder={t("products.search")} shown={shown.length} total={products.length} />
            <TableBand>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t("products.name")}</TableHead>
                    <TableHead>{t("products.source")}</TableHead>
                    <TableHead>{t("products.status")}</TableHead>
                    <TableHead className="text-end">{t("common.actions")}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {shown.map((p) => (
                    <TableRow key={p.id}>
                      <TableCell>
                        <button type="button" className="font-medium underline-offset-4 hover:underline" dir="auto" onClick={() => setOpenProduct(p)}>
                          {p.title || p.name}
                        </button>
                        {p.description ? (
                          <div dir="auto" className="max-w-72 truncate text-xs text-muted-foreground">
                            {p.description}
                          </div>
                        ) : null}
                      </TableCell>
                      <TableCell>
                        <Badge variant="outline">{p.source_kind || "repo"}</Badge>
                      </TableCell>
                      <TableCell>
                        <Badge variant={statusVariant(p.status)}>{p.status}</Badge>
                      </TableCell>
                      <TableCell>
                        <div className="flex flex-wrap justify-end gap-2">
                          <Button size="sm" variant="secondary" onClick={() => setOpenProduct(p)}>
                            {t("products.open")}
                          </Button>
                          {manage ? <ArchiveProduct path={`${base}/${p.id}`} product={p} onArchived={load} /> : null}
                        </div>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </TableBand>
          </>
        )}
      </section>

      {openProduct ? (
        <ProductDetailDialog
          team={team.slug}
          base={base}
          product={openProduct}
          manage={manage}
          operate={operate}
          onClose={() => setOpenProduct(null)}
          onChanged={load}
        />
      ) : null}
    </>
  )
}

function ArchiveProduct({ path, product, onArchived }: { path: string; product: Product; onArchived: () => Promise<void> | void }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  return (
    <ConfirmButton
      icon={<ArchiveIcon />}
      label={t("products.archive")}
      title={t("products.archiveTitle", { name: product.name })}
      description={t("products.archiveBody")}
      confirmLabel={t("products.archive")}
      onConfirm={async () => {
        try {
          await apiFetch(path, { method: "DELETE" })
        } catch (err) {
          throw new Error(errorText(err))
        }
        await onArchived()
      }}
    />
  )
}

/** The create-a-product wizard: from an existing app, a catalogue recipe, or a bare repo — all
 * three just describe how the product's instances are deployed (internal/deploy handles it the
 * same way regardless). */
function ProductDialog({ path, onSaved, ...controlled }: { path: string; onSaved: () => Promise<void> | void } & Controlled) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [name, setName] = useState("")
  const [title, setTitle] = useState("")
  const [description, setDescription] = useState("")
  const [sourceKind, setSourceKind] = useState<"app" | "recipe" | "repo">("repo")
  const [sourceRef, setSourceRef] = useState("")
  const [repoUrl, setRepoUrl] = useState("")
  const [branch, setBranch] = useState("main")
  const [port, setPort] = useState("3000")
  const [healthPath, setHealthPath] = useState("/")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const { open, setOpen, controlled: fromCaller } = useDialogOpen(controlled, reset)

  function reset() {
    setError(null)
    setName("")
    setTitle("")
    setDescription("")
    setSourceKind("repo")
    setSourceRef("")
    setRepoUrl("")
    setBranch("main")
    setPort("3000")
    setHealthPath("/")
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await apiFetch(path, {
        method: "POST",
        body: {
          name: name.trim(),
          title: title.trim(),
          description: description.trim(),
          source_kind: sourceKind,
          source_ref: sourceRef.trim(),
          repo_url: repoUrl.trim(),
          branch: branch.trim(),
          port: Number(port) || undefined,
          healthcheck_path: healthPath.trim(),
        },
      })
      setOpen(false)
      await onSaved()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      {fromCaller ? null : (
        <DialogTrigger render={<Button variant="primary" size="sm" />}>
          <PlusIcon />
          {t("products.add")}
        </DialogTrigger>
      )}
      <DialogContent className="max-h-[90dvh] max-w-2xl overflow-y-auto">
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{t("products.add")}</DialogTitle>
            <DialogDescription>{t("products.dialogHint")}</DialogDescription>
          </DialogHeader>
          <div className="flex w-full flex-col gap-5">
            <Field>
              <FieldLabel htmlFor="prod-kind">{t("products.sourceKind")}</FieldLabel>
              <Select value={sourceKind} onValueChange={(v) => setSourceKind(v as typeof sourceKind)}>
                <SelectTrigger id="prod-kind">
                  <SelectValue>{(v: string) => t(`products.sourceKindValues.${v}`, undefined, v)}</SelectValue>
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="app">{t("products.sourceKindValues.app")}</SelectItem>
                  <SelectItem value="recipe">{t("products.sourceKindValues.recipe")}</SelectItem>
                  <SelectItem value="repo">{t("products.sourceKindValues.repo")}</SelectItem>
                </SelectContent>
              </Select>
              <FieldDescription>{t("products.sourceKindHint")}</FieldDescription>
            </Field>
            {sourceKind !== "repo" ? (
              <Field>
                <FieldLabel htmlFor="prod-ref">{t("products.sourceRef")}</FieldLabel>
                <Input id="prod-ref" dir="ltr" maxLength={120} value={sourceRef} onChange={(e) => setSourceRef(e.target.value)} />
              </Field>
            ) : null}
            <Field>
              <FieldLabel htmlFor="prod-name">{t("products.name")}</FieldLabel>
              <Input id="prod-name" dir="ltr" required maxLength={32} value={name} onChange={(e) => setName(e.target.value)} placeholder="my-app" />
              <FieldDescription>{t("products.nameHint")}</FieldDescription>
            </Field>
            <Field>
              <FieldLabel htmlFor="prod-title">{t("products.displayTitle")}</FieldLabel>
              <Input id="prod-title" dir="auto" maxLength={80} value={title} onChange={(e) => setTitle(e.target.value)} />
            </Field>
            <Field>
              <FieldLabel htmlFor="prod-desc">{t("products.description")}</FieldLabel>
              <Input id="prod-desc" dir="auto" maxLength={280} value={description} onChange={(e) => setDescription(e.target.value)} />
            </Field>
            <Field>
              <FieldLabel htmlFor="prod-repo">{t("products.repoUrl")}</FieldLabel>
              <Input id="prod-repo" dir="ltr" required maxLength={500} value={repoUrl} onChange={(e) => setRepoUrl(e.target.value)} placeholder="https://github.com/team/app" />
            </Field>
            <div className="grid gap-4 sm:grid-cols-2">
              <Field>
                <FieldLabel htmlFor="prod-branch">{t("products.branch")}</FieldLabel>
                <Input id="prod-branch" dir="ltr" maxLength={200} value={branch} onChange={(e) => setBranch(e.target.value)} />
              </Field>
              <Field>
                <FieldLabel htmlFor="prod-port">{t("products.port")}</FieldLabel>
                <Input id="prod-port" dir="ltr" type="number" min={1} max={65535} value={port} onChange={(e) => setPort(e.target.value)} />
              </Field>
            </div>
            <Field>
              <FieldLabel htmlFor="prod-health">{t("products.healthPath")}</FieldLabel>
              <Input id="prod-health" dir="ltr" maxLength={200} value={healthPath} onChange={(e) => setHealthPath(e.target.value)} />
            </Field>
          </div>
          <FormError message={error} />
          <DialogFooter>
            <Button type="button" variant="secondary" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button variant="primary" type="submit" disabled={busy || !name.trim() || !repoUrl.trim()}>
              {busy ? t("common.working") : t("common.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

/** One product's instances, upgrades and keys — provision/suspend/resume/destroy per customer,
 * roll everyone to a new ref, mint/revoke billing-system API keys, and copy-paste curl examples
 * for the public instance-provisioning API. */
function ProductDetailDialog({
  team,
  base,
  product,
  manage,
  operate,
  onClose,
  onChanged,
}: {
  team: string
  base: string
  product: Product
  manage: boolean
  operate: boolean
  onClose: () => void
  onChanged: () => Promise<void> | void
}) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const path = `${base}/${product.id}`
  const [detail, setDetail] = useState<ProductDetail | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [servers, setServers] = useState<Server[] | null>(null)
  const [upgrade, setUpgrade] = useState<ProductUpgrade | null>(null)

  const load = useCallback(async () => {
    try {
      const d = await apiFetch<ProductDetail>(path)
      setDetail(d)
      setError(null)
    } catch (err) {
      setError(errorText(err))
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path])

  useEffect(() => {
    void load()
    apiFetch<{ servers: Server[] }>(`/api/teams/${team}/servers`)
      .then((r) => setServers(r.servers ?? []))
      .catch(() => setServers([]))
  }, [load, team])

  // Poll a running upgrade every 2s until it finishes.
  useEffect(() => {
    if (!upgrade || upgrade.status === "ok" || upgrade.status === "failed") return
    const id = setInterval(async () => {
      try {
        const u = await apiFetch<ProductUpgrade>(`${path}/upgrades/${upgrade.id}`)
        setUpgrade(u)
        if (u.status === "ok" || u.status === "failed") await load()
      } catch {
        // transient; keep polling
      }
    }, 2000)
    return () => clearInterval(id)
  }, [upgrade, path, load])

  async function refreshAll() {
    await load()
    await onChanged()
  }

  async function startUpgrade() {
    try {
      const u = await apiFetch<ProductUpgrade>(`${path}/upgrades`, { method: "POST", body: {} })
      setUpgrade(u)
      toast.success(t("products.upgrade.started"))
    } catch (err) {
      toast.error(errorText(err))
    }
  }

  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="max-h-[90dvh] max-w-3xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2" dir="auto">
            {product.title || product.name}
            <Badge variant={statusVariant(product.status)}>{product.status}</Badge>
          </DialogTitle>
          <DialogDescription dir="ltr">{product.repo_url}</DialogDescription>
        </DialogHeader>
        <ErrorBand message={error} />

        {upgrade ? (
          <div className="flex flex-col gap-2 border border-border p-3">
            <Progress value={upgrade.total > 0 ? Math.round(((upgrade.completed + upgrade.failed) / upgrade.total) * 100) : 0} label={t("products.upgrade.progress")} showValue />
            <p className="text-xs text-muted-foreground">
              {t("products.upgrade.counts", { completed: upgrade.completed, failed: upgrade.failed, total: upgrade.total })} ·{" "}
              <Badge variant={statusVariant(upgrade.status)}>{upgrade.status}</Badge>
            </p>
            {upgrade.error ? <p className="text-xs text-nq-danger-text">{upgrade.error}</p> : null}
          </div>
        ) : null}

        <Tabs defaultValue="instances">
          <TabsList>
            <TabsTab value="instances">{t("products.tabs.instances")}</TabsTab>
            <TabsTab value="keys">{t("products.tabs.keys")}</TabsTab>
            <TabsTab value="api">{t("products.tabs.api")}</TabsTab>
          </TabsList>

          <TabsPanel value="instances">
            <div className="flex flex-wrap items-center justify-between gap-2 py-2">
              {operate ? <ProvisionInstanceDialog path={`${path}/instances`} servers={servers ?? []} onSaved={refreshAll} /> : null}
              {operate ? (
                <Button size="sm" variant="secondary" onClick={() => void startUpgrade()} disabled={!!upgrade && upgrade.status !== "ok" && upgrade.status !== "failed"}>
                  <RefreshCwIcon />
                  {t("products.upgrade.action")}
                </Button>
              ) : null}
            </div>
            {detail === null ? (
              <LoadingRows rows={2} />
            ) : detail.instances.length === 0 ? (
              <p className="border-t border-border py-6 text-center text-sm text-muted-foreground">{t("products.instances.empty")}</p>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t("products.instances.customer")}</TableHead>
                    <TableHead>{t("products.instances.hostname")}</TableHead>
                    <TableHead>{t("products.status")}</TableHead>
                    <TableHead className="text-end">{t("common.actions")}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {detail.instances.map((i) => (
                    <InstanceRow key={i.id} path={`${path}/instances/${i.id}`} instance={i} operate={operate} manage={manage} onChanged={refreshAll} />
                  ))}
                </TableBody>
              </Table>
            )}
          </TabsPanel>

          <TabsPanel value="keys">
            <div className="flex items-center justify-end py-2">
              {manage ? <MintKeyDialog path={`${path}/keys`} onSaved={refreshAll} /> : null}
            </div>
            {detail === null ? (
              <LoadingRows rows={2} />
            ) : detail.keys.length === 0 ? (
              <p className="border-t border-border py-6 text-center text-sm text-muted-foreground">{t("products.keys.empty")}</p>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t("products.keys.name")}</TableHead>
                    <TableHead>{t("products.keys.webhook")}</TableHead>
                    <TableHead className="text-end">{t("common.actions")}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {detail.keys.map((k) => (
                    <TableRow key={k.id}>
                      <TableCell dir="auto">
                        {k.name || t("products.keys.unnamed")}
                        {k.revoked_at ? (
                          <Badge variant="danger" className="ms-2">
                            {t("products.keys.revoked")}
                          </Badge>
                        ) : null}
                      </TableCell>
                      <TableCell dir="ltr" className="max-w-52 truncate text-xs text-muted-foreground">
                        {k.webhook_url || "—"}
                      </TableCell>
                      <TableCell>
                        <div className="flex justify-end">
                          {manage && !k.revoked_at ? (
                            <ConfirmButton
                              size="sm"
                              icon={<Trash2Icon />}
                              label={t("products.keys.revoke")}
                              title={t("products.keys.revokeTitle", { name: k.name || k.id })}
                              description={t("products.keys.revokeBody")}
                              confirmLabel={t("products.keys.revoke")}
                              onConfirm={async () => {
                                try {
                                  await apiFetch(`${path}/keys/${k.id}/revoke`, { method: "POST" })
                                } catch (err) {
                                  throw new Error(errorText(err))
                                }
                                await refreshAll()
                              }}
                            />
                          ) : null}
                        </div>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </TabsPanel>

          <TabsPanel value="api">
            <ApiExamples team={team} product={product} />
          </TabsPanel>
        </Tabs>
      </DialogContent>
    </Dialog>
  )
}

function InstanceRow({
  path,
  instance,
  operate,
  manage,
  onChanged,
}: {
  path: string
  instance: ProductInstance
  operate: boolean
  manage: boolean
  onChanged: () => Promise<void> | void
}) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [busy, setBusy] = useState<"suspend" | "resume" | null>(null)
  const suspended = instance.status === "suspended"

  async function toggle() {
    const action = suspended ? "resume" : "suspend"
    setBusy(action)
    try {
      await apiFetch(`${path}/${action}`, { method: "POST" })
      await onChanged()
    } catch (err) {
      toast.error(errorText(err))
    } finally {
      setBusy(null)
    }
  }

  return (
    <TableRow>
      <TableCell dir="auto">
        <span className="font-medium">{instance.customer_ref}</span>
        <div className="text-xs text-muted-foreground" dir="ltr">
          {instance.name}
        </div>
      </TableCell>
      <TableCell dir="ltr" className="text-xs text-muted-foreground">
        {instance.hostname || "—"}
      </TableCell>
      <TableCell>
        <Badge variant={statusVariant(instance.status)}>{instance.status}</Badge>
      </TableCell>
      <TableCell>
        <div className="flex flex-wrap justify-end gap-2">
          {operate ? (
            <Button size="sm" variant="secondary" disabled={busy !== null} onClick={() => void toggle()}>
              {busy ? t("common.working") : suspended ? t("products.instances.resume") : t("products.instances.suspend")}
            </Button>
          ) : null}
          {manage ? <DestroyInstance path={path} instance={instance} onDestroyed={onChanged} /> : null}
        </div>
      </TableCell>
    </TableRow>
  )
}

/** Destroy an instance: type its exact name to confirm (same pattern as deleting an app). */
function DestroyInstance({ path, instance, onDestroyed }: { path: string; instance: ProductInstance; onDestroyed: () => Promise<void> | void }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [open, setOpen] = useState(false)
  const [typed, setTyped] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function run() {
    setBusy(true)
    setError(null)
    try {
      await apiFetch(path, { method: "DELETE", body: { confirm: typed } })
      setOpen(false)
      await onDestroyed()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        setOpen(o)
        setTyped("")
        setError(null)
      }}
    >
      <DialogTrigger render={<Button size="sm" variant="danger" />}>
        <Trash2Icon />
        {t("products.instances.destroy")}
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("products.instances.destroyTitle")}</DialogTitle>
          <DialogDescription>{t("products.instances.destroyConfirm", { name: instance.name })}</DialogDescription>
        </DialogHeader>
        <Input dir="ltr" value={typed} onChange={(e) => setTyped(e.target.value)} aria-label={t("products.instances.name")} autoComplete="off" />
        {error ? (
          <p role="alert" className="text-sm text-destructive" dir="auto">
            {error}
          </p>
        ) : null}
        <DialogFooter>
          <Button variant="secondary" onClick={() => setOpen(false)}>
            {t("common.cancel")}
          </Button>
          <Button variant="danger" disabled={busy || typed !== instance.name} onClick={run}>
            {busy ? t("common.working") : t("products.instances.destroy")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function ProvisionInstanceDialog({ path, servers, onSaved }: { path: string; servers: Server[]; onSaved: () => Promise<void> | void }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [server, setServer] = useState("")
  const [customerRef, setCustomerRef] = useState("")
  const [name, setName] = useState("")
  const [env, setEnv] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const { open, setOpen, controlled: fromCaller } = useDialogOpen({}, reset)
  const enrolled = useMemo(() => servers.filter((s) => s.status === "enrolled"), [servers])

  function reset() {
    setError(null)
    setServer(enrolled[0]?.id ?? "")
    setCustomerRef("")
    setName("")
    setEnv("")
  }

  function parseEnv(text: string): Record<string, string> {
    const out: Record<string, string> = {}
    for (const line of text.split("\n")) {
      const trimmed = line.trim()
      if (!trimmed || trimmed.startsWith("#")) continue
      const i = trimmed.indexOf("=")
      if (i < 1) continue
      out[trimmed.slice(0, i).trim()] = trimmed.slice(i + 1).trim()
    }
    return out
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await apiFetch(path, {
        method: "POST",
        body: { server, customer_ref: customerRef.trim(), name: name.trim(), env: parseEnv(env) },
      })
      setOpen(false)
      await onSaved()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      {fromCaller ? null : (
        <DialogTrigger render={<Button variant="primary" size="sm" />}>
          <PlusIcon />
          {t("products.instances.add")}
        </DialogTrigger>
      )}
      <DialogContent className="max-h-[90dvh] overflow-y-auto">
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{t("products.instances.add")}</DialogTitle>
            <DialogDescription>{t("products.instances.dialogHint")}</DialogDescription>
          </DialogHeader>
          <div className="flex w-full flex-col gap-5">
            <Field>
              <FieldLabel htmlFor="inst-server">{t("products.instances.server")}</FieldLabel>
              {enrolled.length === 0 ? (
                <p className="text-sm text-muted-foreground">{t("products.instances.noServers")}</p>
              ) : (
                <Select value={server} onValueChange={(v) => setServer(v as string)}>
                  <SelectTrigger id="inst-server">
                    <SelectValue>{(v: string) => enrolled.find((s) => s.id === v)?.name ?? v}</SelectValue>
                  </SelectTrigger>
                  <SelectContent>
                    {enrolled.map((s) => (
                      <SelectItem key={s.id} value={s.id}>
                        {s.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              )}
            </Field>
            <Field>
              <FieldLabel htmlFor="inst-customer">{t("products.instances.customer")}</FieldLabel>
              <Input id="inst-customer" dir="ltr" required maxLength={120} value={customerRef} onChange={(e) => setCustomerRef(e.target.value)} />
              <FieldDescription>{t("products.instances.customerHint")}</FieldDescription>
            </Field>
            <Field>
              <FieldLabel htmlFor="inst-name">{t("products.instances.name")}</FieldLabel>
              <Input id="inst-name" dir="ltr" maxLength={32} value={name} onChange={(e) => setName(e.target.value)} placeholder={t("products.instances.namePlaceholder")} />
            </Field>
            <Field>
              <FieldLabel htmlFor="inst-env">{t("products.instances.env")}</FieldLabel>
              <Textarea id="inst-env" dir="ltr" rows={5} value={env} onChange={(e) => setEnv(e.target.value)} className="font-mono text-xs" placeholder="KEY=value" />
              <FieldDescription>{t("products.instances.envHint")}</FieldDescription>
            </Field>
          </div>
          <FormError message={error} />
          <DialogFooter>
            <Button type="button" variant="secondary" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button variant="primary" type="submit" disabled={busy || !customerRef.trim() || (enrolled.length > 0 && !server)}>
              {busy ? t("common.working") : t("products.instances.add")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

/** Mint a product-scoped billing-system key. The token, and the webhook secret if a webhook URL
 * was given, are shown exactly once — the API never returns them again. */
function MintKeyDialog({ path, onSaved }: { path: string; onSaved: () => Promise<void> | void }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [name, setName] = useState("")
  const [webhookUrl, setWebhookUrl] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [minted, setMinted] = useState<MintedProductKey | null>(null)
  const { open, setOpen, controlled: fromCaller } = useDialogOpen({}, reset)

  function reset() {
    setError(null)
    setName("")
    setWebhookUrl("")
    setMinted(null)
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const r = await apiFetch<MintedProductKey>(path, { method: "POST", body: { name: name.trim(), webhook_url: webhookUrl.trim() } })
      setMinted(r)
      await onSaved()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        setOpen(o)
        if (!o) reset()
      }}
    >
      {fromCaller ? null : (
        <DialogTrigger render={<Button variant="primary" size="sm" />}>
          <KeyRoundIcon />
          {t("products.keys.add")}
        </DialogTrigger>
      )}
      <DialogContent className="max-h-[90dvh] overflow-y-auto">
        {minted ? (
          <>
            <DialogHeader>
              <DialogTitle>{t("products.keys.mintedTitle")}</DialogTitle>
              <DialogDescription>{t("products.keys.mintedHint")}</DialogDescription>
            </DialogHeader>
            <div className="flex flex-col gap-3">
              <div>
                <p className="mb-1 text-xs font-medium text-muted-foreground">{t("products.keys.token")}</p>
                <CopyField value={minted.token} />
              </div>
              {minted.webhook_secret ? (
                <div>
                  <p className="mb-1 text-xs font-medium text-muted-foreground">{t("products.keys.webhookSecret")}</p>
                  <CopyField value={minted.webhook_secret} />
                </div>
              ) : null}
            </div>
            <DialogFooter>
              <Button variant="primary" onClick={() => setOpen(false)}>{t("common.done")}</Button>
            </DialogFooter>
          </>
        ) : (
          <form onSubmit={submit} className="flex flex-col gap-4">
            <DialogHeader>
              <DialogTitle>{t("products.keys.add")}</DialogTitle>
              <DialogDescription>{t("products.keys.dialogHint")}</DialogDescription>
            </DialogHeader>
            <div className="flex w-full flex-col gap-5">
              <Field>
                <FieldLabel htmlFor="key-name">{t("products.keys.name")}</FieldLabel>
                <Input id="key-name" dir="auto" maxLength={80} value={name} onChange={(e) => setName(e.target.value)} />
              </Field>
              <Field>
                <FieldLabel htmlFor="key-webhook">{t("products.keys.webhook")}</FieldLabel>
                <Input id="key-webhook" dir="ltr" maxLength={500} value={webhookUrl} onChange={(e) => setWebhookUrl(e.target.value)} placeholder="https://example.com/hooks/hosbah" />
                <FieldDescription>{t("products.keys.webhookHint")}</FieldDescription>
              </Field>
            </div>
            <FormError message={error} />
            <DialogFooter>
              <Button type="button" variant="secondary" onClick={() => setOpen(false)}>
                {t("common.cancel")}
              </Button>
              <Button variant="primary" type="submit" disabled={busy}>
                {busy ? t("common.working") : t("products.keys.add")}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  )
}

/** curl examples for the team's own billing/signup system, against the public,
 * product-key-authenticated instance API (internal/api/product_keys_public.go). */
function ApiExamples({ team, product }: { team: string; product: Product }) {
  const { t } = useI18n()
  const origin = typeof window !== "undefined" ? window.location.origin : ""
  const provision = `curl -X POST ${origin}/api/products/instances \\
  -H "Authorization: Bearer hbk_…" \\
  -H "Content-Type: application/json" \\
  -d '{"customer_ref":"cust-123","name":"cust-123"}'`
  const suspend = `curl -X POST ${origin}/api/products/instances/{instance}/suspend \\
  -H "Authorization: Bearer hbk_…"`
  const resume = `curl -X POST ${origin}/api/products/instances/{instance}/resume \\
  -H "Authorization: Bearer hbk_…"`
  const destroy = `curl -X DELETE ${origin}/api/products/instances/{instance} \\
  -H "Authorization: Bearer hbk_…" \\
  -H "Content-Type: application/json" \\
  -d '{"confirm":"cust-123"}'`

  return (
    <div className="flex flex-col gap-4 py-2">
      <p dir="auto" className="text-sm text-muted-foreground">
        {t("products.api.hint", { name: product.name })}
      </p>
      <div>
        <p className="mb-1 text-xs font-medium text-muted-foreground">{t("products.api.provision")}</p>
        <CopyField value={provision} block />
      </div>
      <div>
        <p className="mb-1 text-xs font-medium text-muted-foreground">{t("products.api.suspend")}</p>
        <CopyField value={suspend} block />
      </div>
      <div>
        <p className="mb-1 text-xs font-medium text-muted-foreground">{t("products.api.resume")}</p>
        <CopyField value={resume} block />
      </div>
      <div>
        <p className="mb-1 text-xs font-medium text-muted-foreground">{t("products.api.destroy")}</p>
        <CopyField value={destroy} block />
      </div>
      <p dir="auto" className="text-xs text-muted-foreground">
        {t("products.api.webhookHint")}
      </p>
    </div>
  )
}
