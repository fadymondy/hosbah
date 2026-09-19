"use client"

import Link from "next/link"
import { useRouter } from "next/navigation"
import { LayoutGridIcon, ServerIcon, Trash2Icon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { ConfirmButton } from "@/components/confirm-button"
import { useErrorText } from "@/components/form-error"
import { ImportedBadge, ImportedNotice, originSummary, OriginFacts } from "@/components/imported"
import { SectionTitle } from "@/components/states"
import { Button } from "@/components/ui/button"
import { apiFetch } from "@/lib/api"
import type { App } from "@/lib/apps"
import type { DataService } from "@/lib/databases"
import { useI18n } from "@/lib/i18n"
import { useTeam } from "@/lib/team"

/**
 * An imported site: what Hosbah found running on the server, shown read-only. The only action is
 * removing the record — the workload itself is never touched (the API refuses everything else).
 */
export function ImportedAppView({ app, apiPath }: { app: App; apiPath: string }) {
  const { t, locale } = useI18n()
  const { team, manage } = useTeam()
  const router = useRouter()
  const errorText = useErrorText()
  const base = `/${locale}/t/${team.slug}`

  return (
    <PageBody
      eyebrow={t("imported.eyebrowApp")}
      title={<span dir="ltr">{app.name}</span>}
      description={originSummary(app, t)}
      actions={
        <>
          <ImportedBadge item={app} />
          <Button variant="outline" nativeButton={false} render={<Link href={`${base}/servers/${app.server_id}/sites/${app.id}`} />}>
            <LayoutGridIcon />
            {t("sites.canvas")}
          </Button>
        </>
      }
      bleed
    >
      <ImportedNotice kind="app" />
      <OriginFacts
        item={app}
        extra={[
          [
            t("apps.server"),
            <Link key="srv" href={`${base}/servers/${app.server_id}`} className="inline-flex items-center gap-1.5 hover:underline">
              <ServerIcon className="size-3.5 text-grid-muted" />
              <span dir="ltr">{app.server_name ?? app.server_id}</span>
            </Link>,
          ],
        ]}
      />
      {manage ? <RemoveRecord name={app.name} onRemove={async () => {
        try {
          await apiFetch(apiPath, { method: "DELETE", body: { confirm: app.name } })
        } catch (err) {
          throw new Error(errorText(err))
        }
        router.replace(`${base}/apps`)
      }} /> : null}
    </PageBody>
  )
}

/** An imported database: the engine found on the server, read-only; removable as a record only. */
export function ImportedDatabaseView({ service }: { service: DataService }) {
  const { t, locale } = useI18n()
  const { team, manage } = useTeam()
  const router = useRouter()
  const errorText = useErrorText()
  const base = `/${locale}/t/${team.slug}`

  return (
    <PageBody
      eyebrow={t("imported.eyebrowDatabase")}
      title={<span dir="ltr">{service.name}</span>}
      description={`${t(`databases.kinds.${service.kind}`, undefined, service.kind)} ${service.version}`}
      actions={<ImportedBadge item={service} />}
      bleed
    >
      <ImportedNotice kind="database" />
      <OriginFacts
        item={service}
        extra={[
          [
            t("apps.server"),
            <Link key="srv" href={`${base}/servers/${service.server_id}`} className="inline-flex items-center gap-1.5 hover:underline">
              <ServerIcon className="size-3.5 text-grid-muted" />
              <span dir="ltr">{service.server_id.slice(0, 8)}</span>
            </Link>,
          ],
          [
            t("imported.facts.endpoint"),
            <span key="ep" dir="ltr" className="font-mono">
              {service.host ? `${service.host}${service.port ? `:${service.port}` : ""}` : "—"}
            </span>,
          ],
        ]}
      />
      {manage ? (
        <RemoveRecord
          name={service.name}
          onRemove={async () => {
            try {
              await apiFetch(`/api/teams/${team.slug}/data-services/${service.id}`, { method: "DELETE" })
            } catch (err) {
              throw new Error(errorText(err))
            }
            router.replace(`${base}/databases`)
          }}
        />
      ) : null}
    </PageBody>
  )
}

function RemoveRecord({ name, onRemove }: { name: string; onRemove: () => Promise<void> }) {
  const { t } = useI18n()
  return (
    <section>
      <SectionTitle title={t("imported.removeTitle")} hint={t("imported.removeHint")} className="border-t" />
      <div className="flex border-b border-line px-6 py-4">
        <ConfirmButton
          size="default"
          icon={<Trash2Icon />}
          label={t("imported.remove")}
          title={t("imported.removeConfirmTitle", { name })}
          description={t("imported.removeConfirmBody", { name })}
          confirmLabel={t("imported.remove")}
          onConfirm={onRemove}
        />
      </div>
    </section>
  )
}
