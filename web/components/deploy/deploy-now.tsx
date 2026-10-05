"use client"

import { Button, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, Input } from "@fadymondy/nasaq/web"
import { useEffect, useState } from "react"
import { CheckIcon, RocketIcon, SearchIcon } from "lucide-react"

import { FormError, useErrorText } from "@/components/form-error"
import type { LogTarget } from "@/components/home/log-dialog"
import { ServerMark } from "@/components/server-mark"
import { apiFetch } from "@/lib/api"
import { appPath, type App, type Deployment } from "@/lib/apps"
import { useI18n } from "@/lib/i18n"
import { cn } from "@/lib/utils"

/** "Deploy now": pick one of the team's apps and start a deployment of it; `onStarted` gets the new run. */
export function DeployNowButton({ team, onStarted }: { team: string; onStarted: (target: LogTarget) => void }) {
  const { t } = useI18n()
  const errorText = useErrorText()
  const [open, setOpen] = useState(false)
  const [apps, setApps] = useState<App[] | null>(null)
  const [q, setQ] = useState("")
  const [pick, setPick] = useState<App | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (!open || apps) return
    apiFetch<{ apps: App[] }>(`/api/teams/${team}/apps`)
      .then((r) => setApps(r.apps))
      .catch((err) => {
        setError(errorText(err))
        setApps([])
      })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, apps, team])

  function change(next: boolean) {
    setOpen(next)
    if (!next) {
      setPick(null)
      setQ("")
      setError(null)
    }
  }

  async function start(app: App | null) {
    if (!app) return
    setBusy(true)
    setError(null)
    try {
      const d = await apiFetch<Deployment>(`${appPath(team, app.name)}/deployments`, { method: "POST", body: {} })
      onStarted({ app: app.name, appTitle: app.title || app.name, id: d.id, number: d.number })
      change(false)
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  const needle = q.trim().toLowerCase()
  const shown = (apps ?? []).filter((a) => !needle || [a.name, a.title, a.server_name].some((v) => v?.toLowerCase().includes(needle)))

  return (
    <>
      <Button variant="primary" onClick={() => change(true)}>
        <RocketIcon />
        {t("deployNow.button")}
      </Button>
      <Dialog open={open} onOpenChange={change}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("deployNow.title")}</DialogTitle>
            <DialogDescription>{t("deployNow.hint")}</DialogDescription>
          </DialogHeader>
          <div className="relative">
            <SearchIcon
              aria-hidden
              className="pointer-events-none absolute start-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground"
            />
            <Input
              value={q}
              onChange={(e) => setQ(e.currentTarget.value)}
              placeholder={t("deployNow.search")}
              aria-label={t("deployNow.search")}
              className="ps-8"
            />
          </div>
          <div role="listbox" aria-label={t("nav.apps")} className="flex max-h-80 flex-col gap-1 overflow-y-auto">
            {apps === null ? (
              <p className="px-2 py-6 text-center text-body-sm text-muted-foreground">{t("common.loading")}</p>
            ) : shown.length === 0 ? (
              <p className="px-2 py-6 text-center text-body-sm text-muted-foreground">{t("deployNow.empty")}</p>
            ) : (
              shown.map((a) => {
                const on = pick?.id === a.id
                return (
                  <button
                    key={a.id}
                    type="button"
                    role="option"
                    aria-selected={on}
                    onClick={() => setPick(a)}
                    onDoubleClick={() => void start(a)}
                    className={cn(
                      "flex items-center gap-3 rounded-xl border px-3 py-2 text-start outline-none hover:bg-nq-hover focus-visible:outline-2 focus-visible:outline-nq-focus",
                      on ? "border-nq-brand bg-nq-hover" : "border-transparent",
                    )}
                  >
                    <ServerMark server={{ color: a.color ?? null, icon: a.icon ?? "globe", logo_url: a.logo_url ?? null }} size={8} />
                    <span className="flex min-w-0 flex-1 flex-col">
                      <span dir="auto" className="truncate text-body-sm font-medium text-foreground">
                        {a.title || a.name}
                      </span>
                      <span dir="ltr" className="truncate text-start text-caption text-muted-foreground">
                        {[a.branch, a.server_name].filter(Boolean).join(" · ")}
                      </span>
                    </span>
                    {on ? <CheckIcon aria-hidden className="size-4 text-nq-brand" /> : null}
                  </button>
                )
              })
            )}
          </div>
          <FormError message={error} />
          <DialogFooter>
            <Button variant="secondary" onClick={() => change(false)}>
              {t("common.cancel")}
            </Button>
            <Button variant="primary" disabled={!pick || busy} onClick={() => void start(pick)}>
              <RocketIcon />
              {busy ? t("deployNow.starting") : pick ? t("deployNow.start", { app: pick.title || pick.name }) : t("deployNow.button")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}
