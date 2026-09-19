"use client"

import { useEffect, useState } from "react"

import { FormError, useErrorText } from "@/components/form-error"
import { Button } from "@/components/ui/button"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { apiFetch } from "@/lib/api"
import { useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

type TunnelRow = {
  id: string
  server_id: string
  kind: "frp" | "tailscale"
  remote_port: number | null
  status: string
  address: string | null
  error: string | null
}

type Cred = { id: string; name: string; provider: string }

/** A server's tunnels (frp / tailscale): its way in when it has no public address. */
export function ServerTunnels({ serverId, manage }: { serverId: string; manage: boolean }) {
  const { t } = useI18n()
  const { team } = useTeam()
  const errorText = useErrorText()
  const [rows, setRows] = useState<TunnelRow[]>([])
  const [creds, setCreds] = useState<Cred[]>([])
  const [choice, setChoice] = useState("")
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const load = () => {
    apiFetch<{ tunnels: TunnelRow[] }>(`/api/teams/${team.slug}/tunnels`)
      .then((r) => setRows((r.tunnels ?? []).filter((x) => x.server_id === serverId)))
      .catch(() => setRows([]))
  }

  useEffect(() => {
    load()
    if (manage) {
      apiFetch<{ credentials: Cred[] }>(`/api/teams/${team.slug}/credentials`)
        .then((r) => {
          const usable = (r.credentials ?? []).filter((c) => c.provider === "frp" || c.provider === "tailscale")
          setCreds(usable)
          if (usable[0]) setChoice(usable[0].id)
        })
        .catch(() => setCreds([]))
    }
    const id = setInterval(load, 5000)
    return () => clearInterval(id)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [team.slug, serverId, manage])

  async function open() {
    setBusy(true)
    setError(null)
    try {
      await apiFetch(`/api/teams/${team.slug}/servers/${serverId}/tunnels`, { method: "POST", body: { credential: choice } })
      load()
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  async function close(kind: string) {
    setError(null)
    try {
      await apiFetch(`/api/teams/${team.slug}/servers/${serverId}/tunnels/${kind}`, { method: "DELETE" })
      load()
    } catch (err) {
      setError(errorText(err))
    }
  }

  if (rows.length === 0 && (!manage || creds.length === 0)) return null
  return (
    <section aria-labelledby="tunnels-h" className="flex flex-col gap-3" data-testid="server-tunnels">
      <div className="flex flex-col gap-1">
        <h2 id="tunnels-h" className="font-medium">
          {t("tunnels.title")}
        </h2>
        <p className="text-sm text-muted-foreground">{t("tunnels.hint")}</p>
      </div>
      <FormError message={error} />
      {rows.length > 0 ? (
        <ul className="flex flex-col divide-y border text-sm">
          {rows.map((r) => (
            <li key={r.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-4 py-2">
              <span className="font-medium">{t(`tunnels.kinds.${r.kind}`)}</span>
              <span className="text-muted-foreground">{t(`tunnels.status.${r.status}`, undefined, r.status)}</span>
              {r.address ? (
                <span dir="ltr" className="font-mono text-xs">
                  {r.address}
                </span>
              ) : null}
              {manage ? (
                <Button size="sm" variant="outline" className="ms-auto" onClick={() => close(r.kind)}>
                  {t("tunnels.close")}
                </Button>
              ) : null}
              {r.error ? (
                <span dir="auto" className="w-full text-xs text-destructive">
                  {r.error}
                </span>
              ) : null}
            </li>
          ))}
        </ul>
      ) : null}
      {manage && creds.length > 0 ? (
        <div className="flex flex-wrap items-center gap-2">
          <Select value={choice} onValueChange={(v) => v && setChoice(v as string)}>
            <SelectTrigger className="w-64" aria-label={t("tunnels.credential")}>
              <SelectValue>{(v: string) => creds.find((c) => c.id === v)?.name ?? ""}</SelectValue>
            </SelectTrigger>
            <SelectContent>
              {creds.map((c) => (
                <SelectItem key={c.id} value={c.id}>
                  {c.name} ({t(`tunnels.kinds.${c.provider}`)})
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Button size="sm" disabled={busy || !choice} onClick={open}>
            {busy ? t("common.working") : t("tunnels.open")}
          </Button>
        </div>
      ) : null}
    </section>
  )
}
