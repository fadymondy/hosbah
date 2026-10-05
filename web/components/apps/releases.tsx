"use client"

import { Badge, EmptyState, useConfirm } from "@fadymondy/nasaq/web"
import { useCallback, useEffect, useState } from "react"
import { PackageIcon, RotateCcwIcon } from "lucide-react"

import { useErrorText } from "@/components/form-error"
import { NasaqTable } from "@/components/lists/nasaq-table"
import { ErrorBand, LoadingRows, Strip } from "@/components/states"

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
  const confirm = useConfirm()
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

  async function rollback(r: Release) {
    const ok = await confirm({
      title: t("apps.actions.rollbackTitle", { version: r.version }),
      description: t("apps.releases.rollbackHint", { version: r.version }),
      confirmLabel: t("apps.actions.rollback"),
    })
    if (!ok) return
    setError(null)
    try {
      const res = await apiFetch<{ deployment: Deployment }>(`${path}/rollback`, { method: "POST", body: { release: r.id } })
      onRollback(res.deployment)
    } catch (err) {
      setError(errorText(err))
    }
  }

  return (
    <div data-testid="releases" data-stack>
      <Strip>
        <p className="text-sm text-muted-foreground">{t("apps.releases.hint")}</p>
      </Strip>
      <ErrorBand message={error} />
      {releases === null ? (
        <LoadingRows />
      ) : releases.length === 0 ? (
        <EmptyState className="nq-empty" icon={PackageIcon} title={t("apps.releases.empty")} />
      ) : (
        <NasaqTable
          data={releases}
          rowId={(r) => r.id}
          rowLabel={(r) => `v${r.version}`}
          label={t("site.nav.releases")}
          defaultSort={{ id: "version", direction: "desc" }}
          rowActions={
            operate
              ? (r) => [
                  {
                    id: "rollback",
                    label: r.status === "pruned" ? t("apps.releases.prunedHint") : t("apps.releases.rollbackTo"),
                    icon: RotateCcwIcon,
                    disabled: r.status !== "standby",
                    onSelect: () => void rollback(r),
                  },
                ]
              : undefined
          }
          columns={[
            {
              id: "version",
              header: t("apps.releases.version"),
              cell: (r) => (
                <span dir="ltr" className="font-mono text-sm">
                  v{r.version}
                </span>
              ),
              sortValue: (r) => r.version,
              searchValue: (r) => `v${r.version} ${r.commit_sha ?? ""} ${r.image}`,
            },
            {
              id: "status",
              header: t("apps.status"),
              cell: (r) => (
                <Badge variant={r.status === "live" ? "brand" : r.status === "standby" ? "neutral" : "outline"}>{t(`apps.releases.statuses.${r.status}`)}</Badge>
              ),
              sortValue: (r) => r.status,
            },
            {
              id: "commit",
              header: t("apps.commit"),
              cell: (r) => (
                <span dir="ltr" className="font-mono text-xs">
                  {shortSha(r.commit_sha)}
                </span>
              ),
            },
            {
              id: "image",
              header: t("apps.releases.image"),
              cell: (r) => (
                <span dir="ltr" className="block max-w-64 truncate text-start font-mono text-xs" title={r.image}>
                  {r.image}
                </span>
              ),
            },
            {
              id: "activated",
              header: t("apps.releases.activated"),
              cell: (r) => (
                <span dir="ltr" className="text-xs">
                  {fmt(r.activated_at ?? r.created_at)}
                </span>
              ),
              sortValue: (r) => r.activated_at ?? r.created_at,
            },
          ]}
        />
      )}
    </div>
  )
}
