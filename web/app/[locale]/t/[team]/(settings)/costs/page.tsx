"use client"

import { Badge, Button, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger, EmptyState, Field, FieldLabel, Input, Progress, Table, TableBody, TableCell, TableHead, TableHeader, TableRow, toast } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useState, type FormEvent, type ReactNode } from "react"
import { ChevronDownIcon, ChevronRightIcon, CircleDollarSignIcon, PencilIcon, PlusIcon, Trash2Icon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { ConfirmButton } from "@/components/confirm-button"
import { useDialogOpen, type Controlled } from "@/components/dialog-state"
import { useErrorText } from "@/components/form-error"
import { MetricChart, type ChartPoint } from "@/components/servers/metrics-chart"
import { ErrorBand, LoadingRows, SectionTitle, TableBand } from "@/components/states"







import { apiFetch } from "@/lib/api"
import { useDocumentTitle, useFormatDate, useFormatMoney, useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

type CurrencyAmount = { currency: string; amount: number }
type UsageOut = { readings: number; avg_cpu_percent: number; p95_cpu_percent: number; mem_percent: number; disk_percent: number }
type AppCostOut = { app_id: string; app_name: string; amount: number; currency?: string; memory_limit_mb?: number; hint?: string }
type ServerCostOut = {
  server_id: string
  server_name: string
  priced: boolean
  amount?: number
  currency?: string
  usage?: UsageOut
  hint?: string
  apps: AppCostOut[]
  idle_amount?: number
}
type LineItemOut = { id: string; name: string; amount: number; currency: string; created_at: string }
type CostsOverview = {
  window: string
  totals: CurrencyAmount[]
  allocated_totals: CurrencyAmount[]
  idle_totals: CurrencyAmount[]
  over_provisioned_count: number
  under_pressure_count: number
  servers: ServerCostOut[]
  line_items: LineItemOut[]
}

const WINDOWS = ["7d", "30d"] as const
type WindowKey = (typeof WINDOWS)[number]

export default function CostsPage() {
  const { t } = useI18n()
  const { team, manage } = useTeam()
  useDocumentTitle(`${t("nav.costs")} — ${team.name}`)
  const errorText = useErrorText()
  const fmtMoney = useFormatMoney()

  const [window_, setWindow] = useState<WindowKey>("30d")
  const [overview, setOverview] = useState<CostsOverview | null>(null)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      const o = await apiFetch<CostsOverview>(`/api/teams/${team.slug}/costs/overview?window=${window_}`)
      setOverview(o)
      setError(null)
    } catch (err) {
      setError(errorText(err))
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [team.slug, window_])

  useEffect(() => {
    void load()
  }, [load])

  const amountList = (totals: CurrencyAmount[]) =>
    totals.length ? totals.map((x) => fmtMoney(x.amount, x.currency)).join(" + ") : fmtMoney(0, "USD")

  return (
    <PageBody title={t("nav.costs")} description={t("costs.description")} bleed>
      <SectionTitle
        title={t("costs.overviewTitle")}
        hint={t("costs.overviewHint")}
        action={
          <div className="flex flex-wrap items-center gap-1" role="group" aria-label={t("servers.metrics.range")}>
            {WINDOWS.map((w) => (
              <Button key={w} size="sm" variant={window_ === w ? "primary" : "secondary"} onClick={() => setWindow(w)}>
                {t(`servers.metrics.ranges.${w}`)}
              </Button>
            ))}
          </div>
        }
      />
      <ErrorBand message={error} />
      {overview === null ? (
        error ? null : <LoadingRows rows={3} />
      ) : (
        <>
          <dl className="grid gap-px border-b border-border bg-border sm:grid-cols-2 lg:grid-cols-4">
            <KpiTile label={t("costs.kpiTotal")}>
              <span dir="ltr" className="text-start font-mono text-sm tabular-nums">
                {amountList(overview.totals)}
              </span>
            </KpiTile>
            <KpiTile label={t("costs.kpiAllocated")}>
              <span dir="ltr" className="text-start font-mono text-sm tabular-nums">
                {amountList(overview.allocated_totals)}
              </span>
            </KpiTile>
            <KpiTile label={t("costs.kpiIdle")}>
              <span dir="ltr" className="text-start font-mono text-sm tabular-nums">
                {amountList(overview.idle_totals)}
              </span>
            </KpiTile>
            <KpiTile label={t("costs.kpiHints")}>
              <span className="flex flex-wrap gap-2">
                <Badge variant="outline" className="text-nq-warning-text">
                  {t("costs.overProvisionedCount", { n: overview.over_provisioned_count })}
                </Badge>
                <Badge variant="outline" className="text-nq-danger-text">
                  {t("costs.underPressureCount", { n: overview.under_pressure_count })}
                </Badge>
              </span>
            </KpiTile>
          </dl>

          <ServersSection team={team.slug} manage={manage} window={window_} servers={overview.servers} onChanged={load} />
          <LineItemsSection team={team.slug} manage={manage} items={overview.line_items} onChanged={load} />
        </>
      )}
    </PageBody>
  )
}

function KpiTile({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-2 bg-background px-6 py-4">
      <dt className="eyebrow">{label}</dt>
      <dd>{children}</dd>
    </div>
  )
}

function hintTone(hint?: string): "outline" | "danger" {
  return hint === "under_pressure" ? "danger" : "outline"
}

// ── Servers ──────────────────────────────────────────────────────────────

function ServersSection({
  team,
  manage,
  window: win,
  servers,
  onChanged,
}: {
  team: string
  manage: boolean
  window: WindowKey
  servers: ServerCostOut[]
  onChanged: () => Promise<void> | void
}) {
  const { t } = useI18n()
  const [expanded, setExpanded] = useState<string | null>(null)

  return (
    <section aria-labelledby="costs-servers-h">
      <SectionTitle id="costs-servers-h" title={t("costs.serversTitle")} hint={t("costs.serversHint")} />
      {servers.length === 0 ? (
        <EmptyState className="border-b" icon={CircleDollarSignIcon} title={t("costs.serversEmptyTitle")} description={t("costs.serversEmptyHint")} />
      ) : (
        <TableBand>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead />
                <TableHead>{t("costs.server")}</TableHead>
                <TableHead>{t("costs.price")}</TableHead>
                <TableHead>{t("servers.cpu")}</TableHead>
                <TableHead>{t("servers.memory")}</TableHead>
                <TableHead>{t("servers.disk")}</TableHead>
                <TableHead>{t("costs.hint")}</TableHead>
                {manage ? <TableHead className="text-end">{t("common.actions")}</TableHead> : null}
              </TableRow>
            </TableHeader>
            <TableBody>
              {servers.map((s) => (
                <ServerRows
                  key={s.server_id}
                  team={team}
                  manage={manage}
                  window={win}
                  server={s}
                  expanded={expanded === s.server_id}
                  onToggle={() => setExpanded((cur) => (cur === s.server_id ? null : s.server_id))}
                  onChanged={onChanged}
                />
              ))}
            </TableBody>
          </Table>
        </TableBand>
      )}
    </section>
  )
}

function UsageBar({ percent, label }: { percent: number | undefined; label: string }) {
  if (percent == null) return <span className="text-xs text-muted-foreground">—</span>
  return (
    <div className="flex w-28 flex-col gap-1">
      <Progress value={percent} aria-label={label} size="sm" />
      <span dir="ltr" className="text-start font-mono text-[11px] tabular-nums text-muted-foreground">
        {percent}%
      </span>
    </div>
  )
}

function ServerRows({
  team,
  manage,
  window: win,
  server: s,
  expanded,
  onToggle,
  onChanged,
}: {
  team: string
  manage: boolean
  window: WindowKey
  server: ServerCostOut
  expanded: boolean
  onToggle: () => void
  onChanged: () => Promise<void> | void
}) {
  const { t } = useI18n()
  const fmtMoney = useFormatMoney()

  return (
    <>
      <TableRow className="align-top">
        <TableCell>
          <Button size="sm" variant="ghost" onClick={onToggle} aria-expanded={expanded} aria-label={t("costs.showDetail")}>
            {expanded ? <ChevronDownIcon /> : <ChevronRightIcon />}
          </Button>
        </TableCell>
        <TableCell>
          <span dir="auto" className="font-medium">
            {s.server_name}
          </span>
        </TableCell>
        <TableCell>
          {s.priced ? (
            <span dir="ltr" className="text-start font-mono text-sm tabular-nums">
              {fmtMoney(s.amount ?? 0, s.currency ?? "USD")}
              {s.idle_amount ? (
                <span className="ms-1 text-xs text-muted-foreground">({t("costs.idleOf", { amount: fmtMoney(s.idle_amount, s.currency ?? "USD") })})</span>
              ) : null}
            </span>
          ) : (
            <Badge variant="outline">{t("costs.unpriced")}</Badge>
          )}
        </TableCell>
        <TableCell>
          <UsageBar percent={s.usage?.avg_cpu_percent != null ? Math.round(s.usage.avg_cpu_percent) : undefined} label={t("servers.cpu")} />
        </TableCell>
        <TableCell>
          <UsageBar percent={s.usage?.mem_percent != null ? Math.round(s.usage.mem_percent) : undefined} label={t("servers.memory")} />
        </TableCell>
        <TableCell>
          <UsageBar percent={s.usage?.disk_percent != null ? Math.round(s.usage.disk_percent) : undefined} label={t("servers.disk")} />
        </TableCell>
        <TableCell>
          {s.hint ? <Badge variant={hintTone(s.hint)}>{t(`costs.hints.${s.hint}`)}</Badge> : <span className="text-xs text-muted-foreground">—</span>}
        </TableCell>
        {manage ? (
          <TableCell>
            <div className="flex flex-wrap justify-end gap-2">
              <PriceDialog team={team} server={s} onSaved={onChanged} />
              {s.priced ? <ClearPrice team={team} server={s} onCleared={onChanged} /> : null}
            </div>
          </TableCell>
        ) : null}
      </TableRow>
      {expanded ? (
        <TableRow>
          <TableCell colSpan={manage ? 8 : 7} className="bg-secondary/40">
            <ServerDetail team={team} server={s} window={win} />
          </TableCell>
        </TableRow>
      ) : null}
    </>
  )
}

function ServerDetail({ team, server: s, window: win }: { team: string; server: ServerCostOut; window: WindowKey }) {
  const { t, locale } = useI18n()
  const fmtMoney = useFormatMoney()
  const [series, setSeries] = useState<{ points: { at: string; cpu: number; mem_used: number; mem_total: number; disk_used: number; disk_total: number }[] } | null>(
    null,
  )
  const [error, setError] = useState<string | null>(null)
  const errorText = useErrorText()

  useEffect(() => {
    setSeries(null)
    apiFetch<{ points: { at: string; cpu: number; mem_used: number; mem_total: number; disk_used: number; disk_total: number }[] }>(
      `/api/teams/${team}/servers/${s.server_id}/metrics?range=${win}`,
    )
      .then((r) => {
        setSeries(r)
        setError(null)
      })
      .catch((err) => setError(errorText(err)))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [team, s.server_id, win])

  const points = series?.points ?? []
  const pct = (used: number, total: number) => (total > 0 ? Math.round((used / total) * 1000) / 10 : null)
  const fmtTime = (iso: string) => {
    const d = new Date(iso)
    return d.toLocaleString(locale === "ar" ? "ar-EG-u-nu-latn" : "en-GB", { dateStyle: "short", timeStyle: "short" })
  }

  return (
    <div className="flex flex-col gap-6 py-4">
      <div>
        <h4 className="mb-2 text-xs font-medium text-muted-foreground">{t("costs.usageChart")}</h4>
        {error ? (
          <ErrorBand message={error} />
        ) : points.length === 0 ? (
          <p className="text-xs text-muted-foreground">{series === null ? t("common.loading") : t("servers.metrics.emptyTitle")}</p>
        ) : (
          <MetricChart
            ariaLabel={t("costs.usageChart")}
            max={100}
            unit="%"
            series={[
              { key: "cpu", label: t("servers.cpu"), color: "var(--nq-brand)", points: points.map((p) => ({ at: p.at, value: p.cpu })) },
              {
                key: "mem",
                label: t("servers.memory"),
                color: "var(--nq-accent)",
                points: points.map((p) => ({ at: p.at, value: pct(p.mem_used, p.mem_total) })) as ChartPoint[],
              },
              {
                key: "disk",
                label: t("servers.disk"),
                color: "var(--nq-success)",
                points: points.map((p) => ({ at: p.at, value: pct(p.disk_used, p.disk_total) })) as ChartPoint[],
              },
            ]}
            formatTime={fmtTime}
          />
        )}
      </div>
      <div>
        <h4 className="mb-2 text-xs font-medium text-muted-foreground">{t("costs.byApp")}</h4>
        {s.apps.length === 0 ? (
          <p className="text-xs text-muted-foreground">{t("costs.noApps")}</p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("costs.app")}</TableHead>
                <TableHead>{t("costs.allocated")}</TableHead>
                <TableHead>{t("costs.memoryLimit")}</TableHead>
                <TableHead>{t("costs.hint")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {s.apps.map((a) => (
                <TableRow key={a.app_id}>
                  <TableCell dir="auto">{a.app_name}</TableCell>
                  <TableCell dir="ltr" className="text-start font-mono text-xs tabular-nums">
                    {s.priced ? fmtMoney(a.amount, a.currency ?? s.currency ?? "USD") : "—"}
                  </TableCell>
                  <TableCell dir="ltr" className="text-start font-mono text-xs tabular-nums">
                    {a.memory_limit_mb ? `${a.memory_limit_mb} MiB` : "—"}
                  </TableCell>
                  <TableCell>
                    {a.hint ? (
                      <Badge variant="outline" className="text-nq-warning-text">
                        {t(`costs.appHints.${a.hint}`)}
                      </Badge>
                    ) : (
                      "—"
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </div>
    </div>
  )
}

function ClearPrice({
  team,
  server: s,
  onCleared,
  ...controlled
}: { team: string; server: ServerCostOut; onCleared: () => Promise<void> | void } & Controlled) {
  const { t } = useI18n()
  const errorText = useErrorText()
  return (
    <ConfirmButton
      {...controlled}
      icon={<Trash2Icon />}
      label={t("costs.clearPrice")}
      title={t("costs.clearPriceTitle", { name: s.server_name })}
      description={t("costs.clearPriceBody")}
      confirmLabel={t("common.delete")}
      onConfirm={async () => {
        try {
          await apiFetch(`/api/teams/${team}/costs/servers/${s.server_id}`, { method: "DELETE" })
        } catch (err) {
          throw new Error(errorText(err))
        }
        await onCleared()
      }}
    />
  )
}

function PriceDialog({
  team,
  server: s,
  onSaved,
  ...controlled
}: { team: string; server: ServerCostOut; onSaved: () => Promise<void> | void } & Controlled) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [amount, setAmount] = useState("")
  const [currency, setCurrency] = useState("USD")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const { open, setOpen, controlled: fromCaller } = useDialogOpen(controlled, reset)

  function reset() {
    setError(null)
    setAmount(s.priced ? String(s.amount ?? 0) : "")
    setCurrency(s.currency || "USD")
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await apiFetch(`/api/teams/${team}/costs/servers/${s.server_id}`, {
        method: "PUT",
        body: { amount: Number(amount) || 0, currency: currency.trim().toUpperCase() || "USD" },
      })
      toast.success(t("costs.priceSaved"))
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
        <DialogTrigger render={<Button size="sm" variant={s.priced ? "secondary" : "primary"} />}>
          <PencilIcon />
          {s.priced ? t("costs.editPrice") : t("costs.setPrice")}
        </DialogTrigger>
      )}
      <DialogContent>
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{s.server_name}</DialogTitle>
            <DialogDescription>{t("costs.priceHint")}</DialogDescription>
          </DialogHeader>
          <div className="flex w-full flex-col gap-5">
            <Field>
              <FieldLabel htmlFor="cost-amount">{t("costs.amount")}</FieldLabel>
              <Input
                id="cost-amount"
                dir="ltr"
                type="number"
                min={0}
                step="0.01"
                required
                value={amount}
                onChange={(e) => setAmount(e.target.value)}
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="cost-currency">{t("costs.currency")}</FieldLabel>
              <Input
                id="cost-currency"
                dir="ltr"
                maxLength={3}
                minLength={3}
                required
                className="w-24 font-mono uppercase"
                value={currency}
                onChange={(e) => setCurrency(e.target.value.toUpperCase())}
              />
            </Field>
          </div>
          {error ? (
            <p role="alert" className="text-sm text-destructive" dir="auto">
              {error}
            </p>
          ) : null}
          <DialogFooter>
            <Button type="button" variant="secondary" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button variant="primary" type="submit" disabled={busy || !amount}>
              {busy ? t("common.working") : t("common.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ── Line items ───────────────────────────────────────────────────────────

function LineItemsSection({
  team,
  manage,
  items,
  onChanged,
}: {
  team: string
  manage: boolean
  items: LineItemOut[]
  onChanged: () => Promise<void> | void
}) {
  const { t } = useI18n()
  const fmtMoney = useFormatMoney()
  const fmtDate = useFormatDate()
  const add = manage ? <LineItemDialog team={team} onSaved={onChanged} /> : null

  return (
    <section aria-labelledby="costs-lineitems-h">
      <SectionTitle id="costs-lineitems-h" title={t("costs.lineItemsTitle")} hint={t("costs.lineItemsHint")} action={add} />
      {items.length === 0 ? (
        <EmptyState className="border-b" icon={CircleDollarSignIcon} title={t("costs.lineItemsEmptyTitle")} description={t("costs.lineItemsEmptyHint")} actions={<>{add}</>} />
      ) : (
        <TableBand>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("costs.name")}</TableHead>
                <TableHead>{t("costs.amount")}</TableHead>
                <TableHead>{t("costs.added")}</TableHead>
                {manage ? <TableHead className="text-end">{t("common.actions")}</TableHead> : null}
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((li) => (
                <TableRow key={li.id}>
                  <TableCell dir="auto">{li.name}</TableCell>
                  <TableCell dir="ltr" className="text-start font-mono text-sm tabular-nums">
                    {fmtMoney(li.amount, li.currency)}
                  </TableCell>
                  <TableCell dir="ltr" className="text-start text-xs text-muted-foreground">
                    {fmtDate(li.created_at)}
                  </TableCell>
                  {manage ? (
                    <TableCell>
                      <div className="flex flex-wrap justify-end gap-2">
                        <LineItemDialog team={team} item={li} onSaved={onChanged} />
                        <DeleteLineItem team={team} item={li} onDeleted={onChanged} />
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
  )
}

function DeleteLineItem({
  team,
  item,
  onDeleted,
  ...controlled
}: { team: string; item: LineItemOut; onDeleted: () => Promise<void> | void } & Controlled) {
  const { t } = useI18n()
  const errorText = useErrorText()
  return (
    <ConfirmButton
      {...controlled}
      icon={<Trash2Icon />}
      label={t("common.delete")}
      title={t("costs.deleteLineItemTitle", { name: item.name })}
      description={t("costs.deleteLineItemBody")}
      confirmLabel={t("common.delete")}
      onConfirm={async () => {
        try {
          await apiFetch(`/api/teams/${team}/costs/line-items/${item.id}`, { method: "DELETE" })
        } catch (err) {
          throw new Error(errorText(err))
        }
        await onDeleted()
      }}
    />
  )
}

function LineItemDialog({
  team,
  item,
  onSaved,
  ...controlled
}: { team: string; item?: LineItemOut; onSaved: () => Promise<void> | void } & Controlled) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [name, setName] = useState("")
  const [amount, setAmount] = useState("")
  const [currency, setCurrency] = useState("USD")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const { open, setOpen, controlled: fromCaller } = useDialogOpen(controlled, reset)

  function reset() {
    setError(null)
    setName(item?.name ?? "")
    setAmount(item ? String(item.amount) : "")
    setCurrency(item?.currency ?? "USD")
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    const body = { name: name.trim(), amount: Number(amount) || 0, currency: currency.trim().toUpperCase() || "USD" }
    try {
      await apiFetch(item ? `/api/teams/${team}/costs/line-items/${item.id}` : `/api/teams/${team}/costs/line-items`, {
        method: item ? "PUT" : "POST",
        body,
      })
      toast.success(t("costs.lineItemSaved"))
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
        <DialogTrigger render={<Button size="sm" variant={item ? "secondary" : "primary"} />}>
          {item ? <PencilIcon /> : <PlusIcon />}
          {item ? t("lists.edit") : t("costs.addLineItem")}
        </DialogTrigger>
      )}
      <DialogContent>
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{item ? item.name : t("costs.addLineItem")}</DialogTitle>
            <DialogDescription>{t("costs.lineItemsHint")}</DialogDescription>
          </DialogHeader>
          <div className="flex w-full flex-col gap-5">
            <Field>
              <FieldLabel htmlFor="li-name">{t("costs.name")}</FieldLabel>
              <Input id="li-name" dir="auto" required maxLength={120} value={name} onChange={(e) => setName(e.target.value)} />
            </Field>
            <Field>
              <FieldLabel htmlFor="li-amount">{t("costs.amount")}</FieldLabel>
              <Input
                id="li-amount"
                dir="ltr"
                type="number"
                min={0}
                step="0.01"
                required
                value={amount}
                onChange={(e) => setAmount(e.target.value)}
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="li-currency">{t("costs.currency")}</FieldLabel>
              <Input
                id="li-currency"
                dir="ltr"
                maxLength={3}
                minLength={3}
                required
                className="w-24 font-mono uppercase"
                value={currency}
                onChange={(e) => setCurrency(e.target.value.toUpperCase())}
              />
            </Field>
          </div>
          {error ? (
            <p role="alert" className="text-sm text-destructive" dir="auto">
              {error}
            </p>
          ) : null}
          <DialogFooter>
            <Button type="button" variant="secondary" onClick={() => setOpen(false)}>
              {t("common.cancel")}
            </Button>
            <Button variant="primary" type="submit" disabled={busy || !name.trim() || !amount}>
              {busy ? t("common.working") : t("common.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
