import { ShieldCheck, Server as ServerIcon, Boxes, DatabaseBackup } from "lucide-react";
import {
  appHealth,
  backupHealth,
  certificateHealth,
  duration,
  formatBytes,
  relativeTime,
  serverHealth,
  usagePercent,
  type App,
  type Certificate,
  type DataService,
  type Health,
  type Server,
  type ServersResponse,
} from "@hosbah/client";
import { Badge, Card, Dot, Empty, ErrorNote, Meter, PageHeader, Spinner } from "../components/ui";
import { useApp } from "../lib/app";
import { data } from "../lib/bridge";
import { useData } from "../lib/useData";

export function serverBadge(s: Server, t: ReturnType<typeof useApp>["t"]): { health: Health; label: string } {
  const health = serverHealth(s);
  if (s.status !== "enrolled") return { health, label: t(`server.${s.status}` as never) };
  return { health, label: s.online ? (health === "warn" ? t("health.warn") : t("server.online")) : t("server.offline") };
}

const worst = (hs: Health[]): Health => (hs.includes("down") ? "down" : hs.includes("warn") ? "warn" : "ok");

function Tile({ icon, label, value, sub, health }: { icon: React.ReactNode; label: string; value: string; sub: string; health: Health }) {
  return (
    <Card className="p-4">
      <div className="flex items-center justify-between text-muted">
        <span className="text-xs">{label}</span>
        {icon}
      </div>
      <div className="mt-2 flex items-center gap-2">
        <Dot health={health} />
        <span className="text-2xl font-semibold text-fg">{value}</span>
      </div>
      <div className="mt-0.5 text-xs text-muted">{sub}</div>
    </Card>
  );
}

export function Overview() {
  const { t, locale, route, go } = useApp();
  const team = route.team;
  const q = useData(
    async () => {
      const [servers, apps, certs, services] = await Promise.all([
        data<ServersResponse>("servers", team),
        data<App[]>("apps", team),
        data<Certificate[]>("certificates", team).catch(() => [] as Certificate[]),
        data<DataService[]>("dataServices", team).catch(() => [] as DataService[]),
      ]);
      return { servers: servers.servers, apps, certs, services };
    },
    [team],
    { every: 30_000, onMonitor: true },
  );

  if (q.loading && !q.data) return <Spinner />;
  if (!q.data) return q.error ? <ErrorNote error={q.error} onRetry={q.reload} /> : null;
  const { servers, apps, certs, services } = q.data;

  const online = servers.filter((s) => s.status === "enrolled" && s.online).length;
  const running = apps.filter((a) => a.status === "running").length;
  const soon = certs.filter((c) => certificateHealth(c) !== "ok").length;
  const backupsOk = services.filter((s) => s.backups && backupHealth(s.backups) === "ok").length;
  const withBackups = services.filter((s) => s.backups).length;

  return (
    <>
      <PageHeader title={t("overview.title")} sub={t("overview.refreshed", { when: relativeTime(Date.now(), locale) })} />
      {q.error && <div className="mb-4"><ErrorNote error={q.error} onRetry={q.reload} /></div>}

      <div className="mb-6 grid grid-cols-2 gap-3 lg:grid-cols-4">
        <Tile icon={<ServerIcon className="size-4" />} label={t("overview.servers")} value={`${online}/${servers.length}`} sub={t("overview.onlineOf", { online, total: servers.length })} health={worst(servers.map(serverHealth))} />
        <Tile icon={<Boxes className="size-4" />} label={t("overview.apps")} value={`${running}/${apps.length}`} sub={t("overview.runningOf", { running, total: apps.length })} health={worst(apps.map(appHealth))} />
        <Tile
          icon={<ShieldCheck className="size-4" />}
          label={t("overview.certificates")}
          value={String(soon)}
          sub={soon ? t("overview.certsSoon", { count: soon }) : t("overview.allClear")}
          health={worst(certs.map(certificateHealth))}
        />
        <Tile
          icon={<DatabaseBackup className="size-4" />}
          label={t("overview.backups")}
          value={`${backupsOk}/${withBackups}`}
          sub={t("overview.backupsOk", { ok: backupsOk, total: withBackups })}
          health={worst(services.map((s) => backupHealth(s.backups)))}
        />
      </div>

      <h2 className="mb-3 text-sm font-semibold text-fg">{t("overview.servers")}</h2>
      {servers.length === 0 ? (
        <Empty>{t("overview.noServers")}</Empty>
      ) : (
        <div className="grid gap-3 md:grid-cols-2">
          {servers.map((s) => {
            const b = serverBadge(s, t);
            const m = s.metrics;
            return (
              <button key={s.id} onClick={() => go({ team, section: "servers", id: s.id })} className="text-start">
                <Card className="p-4 transition hover:border-action/60">
                  <div className="mb-3 flex items-center justify-between gap-2">
                    <div className="min-w-0">
                      <div className="truncate font-medium text-fg">{s.name}</div>
                      <div className="ltr truncate text-xs text-muted">{s.ip_address ?? s.hostname ?? ""}</div>
                    </div>
                    <Badge health={b.health}>{b.label}</Badge>
                  </div>
                  {m ? (
                    <div className="grid grid-cols-3 gap-4">
                      <Meter label={t("server.cpu")} value={m.cpu_percent} />
                      <Meter label={t("server.memory")} value={usagePercent(m.mem_used_bytes, m.mem_total_bytes)} sub={`${formatBytes(m.mem_used_bytes)} / ${formatBytes(m.mem_total_bytes)}`} />
                      <Meter label={t("server.disk")} value={usagePercent(m.disk_used_bytes, m.disk_total_bytes)} sub={`${formatBytes(m.disk_used_bytes)} / ${formatBytes(m.disk_total_bytes)}`} />
                    </div>
                  ) : (
                    <div className="text-xs text-muted">{t("common.none")}</div>
                  )}
                  {m && (
                    <div className="mt-3 text-xs text-muted">
                      {t("server.uptime")}: <span className="ltr">{duration(m.uptime_seconds, locale)}</span>
                    </div>
                  )}
                </Card>
              </button>
            );
          })}
        </div>
      )}
    </>
  );
}
