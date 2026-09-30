"use client"

import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Badge, Button, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@fadymondy/nasaq/web"
import Link from "next/link"
import { useEffect, useState } from "react"
import { NetworkIcon, PlusIcon, RefreshCwIcon, Trash2Icon } from "lucide-react"

import { ConfirmButton } from "@/components/confirm-button"
import { ListSearch, matches } from "@/components/server-tabs/list-tools"




import { useErrorText } from "@/components/form-error"
import { ErrorBand, SectionTitle, SettingsRow, TableBand } from "@/components/states"


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
  const { t, locale } = useI18n()
  const { team } = useTeam()
  const [q, setQ] = useState("")
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

  async function open(credential = choice) {
    setBusy(true)
    setError(null)
    try {
      await apiFetch(`/api/teams/${team.slug}/servers/${serverId}/tunnels`, {
        method: "POST",
        body: { credential },
      })
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
      await apiFetch(`/api/teams/${team.slug}/servers/${serverId}/tunnels/${kind}`, {
        method: "DELETE",
      })
      load()
    } catch (err) {
      setError(errorText(err))
    }
  }

  const shown = rows.filter((r) => matches(q, r.kind, r.status, r.address, r.remote_port))
  return (
    <section aria-labelledby="tunnels-h" data-testid="server-tunnels">
      <SectionTitle id="tunnels-h" title={t("tunnels.title")} hint={t("tunnels.hint")} />
      <ErrorBand message={error} />
      {rows.length === 0 ? (
        <Empty className="border-b">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <NetworkIcon />
            </EmptyMedia>
            <EmptyTitle>{t("lists.noTunnels")}</EmptyTitle>
            {manage && creds.length === 0 ? <EmptyDescription>{t("lists.noTunnelCreds")}</EmptyDescription> : null}
          </EmptyHeader>
          {manage && creds.length === 0 ? (
            <Button variant="secondary" nativeButton={false} render={<Link href={`/${locale}/t/${team.slug}/credentials`} />}>
              <PlusIcon />
              {t("lists.credentials")}
            </Button>
          ) : null}
        </Empty>
      ) : (
        <>
          <ListSearch value={q} onChange={setQ} placeholder={t("lists.searchTunnels")} shown={shown.length} total={rows.length} />
          <TableBand>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("lists.kind")}</TableHead>
                  <TableHead>{t("lists.status")}</TableHead>
                  <TableHead>{t("lists.address")}</TableHead>
                  {manage ? <TableHead className="text-end">{t("common.actions")}</TableHead> : null}
                </TableRow>
              </TableHeader>
              <TableBody>
                {shown.map((r) => (
                  <TableRow key={r.id}>
                    <TableCell className="font-medium">
                      <span className="inline-flex items-center gap-2">
                        <NetworkIcon className="size-4 text-nq-brand" />
                        {t(`tunnels.kinds.${r.kind}`)}
                      </span>
                    </TableCell>
                    <TableCell>
                      <Badge variant={r.status === "active" ? "brand" : r.status === "failed" ? "danger" : "neutral"}>
                        {t(`tunnels.status.${r.status}`, undefined, r.status)}
                      </Badge>
                      {r.error ? (
                        <p dir="auto" className="mt-1 max-w-md text-xs text-nq-danger-text">
                          {r.error}
                        </p>
                      ) : null}
                    </TableCell>
                    <TableCell dir="ltr" className="text-start font-mono text-xs">
                      {r.address ?? "—"}
                    </TableCell>
                    {manage ? (
                      <TableCell>
                        <div className="flex flex-wrap justify-end gap-2">
                          {creds.some((c) => c.provider === r.kind) ? (
                            <Button
                              size="sm"
                              variant="secondary"
                              disabled={busy}
                              onClick={() => {
                                const c = creds.find((x) => x.provider === r.kind)
                                if (c) void open(c.id)
                              }}
                            >
                              <RefreshCwIcon />
                              {t("lists.reconnect")}
                            </Button>
                          ) : null}
                          <ConfirmButton
                            icon={<Trash2Icon />}
                            label={t("tunnels.close")}
                            title={t("tunnels.close")}
                            description={r.address ?? t(`tunnels.kinds.${r.kind}`)}
                            confirmLabel={t("tunnels.close")}
                            onConfirm={() => close(r.kind)}
                          />
                        </div>
                      </TableCell>
                    ) : null}
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </TableBand>
        </>
      )}
      {manage && creds.length > 0 ? (
        <SettingsRow label={t("tunnels.credential")}>
          <div className="flex flex-wrap items-center gap-2">
            <Select value={choice} onValueChange={(v) => v && setChoice(v as string)}>
              <SelectTrigger className="w-full sm:w-64" aria-label={t("tunnels.credential")}>
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
            <Button variant="primary" disabled={busy || !choice} onClick={() => void open()}>
              <PlusIcon />
              {busy ? t("common.working") : t("tunnels.open")}
            </Button>
          </div>
        </SettingsRow>
      ) : null}
    </section>
  )
}
