"use client"

import { useCallback, useEffect, useState, type FormEvent } from "react"
import { CheckCircle2Icon, ShieldCheckIcon } from "lucide-react"

import { ConfirmButton } from "@/components/confirm-button"
import { CopyField } from "@/components/copy-field"
import { FormError, useErrorText } from "@/components/form-error"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { apiFetch } from "@/lib/api"
import type { Domain, Integrations, TLSMode } from "@/lib/apps"
import { useFormatDate, useI18n } from "@/lib/i18n"

const TLS: TLSMode[] = ["auto", "internal", "off"]

export function DomainsPanel({ team, path, manage }: { team: string; path: string; manage: boolean }) {
  const { t } = useI18n()
  const fmt = useFormatDate()
  const errorText = useErrorText()
  const [domains, setDomains] = useState<Domain[] | null>(null)
  const [appsDomain, setAppsDomain] = useState<boolean | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [hostname, setHostname] = useState("")
  const [tls, setTls] = useState<TLSMode>("auto")
  const [busy, setBusy] = useState(false)
  const [messages, setMessages] = useState<Record<string, string>>({})

  const load = useCallback(async () => {
    try {
      const r = await apiFetch<{ domains: Domain[] }>(`${path}/domains`)
      setDomains(r.domains ?? [])
    } catch (err) {
      setError(errorText(err))
      setDomains((d) => d ?? [])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path])

  useEffect(() => {
    void load()
    apiFetch<Integrations>(`/api/teams/${team}/integrations`)
      .then((r) => setAppsDomain(r.apps_domain.configured))
      .catch(() => setAppsDomain(null))
  }, [load, team])

  const hostOK = /^(?=.{3,253}$)([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,}$/i.test(hostname.trim())

  async function add(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await apiFetch(`${path}/domains`, { method: "POST", body: { hostname: hostname.trim().toLowerCase(), tls } })
      setHostname("")
      await load()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  async function verify(d: Domain) {
    setError(null)
    try {
      const r = await apiFetch<{ verified: boolean; message: string; domain: Domain }>(
        `${path}/domains/${d.id}/verify`,
        { method: "POST" },
      )
      setMessages((m) => ({ ...m, [d.id]: r.message }))
      setDomains((list) => list?.map((x) => (x.id === d.id ? r.domain : x)) ?? list)
    } catch (err) {
      setError(errorText(err))
    }
  }

  return (
    <div className="flex flex-col gap-4" data-testid="domains">
      {appsDomain === false ? <p className="text-sm text-muted-foreground">{t("apps.domains.noAppsDomain")}</p> : null}
      <FormError message={error} />
      {domains === null ? (
        <Skeleton className="h-16 w-full" />
      ) : domains.length === 0 ? (
        <p className="border p-4 text-sm text-muted-foreground">{t("apps.domains.empty")}</p>
      ) : (
        <ul className="flex flex-col divide-y border">
          {domains.map((d) => (
            <li key={d.id} className="flex flex-col gap-3 p-4" data-domain={d.hostname}>
              <div className="flex flex-wrap items-center gap-2">
                <a
                  dir="ltr"
                  href={`${d.tls === "off" ? "http" : "https"}://${d.hostname}`}
                  target="_blank"
                  rel="noreferrer"
                  className="font-mono text-sm font-medium underline-offset-4 hover:underline"
                >
                  {d.hostname}
                </a>
                <Badge variant="outline">{t(`apps.domains.tls.${d.tls}`)}</Badge>
                {d.verified ? (
                  <Badge>
                    <CheckCircle2Icon />
                    {t("apps.domains.verified")}
                  </Badge>
                ) : (
                  <Badge variant="secondary">{t("apps.domains.unverified")}</Badge>
                )}
                {manage ? (
                  <span className="ms-auto flex gap-2">
                    {!d.verified ? (
                      <Button size="sm" variant="outline" onClick={() => verify(d)}>
                        <ShieldCheckIcon />
                        {t("apps.domains.verify")}
                      </Button>
                    ) : null}
                    <ConfirmButton
                      label={t("apps.domains.remove")}
                      title={t("apps.domains.removeTitle")}
                      description={t("apps.domains.removeHint", { host: d.hostname })}
                      confirmLabel={t("apps.domains.remove")}
                      onConfirm={async () => {
                        try {
                          await apiFetch(`${path}/domains/${d.id}`, { method: "DELETE" })
                        } catch (err) {
                          throw new Error(errorText(err))
                        }
                        await load()
                      }}
                    />
                  </span>
                ) : null}
              </div>
              {d.verified ? (
                <p className="text-xs text-muted-foreground">
                  {t("apps.domains.verifiedAt")} <span dir="ltr">{fmt(d.verified_at)}</span>
                </p>
              ) : (
                <div className="flex flex-col gap-2 text-sm">
                  <p className="text-muted-foreground">{t("apps.domains.txtHint")}</p>
                  <div className="grid gap-2 sm:grid-cols-[auto_1fr] sm:items-center">
                    <span className="text-xs text-muted-foreground">{t("apps.domains.txtName")}</span>
                    <CopyField value={d.verify_name} />
                    <span className="text-xs text-muted-foreground">{t("apps.domains.txtValue")}</span>
                    <CopyField value={d.verify_value} />
                  </div>
                </div>
              )}
              {messages[d.id] ? (
                <p dir="auto" role="status" className="text-xs text-muted-foreground">
                  {messages[d.id]}
                </p>
              ) : null}
            </li>
          ))}
        </ul>
      )}

      {manage ? (
        <form onSubmit={add} className="flex flex-col gap-4 border p-4">
          <h3 className="font-medium">{t("apps.domains.addTitle")}</h3>
          <FieldGroup>
            <div className="grid gap-4 sm:grid-cols-[minmax(0,2fr)_minmax(0,1fr)]">
              <Field>
                <FieldLabel htmlFor="dom-host">{t("apps.domains.hostname")}</FieldLabel>
                <Input
                  id="dom-host"
                  dir="ltr"
                  required
                  maxLength={253}
                  placeholder="app.example.com"
                  value={hostname}
                  onChange={(e) => setHostname(e.target.value)}
                  aria-invalid={hostname !== "" && !hostOK}
                  autoComplete="off"
                />
              </Field>
              <Field>
                <FieldLabel htmlFor="dom-tls">{t("apps.domains.tlsLabel")}</FieldLabel>
                <Select value={tls} onValueChange={(v) => v && setTls(v as TLSMode)}>
                  <SelectTrigger id="dom-tls" className="w-full">
                    <SelectValue>{(v: string) => t(`apps.domains.tls.${v}`)}</SelectValue>
                  </SelectTrigger>
                  <SelectContent>
                    {TLS.map((m) => (
                      <SelectItem key={m} value={m}>
                        {t(`apps.domains.tls.${m}`)}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
            </div>
            <ul className="flex flex-col gap-1 text-xs text-muted-foreground">
              {TLS.map((m) => (
                <li key={m}>
                  <span className="font-medium text-foreground">{t(`apps.domains.tls.${m}`)}</span> —{" "}
                  {t(`apps.domains.tlsHint.${m}`)}
                </li>
              ))}
            </ul>
            <div>
              <Button type="submit" disabled={busy || !hostOK}>
                {busy ? t("common.working") : t("apps.domains.add")}
              </Button>
            </div>
          </FieldGroup>
        </form>
      ) : (
        <p className="text-sm text-muted-foreground">{t("common.readOnly")}</p>
      )}
    </div>
  )
}
