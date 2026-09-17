"use client"

import { useState } from "react"
import { PlayIcon } from "lucide-react"

import { FormError, useErrorText } from "@/components/form-error"
import { OperationLog } from "@/components/op-log"
import { Button } from "@/components/ui/button"
import { Field, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { apiFetch } from "@/lib/api"
import type { App } from "@/lib/apps"
import { useI18n } from "@/lib/i18n"
import type { Operation } from "@/lib/servers"

/** Runtime logs are relayed on demand as a bounded operation (docker logs --follow). */
export function RuntimeLogsPanel({
  path,
  team,
  app,
  processes,
  operate,
  live,
}: {
  path: string
  team: string
  app: App
  processes: { name: string }[]
  operate: boolean
  live: boolean
}) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const names = processes.length ? processes.map((p) => p.name) : ["web"]
  const [process, setProcess] = useState(names[0])
  const [tail, setTail] = useState("200")
  const [follow, setFollow] = useState("60")
  const [op, setOp] = useState<Operation | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function run() {
    setBusy(true)
    setError(null)
    try {
      setOp(
        await apiFetch<Operation>(`${path}/logs`, {
          method: "POST",
          body: { process, tail: Number(tail) || 200, follow_seconds: Number(follow) || 60 },
        }),
      )
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  if (!operate) return <p className="text-sm text-muted-foreground">{t("apps.logs.readOnly")}</p>

  return (
    <div className="flex flex-col gap-4" data-testid="runtime-logs">
      <p className="text-sm text-muted-foreground">{t("apps.logs.hint")}</p>
      {!live ? <p className="text-sm text-muted-foreground">{t("apps.logs.notLive")}</p> : null}
      <div className="flex flex-wrap items-end gap-3">
        <Field className="w-44">
          <FieldLabel htmlFor="log-proc">{t("apps.logs.process")}</FieldLabel>
          <Select value={process} onValueChange={(v) => v && setProcess(v as string)}>
            <SelectTrigger id="log-proc" className="w-full">
              <SelectValue>{(v: string) => <span dir="ltr">{v}</span>}</SelectValue>
            </SelectTrigger>
            <SelectContent>
              {names.map((n) => (
                <SelectItem key={n} value={n}>
                  <span dir="ltr">{n}</span>
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        <Field className="w-28">
          <FieldLabel htmlFor="log-tail">{t("apps.logs.tail")}</FieldLabel>
          <Input id="log-tail" dir="ltr" type="number" min={0} max={1000} value={tail} onChange={(e) => setTail(e.target.value)} />
        </Field>
        <Field className="w-28">
          <FieldLabel htmlFor="log-follow">{t("apps.logs.follow")}</FieldLabel>
          <Input id="log-follow" dir="ltr" type="number" min={0} max={300} value={follow} onChange={(e) => setFollow(e.target.value)} />
        </Field>
        <Button disabled={busy} onClick={run}>
          <PlayIcon />
          {busy ? t("common.working") : t("apps.logs.fetch")}
        </Button>
      </div>
      <FormError message={error} />
      <div className="border">
        {op ? (
          <OperationLog team={team} serverId={app.server_id} op={op} />
        ) : (
          <p className="p-4 text-sm text-muted-foreground">{t("apps.logs.empty")}</p>
        )}
      </div>
    </div>
  )
}
