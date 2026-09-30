"use client"

import { Button } from "@fadymondy/nasaq/web"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { LayoutGridIcon, ServerIcon } from "lucide-react"

import { PageBody } from "@/components/app-shell"
import { DomainsPanel } from "@/components/apps/domains"
import { useErrorText } from "@/components/form-error"
import { ImportedBadge, ImportedNotice, originSummary, OriginFacts } from "@/components/imported"
import {
  DangerZone,
  DatabaseCredentials,
  GuestBackup,
  GuestSnapshots,
  PowerControls,
  RecreateContainer,
  ResizeGuest,
  useWorkloadActions,
  WorkloadLogs,
  WorkloadStats,
} from "@/components/imported-controls"
import { ErrorBand, SectionTitle } from "@/components/states"

import { apiFetch } from "@/lib/api"
import type { App } from "@/lib/apps"
import type { DataService } from "@/lib/databases"
import { useI18n } from "@/lib/i18n"
import { canOperate } from "@/lib/servers"
import { useTeam } from "@/lib/team"

/** The first published host port of a container ("0.0.0.0:8081->80/tcp" → 8081). */
function firstPort(ports?: string[]): number | undefined {
  for (const p of ports ?? []) {
    const m = /:(\d+)->/.exec(p)
    if (m) return Number(m[1])
  }
  return undefined
}

/**
 * An imported site: a Proxmox guest or a container Hosbah found on the server, managed like any
 * service — power, stats, logs, domains with HTTPS, and for guests resize / snapshots / backups,
 * for containers pull-and-recreate. Only deploying from git does not apply (there is no build).
 */
export function ImportedAppView({ app, apiPath, onChanged }: { app: App; apiPath: string; onChanged?: () => void }) {
  const { t, locale } = useI18n()
  const { team, manage } = useTeam()
  const router = useRouter()
  const errorText = useErrorText()
  const operate = canOperate(team.role)
  const base = `/${locale}/t/${team.slug}`
  const guest = app.origin_kind === "guest"
  const container = app.origin_kind === "container"
  const { run, busy, error, log } = useWorkloadActions(apiPath, app.server_id)
  const missing = app.imported_state === "missing"

  return (
    <PageBody
      eyebrow={t("imported.eyebrowApp")}
      title={<span dir="ltr">{app.name}</span>}
      description={originSummary(app, t)}
      actions={
        <>
          <ImportedBadge item={app} />
          <Button variant="secondary" nativeButton={false} render={<Link href={`${base}/servers/${app.server_id}/sites/${app.id}`} />}>
            <LayoutGridIcon />
            {t("sites.canvas")}
          </Button>
        </>
      }
      bleed
    >
      <ImportedNotice kind="app" />
      <ErrorBand message={error} />

      {operate && !missing ? (
        <section aria-labelledby="power-h">
          <SectionTitle id="power-h" title={t("controls.power")} hint={t("controls.powerHint")} />
          <div className="border-b border-border px-6 py-4">
            <PowerControls item={app} run={(k, s, i) => run(k, s, i).then(() => onChanged?.())} busy={busy} guest={guest} />
          </div>
        </section>
      ) : null}

      <OriginFacts
        item={app}
        extra={[
          [
            t("apps.server"),
            <Link key="srv" href={`${base}/servers/${app.server_id}`} className="inline-flex items-center gap-1.5 hover:underline">
              <ServerIcon className="size-3.5 text-muted-foreground" />
              <span dir="ltr">{app.server_name ?? app.server_id}</span>
            </Link>,
          ],
        ]}
      />

      {operate && !missing ? <WorkloadStats path={apiPath} /> : null}
      {operate && container && !missing ? <WorkloadLogs run={run} busy={busy} /> : null}
      {log}

      <section aria-labelledby="domains-h">
        <SectionTitle id="domains-h" title={t("apps.tabs.domains")} hint={t("controls.domainsHint")} className="border-t" />
        <DomainsPanel
          team={team.slug}
          path={apiPath}
          manage={manage}
          port={guest ? { required: true } : { required: false, suggested: firstPort(app.origin?.ports) }}
        />
      </section>

      {operate && guest && !missing ? (
        <>
          <ResizeGuest run={run} busy={busy} item={app} />
          <GuestSnapshots path={apiPath} name={app.name} run={run} busy={busy} admin={manage} />
          <GuestBackup run={run} busy={busy} />
        </>
      ) : null}
      {operate && container && !missing ? <RecreateContainer run={run} /> : null}

      {manage ? (
        <DangerZone
          name={app.name}
          admin={manage && !missing}
          guest={guest}
          onForget={async () => {
            try {
              await apiFetch(apiPath, { method: "DELETE", body: { confirm: app.name } })
            } catch (err) {
              throw new Error(errorText(err))
            }
            router.replace(`${base}/apps`)
          }}
          onDestroy={async (purge) => {
            try {
              await run("destroy", "?destroy=true", { method: "DELETE", body: { confirm: app.name, purge } })
            } catch (err) {
              throw new Error(errorText(err))
            }
          }}
        />
      ) : null}
    </PageBody>
  )
}

/**
 * An imported database: power, stats, logs and recreate like an imported site; with its admin
 * login added, its databases, users and backups are managed like any Hosbah database (the page
 * then shows the full database tools, see databases/[service]).
 */
export function ImportedDatabaseView({ service, onChanged }: { service: DataService; onChanged: () => void }) {
  const { t, locale } = useI18n()
  const { team, manage } = useTeam()
  const router = useRouter()
  const errorText = useErrorText()
  const base = `/${locale}/t/${team.slug}`
  const apiPath = `/api/teams/${team.slug}/data-services/${service.id}`

  return (
    <PageBody
      eyebrow={t("imported.eyebrowDatabase")}
      title={<span dir="ltr">{service.name}</span>}
      description={`${t(`databases.kinds.${service.kind}`, undefined, service.kind)} ${service.version}`}
      actions={<ImportedBadge item={service} />}
      bleed
    >
      <ImportedNotice kind="database" />
      <ImportedDatabaseControls service={service} onChanged={onChanged} />
      <OriginFacts
        item={service}
        extra={[
          [
            t("apps.server"),
            <Link key="srv" href={`${base}/servers/${service.server_id}`} className="inline-flex items-center gap-1.5 hover:underline">
              <ServerIcon className="size-3.5 text-muted-foreground" />
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
      {manage && service.credentials_required ? <DatabaseCredentials path={apiPath} onSaved={onChanged} /> : null}
      {manage ? (
        <DangerZone
          name={service.name}
          admin={manage && service.imported_state !== "missing"}
          guest={service.origin_kind === "guest"}
          onForget={async () => {
            try {
              await apiFetch(apiPath, { method: "DELETE" })
            } catch (err) {
              throw new Error(errorText(err))
            }
            router.replace(`${base}/databases`)
          }}
          onDestroy={async (purge) => {
            try {
              await apiFetch(`${apiPath}?destroy=true`, { method: "DELETE", body: { confirm: service.name, purge } })
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

/** Power, stats, logs and recreate for an imported database — also shown atop its full tools. */
export function ImportedDatabaseControls({ service, onChanged }: { service: DataService; onChanged: () => void }) {
  const { t } = useI18n()
  const { team } = useTeam()
  const operate = canOperate(team.role)
  const apiPath = `/api/teams/${team.slug}/data-services/${service.id}`
  const { run, busy, error, log } = useWorkloadActions(apiPath, service.server_id)
  const guest = service.origin_kind === "guest"
  const container = service.origin_kind === "container"
  if (!operate || service.imported_state === "missing") return null
  return (
    <>
      <ErrorBand message={error} />
      <section aria-labelledby="dbpower-h">
        <SectionTitle id="dbpower-h" title={t("controls.power")} hint={t("controls.powerHint")} />
        <div className="border-b border-border px-6 py-4">
          <PowerControls item={service} run={(k, s, i) => run(k, s, i).then(() => onChanged())} busy={busy} guest={guest} />
        </div>
      </section>
      <WorkloadStats path={apiPath} />
      {container ? <WorkloadLogs run={run} busy={busy} /> : null}
      {container ? <RecreateContainer run={run} /> : null}
      {log}
    </>
  )
}
