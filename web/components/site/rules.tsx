"use client"

import {
  Badge,
  Button,
  Checkbox,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Field,
  FieldDescription,
  FieldLabel,
  Input,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Textarea,
  type DataTableRowAction,
} from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useState, type FormEvent, type ReactNode } from "react"
import { KeyRoundIcon, PencilIcon, PlusIcon, SettingsIcon, Trash2Icon, XIcon } from "lucide-react"

import { FormError, useErrorText } from "@/components/form-error"
import { useLoadedSiteOptional } from "@/components/site/context"
import { NasaqTable } from "@/components/lists/nasaq-table"
import { RowMenu, RowWithActions } from "@/components/lists/row-actions"
import { Band, ErrorBand, LoadingRows, SectionTitle, SettingsRow } from "@/components/states"
import { apiFetch } from "@/lib/api"
import { useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

type RedirectRule = { from: string; to: string; code?: number; preserve_query?: boolean }
type HeaderAction = "set" | "add" | "remove"
type HeaderRule = { action: HeaderAction; name: string; value?: string }
/** On write, a password sets (or changes) the user's password; without one the current one is kept. */
type BasicAuthUser = { username: string; password?: string }
type BasicAuthRule = { enabled: boolean; path_prefix?: string; exempt_paths?: string[]; users: BasicAuthUser[] }
type AccessRules = { ip_allow?: string[]; ip_deny?: string[]; block_bad_bots?: boolean }
type HTTPRules = {
  redirects: RedirectRule[]
  headers: HeaderRule[]
  basic_auth: BasicAuthRule | null
  access: AccessRules | null
  status: string
  error?: string
}

const REDIRECT_CODES = [301, 302, 307, 308]

const HEADER_PRESETS: { key: string; name: string; value: string }[] = [
  { key: "hsts", name: "Strict-Transport-Security", value: "max-age=63072000; includeSubDomains; preload" },
  { key: "nosniff", name: "X-Content-Type-Options", value: "nosniff" },
  { key: "frame", name: "X-Frame-Options", value: "DENY" },
  { key: "cors", name: "Access-Control-Allow-Origin", value: "*" },
]

function statusVariant(status: string): "success" | "neutral" | "outline" | "danger" {
  if (status === "error") return "danger"
  if (status === "pending") return "neutral"
  return "outline"
}

function emptyRules(): HTTPRules {
  return { redirects: [], headers: [], basic_auth: null, access: null, status: "ok" }
}

type Editing =
  | { kind: "redirect"; index: number | null }
  | { kind: "header"; index: number | null }
  | { kind: "user"; username: string | null }
  | { kind: "auth" }
  | { kind: "cidr"; list: "ip_allow" | "ip_deny" }

/**
 * Redirects, response headers and access rules (MH-578 E4-E6) rendered into this app's own Caddy
 * route by the agent. Rate limiting is not offered: the agent's plain Caddy build has no rate-limit
 * module, and this project does not rebuild Caddy to add one (see internal/deploy/httprules.go).
 * Each rule is added and edited in a dialog; removing is one click (the rules are re-applied at once).
 *
 * Reused for proxy hosts (MH-578 C-rules): pass `basePath` to point at
 * `/api/teams/{team}/proxy-hosts/{proxy}/rules` instead of an app's own `{path}/rules`. Without it,
 * this falls back to the app rendered by `useLoadedSite()`.
 */
export function SiteRules({ basePath }: { basePath?: string } = {}) {
  const { t } = useI18n()
  const { manage } = useTeam()
  const site = useLoadedSiteOptional()
  const errorText = useErrorText()
  const base = basePath ?? `${site?.path}/rules`

  const [rules, setRules] = useState<HTTPRules | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [editing, setEditing] = useState<Editing | null>(null)

  const load = useCallback(() => {
    apiFetch<HTTPRules>(base)
      .then((r) => {
        setRules(r)
        setError(null)
      })
      .catch((err) => setError(errorText(err)))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [base])
  useEffect(load, [load])

  const current = rules ?? emptyRules()

  /** Saves the whole rule set; throws the readable error so a dialog can show it. */
  async function put(next: Partial<HTTPRules>) {
    const merged = { ...current, ...next }
    const body = {
      redirects: merged.redirects,
      headers: merged.headers,
      basic_auth: merged.basic_auth ? { ...merged.basic_auth, users: merged.basic_auth.users.map((u) => (u.password ? u : { username: u.username })) } : null,
      access: merged.access,
    }
    try {
      setRules(await apiFetch<HTTPRules>(base, { method: "PUT", body }))
    } catch (err) {
      throw new Error(errorText(err))
    }
  }

  /** A one-click change (remove, toggle): errors land in the page's error band. */
  async function quick(next: Partial<HTTPRules>) {
    setBusy(true)
    setError(null)
    try {
      await put(next)
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const auth = (next: Partial<BasicAuthRule>): BasicAuthRule => ({
    enabled: next.enabled ?? current.basic_auth?.enabled ?? false,
    path_prefix: next.path_prefix ?? current.basic_auth?.path_prefix ?? "",
    exempt_paths: next.exempt_paths ?? current.basic_auth?.exempt_paths ?? [],
    users: next.users ?? current.basic_auth?.users ?? [],
  })
  const access = (next: Partial<AccessRules>): AccessRules => ({
    ip_allow: next.ip_allow ?? current.access?.ip_allow ?? [],
    ip_deny: next.ip_deny ?? current.access?.ip_deny ?? [],
    block_bad_bots: next.block_bad_bots ?? current.access?.block_bad_bots ?? false,
  })

  const addButton = (label: string, open: Editing) =>
    manage ? (
      <Button size="sm" variant="secondary" disabled={!rules} onClick={() => setEditing(open)}>
        <PlusIcon />
        {label}
      </Button>
    ) : null

  // Edit and remove behind each row's ⋯ menu (and its context menu).
  const rowActions = (onEdit: () => void, onRemove: () => void): DataTableRowAction[] => [
    { id: "edit", label: t("lists.edit"), icon: PencilIcon, disabled: busy, onSelect: onEdit },
    { id: "remove", label: t("site.rules.remove"), icon: Trash2Icon, danger: true, group: "danger", disabled: busy, onSelect: onRemove },
  ]
  const redirects = current.redirects.map((r, i) => ({ ...r, i }))
  const headers = current.headers.map((h, i) => ({ ...h, i }))

  const empty = (text: string) => (
    <Band>
      <p className="text-sm text-muted-foreground">{text}</p>
    </Band>
  )

  const users = current.basic_auth?.users ?? []

  return (
    <div className="flex flex-col gap-6" data-testid="rules">
      <section aria-labelledby="rules-redirects-h">
        <SectionTitle
          id="rules-redirects-h"
          title={t("site.rules.redirects.title")}
          hint={t("site.rules.redirects.hint")}
          action={addButton(t("site.rules.redirects.add"), { kind: "redirect", index: null })}
        />
        {rules === null ? (
          <LoadingRows rows={2} />
        ) : current.redirects.length === 0 ? (
          empty(t("site.rules.redirects.empty"))
        ) : (
          <NasaqTable
            data={redirects}
            rowId={(r) => String(r.i)}
            rowLabel={(r) => r.from}
            label={t("site.rules.redirects.title")}
            columns={[
              { id: "from", header: t("site.rules.redirects.from"), cell: (r) => <span dir="ltr" className="font-mono text-xs">{r.from}</span>, searchValue: (r) => r.from },
              {
                id: "to",
                header: t("site.rules.redirects.to"),
                cell: (r) => (
                  <span dir="ltr" className="block max-w-80 truncate font-mono text-xs">
                    {r.to}
                  </span>
                ),
                searchValue: (r) => r.to,
              },
              { id: "code", header: t("site.rules.redirects.code"), cell: (r) => <Badge variant="outline">{r.code ?? 302}</Badge> },
            ]}
            rowActions={
              manage
                ? (r) =>
                    rowActions(
                      () => setEditing({ kind: "redirect", index: r.i }),
                      () => quick({ redirects: current.redirects.filter((_, idx) => idx !== r.i) }),
                    )
                : undefined
            }
            onRowClick={manage ? (r) => setEditing({ kind: "redirect", index: r.i }) : undefined}
          />
        )}
      </section>

      {rules !== null ? (
        <section aria-labelledby="rules-headers-h">
          <SectionTitle
            id="rules-headers-h"
            title={t("site.rules.headers.title")}
            hint={t("site.rules.headers.hint")}
            action={addButton(t("site.rules.headers.add"), { kind: "header", index: null })}
          />
          {current.headers.length === 0 ? (
            empty(t("site.rules.headers.empty"))
          ) : (
            <NasaqTable
              data={headers}
              rowId={(h) => String(h.i)}
              rowLabel={(h) => h.name}
              label={t("site.rules.headers.title")}
              columns={[
                { id: "action", header: t("site.rules.headers.action"), cell: (h) => <Badge variant="outline">{t(`site.rules.headers.actions.${h.action}`)}</Badge> },
                { id: "name", header: t("site.rules.headers.name"), cell: (h) => <span dir="ltr" className="font-mono text-xs">{h.name}</span>, searchValue: (h) => h.name },
                {
                  id: "value",
                  header: t("site.rules.headers.value"),
                  cell: (h) => (
                    <span dir="ltr" className="block max-w-80 truncate font-mono text-xs">
                      {h.action !== "remove" ? h.value : "—"}
                    </span>
                  ),
                },
              ]}
              rowActions={
                manage
                  ? (h) =>
                      rowActions(
                        () => setEditing({ kind: "header", index: h.i }),
                        () => quick({ headers: current.headers.filter((_, idx) => idx !== h.i) }),
                      )
                  : undefined
              }
              onRowClick={manage ? (h) => setEditing({ kind: "header", index: h.i }) : undefined}
            />
          )}
        </section>
      ) : null}

      {rules !== null ? (
        <section aria-labelledby="rules-access-h">
          <SectionTitle id="rules-access-h" title={t("site.rules.access.title")} hint={t("site.rules.access.hint")} />

          <SettingsRow label={t("site.rules.access.basicAuth")} htmlFor="rules-auth-enable" hint={t("site.rules.access.basicAuthHint")}>
            <div className="flex flex-wrap items-center gap-3">
              <Checkbox
                id="rules-auth-enable"
                checked={!!current.basic_auth?.enabled}
                disabled={!manage || busy}
                onCheckedChange={(v) => quick({ basic_auth: auth({ enabled: !!v }) })}
              />
              <Badge variant={statusVariant(current.status)}>{t(`site.rules.statuses.${current.status}`, undefined, current.status)}</Badge>
              {current.basic_auth?.path_prefix ? (
                <span dir="ltr" className="font-mono text-xs text-muted-foreground">
                  {current.basic_auth.path_prefix}
                </span>
              ) : null}
              {manage ? (
                <span className="ms-auto flex gap-2">
                  <Button size="sm" variant="ghost" onClick={() => setEditing({ kind: "auth" })}>
                    <SettingsIcon />
                    {t("site.rules.access.authSettings")}
                  </Button>
                  <Button size="sm" variant="secondary" onClick={() => setEditing({ kind: "user", username: null })}>
                    <PlusIcon />
                    {t("site.rules.access.addUser")}
                  </Button>
                </span>
              ) : null}
            </div>
            {current.error ? <p className="text-xs text-nq-danger-text">{current.error}</p> : null}
            {users.length ? (
              <ul className="flex flex-col divide-y divide-border rounded-card border border-border">
                {users.map((u) => {
                  const actions = manage
                    ? [
                        { id: "password", label: t("site.rules.access.changePassword"), icon: KeyRoundIcon, disabled: busy, onSelect: () => setEditing({ kind: "user", username: u.username }) },
                        {
                          id: "remove",
                          label: t("site.rules.remove"),
                          icon: Trash2Icon,
                          danger: true,
                          group: "danger",
                          disabled: busy,
                          onSelect: () => quick({ basic_auth: auth({ users: users.filter((x) => x.username !== u.username) }) }),
                        },
                      ]
                    : []
                  return (
                    <RowWithActions key={u.username} actions={actions} render={<li className="flex items-center gap-2 px-3 py-2 text-sm" />}>
                      <KeyRoundIcon className="size-4 text-muted-foreground" aria-hidden />
                      <span dir="ltr" className="min-w-0 flex-1 truncate font-mono text-xs">
                        {u.username}
                      </span>
                      <RowMenu actions={actions} label={u.username} />
                    </RowWithActions>
                  )
                })}
              </ul>
            ) : current.basic_auth?.enabled ? (
              <p className="text-xs text-muted-foreground">{t("site.rules.access.noUsers")}</p>
            ) : null}
          </SettingsRow>

          {(["ip_allow", "ip_deny"] as const).map((list) => {
            const items = current.access?.[list] ?? []
            const key = list === "ip_allow" ? "ipAllow" : "ipDeny"
            return (
              <SettingsRow key={list} label={t(`site.rules.access.${key}`)} hint={t(`site.rules.access.${key}Hint`)}>
                <div className="flex flex-wrap items-center gap-2">
                  {items.map((c) => (
                    <Badge key={c} variant="outline" className="gap-1">
                      <span dir="ltr">{c}</span>
                      {manage ? (
                        <button
                          type="button"
                          aria-label={t("site.rules.remove")}
                          onClick={() => quick({ access: access({ [list]: items.filter((x) => x !== c) }) })}
                          disabled={busy}
                        >
                          <XIcon className="size-3" />
                        </button>
                      ) : null}
                    </Badge>
                  ))}
                  {!items.length ? <span className="text-xs text-muted-foreground">{t("site.rules.access.noAddresses")}</span> : null}
                  {manage ? (
                    <Button size="sm" variant="ghost" onClick={() => setEditing({ kind: "cidr", list })}>
                      <PlusIcon />
                      {t("site.rules.access.addAddresses")}
                    </Button>
                  ) : null}
                </div>
              </SettingsRow>
            )
          })}

          <SettingsRow label={t("site.rules.access.blockBadBots")} htmlFor="rules-bad-bots" hint={t("site.rules.access.blockBadBotsHint")}>
            <Checkbox
              id="rules-bad-bots"
              checked={!!current.access?.block_bad_bots}
              disabled={!manage || busy}
              onCheckedChange={(v) => quick({ access: access({ block_bad_bots: !!v }) })}
            />
          </SettingsRow>

          <p className="px-1 pt-3 text-xs text-muted-foreground">{t("site.rules.access.rateLimitNote")}</p>
        </section>
      ) : null}

      <ErrorBand message={error} />

      {editing?.kind === "redirect" ? (
        <RedirectDialog
          rule={editing.index === null ? null : current.redirects[editing.index]}
          onClose={() => setEditing(null)}
          onSave={(r) =>
            put({ redirects: editing.index === null ? [...current.redirects, r] : current.redirects.map((x, i) => (i === editing.index ? r : x)) })
          }
        />
      ) : null}
      {editing?.kind === "header" ? (
        <HeaderDialog
          rule={editing.index === null ? null : current.headers[editing.index]}
          onClose={() => setEditing(null)}
          onSave={(h) => put({ headers: editing.index === null ? [...current.headers, h] : current.headers.map((x, i) => (i === editing.index ? h : x)) })}
        />
      ) : null}
      {editing?.kind === "user" ? (
        <UserDialog
          username={editing.username}
          onClose={() => setEditing(null)}
          onSave={(u) =>
            put({
              basic_auth: auth({
                enabled: current.basic_auth ? current.basic_auth.enabled : true,
                users: [...users.filter((x) => x.username !== u.username), u],
              }),
            })
          }
        />
      ) : null}
      {editing?.kind === "auth" ? (
        <AuthDialog prefix={current.basic_auth?.path_prefix ?? ""} onClose={() => setEditing(null)} onSave={(p) => put({ basic_auth: auth({ path_prefix: p }) })} />
      ) : null}
      {editing?.kind === "cidr" ? (
        <CidrDialog
          title={t(`site.rules.access.${editing.list === "ip_allow" ? "ipAllow" : "ipDeny"}`)}
          onClose={() => setEditing(null)}
          onSave={(add) => {
            const have = current.access?.[editing.list] ?? []
            return put({ access: access({ [editing.list]: [...have, ...add.filter((c) => !have.includes(c))] }) })
          }}
        />
      ) : null}
    </div>
  )
}

/** The dialog shell every rule form shares: title, hint, fields, error, cancel and save. */
function RuleDialog({
  title,
  description,
  canSave,
  onClose,
  onSubmit,
  children,
}: {
  title: string
  description?: string
  canSave: boolean
  onClose: () => void
  onSubmit: () => Promise<void>
  children: ReactNode
}) {
  const { t } = useI18n()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await onSubmit()
      onClose()
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="max-h-[90dvh] overflow-y-auto">
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{title}</DialogTitle>
            {description ? <DialogDescription>{description}</DialogDescription> : null}
          </DialogHeader>
          <div className="flex w-full flex-col gap-5">{children}</div>
          <FormError message={error} />
          <DialogFooter>
            <Button type="button" variant="secondary" onClick={onClose}>
              {t("common.cancel")}
            </Button>
            <Button variant="primary" type="submit" disabled={busy || !canSave}>
              {busy ? t("common.working") : t("common.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function RedirectDialog({ rule, onClose, onSave }: { rule: RedirectRule | null; onClose: () => void; onSave: (r: RedirectRule) => Promise<void> }) {
  const { t } = useI18n()
  const [from, setFrom] = useState(rule?.from ?? "")
  const [to, setTo] = useState(rule?.to ?? "")
  const [code, setCode] = useState(String(rule?.code ?? 302))
  const [preserve, setPreserve] = useState(!!rule?.preserve_query)
  return (
    <RuleDialog
      title={rule ? t("site.rules.redirects.edit") : t("site.rules.redirects.add")}
      description={t("site.rules.redirects.hint")}
      canSave={!!from.trim() && !!to.trim()}
      onClose={onClose}
      onSubmit={() => onSave({ from: from.trim(), to: to.trim(), code: Number(code), preserve_query: preserve })}
    >
      <Field>
        <FieldLabel htmlFor="rd-from">{t("site.rules.redirects.from")}</FieldLabel>
        <Input id="rd-from" dir="ltr" required value={from} placeholder={t("site.rules.redirects.fromPlaceholder")} onChange={(e) => setFrom(e.target.value)} />
      </Field>
      <Field>
        <FieldLabel htmlFor="rd-to">{t("site.rules.redirects.to")}</FieldLabel>
        <Input id="rd-to" dir="ltr" required value={to} placeholder={t("site.rules.redirects.toPlaceholder")} onChange={(e) => setTo(e.target.value)} />
      </Field>
      <Field>
        <FieldLabel>{t("site.rules.redirects.code")}</FieldLabel>
        <Select value={code} onValueChange={(v) => v && setCode(v)}>
          <SelectTrigger aria-label={t("site.rules.redirects.code")}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {REDIRECT_CODES.map((c) => (
              <SelectItem key={c} value={String(c)}>
                {c}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>
      <label className="flex items-center gap-2 text-sm">
        <Checkbox checked={preserve} onCheckedChange={(v) => setPreserve(v === true)} />
        {t("site.rules.redirects.preserveQuery")}
      </label>
    </RuleDialog>
  )
}

function HeaderDialog({ rule, onClose, onSave }: { rule: HeaderRule | null; onClose: () => void; onSave: (h: HeaderRule) => Promise<void> }) {
  const { t } = useI18n()
  const [action, setAction] = useState<HeaderAction>(rule?.action ?? "set")
  const [name, setName] = useState(rule?.name ?? "")
  const [value, setValue] = useState(rule?.value ?? "")
  return (
    <RuleDialog
      title={rule ? t("site.rules.headers.edit") : t("site.rules.headers.add")}
      description={t("site.rules.headers.hint")}
      canSave={!!name.trim()}
      onClose={onClose}
      onSubmit={() => onSave({ action, name: name.trim(), value: action === "remove" ? "" : value.trim() })}
    >
      {rule ? null : (
        <Field>
          <FieldLabel>{t("site.rules.headers.presetsLabel")}</FieldLabel>
          <div className="flex flex-wrap gap-2">
            {HEADER_PRESETS.map((p) => (
              <Button
                key={p.key}
                type="button"
                variant="secondary"
                size="sm"
                onClick={() => {
                  setAction("set")
                  setName(p.name)
                  setValue(p.value)
                }}
              >
                {t(`site.rules.headers.presets.${p.key}`)}
              </Button>
            ))}
          </div>
        </Field>
      )}
      <Field>
        <FieldLabel>{t("site.rules.headers.action")}</FieldLabel>
        <Select value={action} onValueChange={(v) => v && setAction(v as HeaderAction)}>
          <SelectTrigger aria-label={t("site.rules.headers.action")}>
            <SelectValue>{(v: string) => t(`site.rules.headers.actions.${v}`)}</SelectValue>
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="set">{t("site.rules.headers.actions.set")}</SelectItem>
            <SelectItem value="add">{t("site.rules.headers.actions.add")}</SelectItem>
            <SelectItem value="remove">{t("site.rules.headers.actions.remove")}</SelectItem>
          </SelectContent>
        </Select>
      </Field>
      <Field>
        <FieldLabel htmlFor="hd-name">{t("site.rules.headers.name")}</FieldLabel>
        <Input id="hd-name" dir="ltr" required value={name} placeholder={t("site.rules.headers.namePlaceholder")} onChange={(e) => setName(e.target.value)} />
      </Field>
      {action !== "remove" ? (
        <Field>
          <FieldLabel htmlFor="hd-value">{t("site.rules.headers.value")}</FieldLabel>
          <Input id="hd-value" dir="ltr" value={value} placeholder={t("site.rules.headers.valuePlaceholder")} onChange={(e) => setValue(e.target.value)} />
        </Field>
      ) : null}
    </RuleDialog>
  )
}

function UserDialog({ username, onClose, onSave }: { username: string | null; onClose: () => void; onSave: (u: BasicAuthUser) => Promise<void> }) {
  const { t } = useI18n()
  const [name, setName] = useState(username ?? "")
  const [password, setPassword] = useState("")
  return (
    <RuleDialog
      title={username ? t("site.rules.access.changePassword") : t("site.rules.access.addUser")}
      description={t("site.rules.access.basicAuthHint")}
      canSave={!!name.trim() && password.length >= 8}
      onClose={onClose}
      onSubmit={() => onSave({ username: name.trim(), password })}
    >
      <Field>
        <FieldLabel htmlFor="ba-user">{t("site.rules.access.usernamePlaceholder")}</FieldLabel>
        <Input id="ba-user" dir="ltr" required maxLength={128} autoComplete="off" disabled={!!username} value={name} onChange={(e) => setName(e.target.value)} />
      </Field>
      <Field>
        <FieldLabel htmlFor="ba-pass">{t("site.rules.access.passwordPlaceholder")}</FieldLabel>
        <Input id="ba-pass" dir="ltr" type="password" required minLength={8} maxLength={72} autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} />
        <FieldDescription>{t("site.rules.access.passwordHint")}</FieldDescription>
      </Field>
    </RuleDialog>
  )
}

function AuthDialog({ prefix, onClose, onSave }: { prefix: string; onClose: () => void; onSave: (prefix: string) => Promise<void> }) {
  const { t } = useI18n()
  const [value, setValue] = useState(prefix)
  return (
    <RuleDialog title={t("site.rules.access.basicAuth")} description={t("site.rules.access.basicAuthHint")} canSave onClose={onClose} onSubmit={() => onSave(value.trim())}>
      <Field>
        <FieldLabel htmlFor="ba-prefix">{t("site.rules.access.pathPrefixPlaceholder")}</FieldLabel>
        <Input id="ba-prefix" dir="ltr" placeholder="/admin" value={value} onChange={(e) => setValue(e.target.value)} />
      </Field>
    </RuleDialog>
  )
}

function CidrDialog({ title, onClose, onSave }: { title: string; onClose: () => void; onSave: (cidrs: string[]) => Promise<void> }) {
  const { t } = useI18n()
  const [text, setText] = useState("")
  const cidrs = text
    .split(/[\s,]+/)
    .map((s) => s.trim())
    .filter(Boolean)
  return (
    <RuleDialog title={title} description={t("site.rules.access.cidrsHint")} canSave={cidrs.length > 0} onClose={onClose} onSubmit={() => onSave(cidrs)}>
      <Field>
        <FieldLabel htmlFor="ip-list">{t("site.rules.access.addresses")}</FieldLabel>
        <Textarea id="ip-list" dir="ltr" rows={4} className="font-mono text-xs" placeholder={t("site.rules.access.cidrPlaceholder")} value={text} onChange={(e) => setText(e.target.value)} />
      </Field>
    </RuleDialog>
  )
}
