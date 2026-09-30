"use client"

import { Badge, EmptyState, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useState } from "react"
import { PackageIcon } from "lucide-react"

import { ConfirmButton } from "@/components/confirm-button"
import { useErrorText } from "@/components/form-error"
import { ErrorBand, LoadingRows, Strip, TableBand } from "@/components/states"



import { apiFetch } from "@/lib/api"
import { shortSha, type Deployment, type Release } from "@/lib/apps"
import { useFormatDate, useI18n } from "@/lib/i18n"

export function ReleasesPanel({
  path,
  operate,
  liveId,
  onRollback,
}: {
  path: string
  operate: boolean
  liveId: string | null
  onRollback: (d: Deployment) => void
}) {
  const { t } = useI18n()
  const fmt = useFormatDate()
  const errorText = useErrorText()
  const [releases, setReleases] = useState<Release[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      const r = await apiFetch<{ releases: Release[] }>(`${path}/releases`)
      setReleases(r.releases ?? [])
    } catch (err) {
      setError(errorText(err))
      setReleases((x) => x ?? [])
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path])

  // Reload whenever the live release changes (a deploy or rollback finished).
  useEffect(() => {
    void load()
  }, [load, liveId])

  return (
    <div data-testid="releases">
      <Strip>
        <p className="text-sm text-muted-foreground">{t("apps.releases.hint")}</p>
      </Strip>
      <ErrorBand message={error} />
      {releases === null ? (
        <LoadingRows />
      ) : releases.length === 0 ? (
        <EmptyState className="border-b" icon={PackageIcon} title={t("apps.releases.empty")} />
      ) : (
        <TableBand>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("apps.releases.version")}</TableHead>
                <TableHead>{t("apps.status")}</TableHead>
                <TableHead>{t("apps.commit")}</TableHead>
                <TableHead>{t("apps.releases.image")}</TableHead>
                <TableHead>{t("apps.releases.activated")}</TableHead>
                {operate ? <TableHead className="text-end">{t("common.actions")}</TableHead> : null}
              </TableRow>
            </TableHeader>
            <TableBody>
              {releases.map((r) => (
                <TableRow key={r.id} data-release={r.version}>
                  <TableCell dir="ltr" className="text-start font-mono text-sm">
                    v{r.version}
                  </TableCell>
                  <TableCell>
                    <Badge variant={r.status === "live" ? "brand" : r.status === "standby" ? "neutral" : "outline"}>
                      {t(`apps.releases.statuses.${r.status}`)}
                    </Badge>
                  </TableCell>
                  <TableCell dir="ltr" className="text-start font-mono text-xs">
                    {shortSha(r.commit_sha)}
                  </TableCell>
                  <TableCell dir="ltr" className="max-w-64 truncate text-start font-mono text-xs" title={r.image}>
                    {r.image}
                  </TableCell>
                  <TableCell dir="ltr" className="text-start text-xs">
                    {fmt(r.activated_at ?? r.created_at)}
                  </TableCell>
                  {operate ? (
                    <TableCell className="text-end">
                      {r.status === "standby" ? (
                        <ConfirmButton
                          label={t("apps.releases.rollbackTo")}
                          title={t("apps.actions.rollbackTitle", { version: r.version })}
                          description={t("apps.releases.rollbackHint", { version: r.version })}
                          confirmLabel={t("apps.actions.rollback")}
                          onConfirm={async () => {
                            try {
                              const res = await apiFetch<{ deployment: Deployment }>(`${path}/rollback`, {
                                method: "POST",
                                body: { release: r.id },
                              })
                              onRollback(res.deployment)
                            } catch (err) {
                              throw new Error(errorText(err))
                            }
                          }}
                        />
                      ) : r.status === "pruned" ? (
                        <span className="text-xs text-muted-foreground">{t("apps.releases.prunedHint")}</span>
                      ) : null}
                    </TableCell>
                  ) : null}
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </TableBand>
      )}
    </div>
  )
}
