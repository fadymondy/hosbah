import { useState } from "react";
import { ChevronDown, ChevronUp } from "lucide-react";
import { backupHealth, formatBytes, relativeTime, type Backup, type DataService } from "@hosbah/client";
import { Badge, Card, Empty, ErrorNote, PageHeader, Spinner } from "../components/ui";
import { useApp } from "../lib/app";
import { data } from "../lib/bridge";
import { useData } from "../lib/useData";

function History({ team, service }: { team: string; service: string }) {
  const { t, locale } = useApp();
  const q = useData(() => data<Backup[]>("backups", team, service), [team, service]);
  if (!q.data) return <Spinner />;
  if (q.data.length === 0) return <p className="mt-3 text-xs text-muted">{t("backup.noneYet")}</p>;
  return (
    <div className="mt-3 divide-y divide-line border-t border-line">
      {q.data.slice(0, 6).map((b) => (
        <div key={b.id} className="flex items-center gap-3 py-2 text-xs">
          <Badge health={b.status === "failed" ? "down" : b.status === "succeeded" ? "ok" : "warn"}>{b.status === "succeeded" ? t("backup.succeeded") : b.status === "failed" ? t("backup.failed") : t("backup.pending")}</Badge>
          <span className="text-muted">{b.kind === "manual" ? t("backup.manual") : t("backup.scheduled")}</span>
          <span className="ltr text-body">{formatBytes(b.size_bytes)}</span>
          <span className="ms-auto text-muted">{relativeTime(b.taken_at ?? b.created_at, locale)}</span>
        </div>
      ))}
    </div>
  );
}

export function Backups() {
  const { t, locale, route } = useApp();
  const team = route.team;
  const [open, setOpen] = useState<string | null>(null);
  const q = useData(() => data<DataService[]>("dataServices", team), [team], { every: 60_000, onMonitor: true });
  if (q.loading && !q.data) return <Spinner />;
  if (!q.data) return q.error ? <ErrorNote error={q.error} onRetry={q.reload} /> : null;

  return (
    <>
      <PageHeader title={t("backup.title")} sub={`${q.data.length}`} />
      {q.data.length === 0 ? (
        <Empty>{t("backup.empty")}</Empty>
      ) : (
        <div className="space-y-2">
          {q.data.map((s) => {
            const b = s.backups;
            const h = backupHealth(b);
            const isOpen = open === s.id;
            return (
              <Card key={s.id} className="px-4 py-3">
                <button className="flex w-full flex-wrap items-center gap-4 text-start" onClick={() => setOpen(isOpen ? null : s.id)} aria-expanded={isOpen}>
                  <div className="min-w-0 flex-1">
                    <div className="ltr truncate font-medium text-fg">{s.name}</div>
                    <div className="ltr text-xs text-muted">
                      {s.kind} {s.version}
                    </div>
                  </div>
                  {b ? (
                    <div className="text-xs text-muted">
                      <div>{b.last_ok_at ? t("backup.last", { when: relativeTime(b.last_ok_at, locale) }) : t("backup.never")}</div>
                      <div>
                        {b.count > 0 && <span className="ltr me-2">{formatBytes(b.last_ok_size)}</span>}
                        {b.schedule && !b.schedule_paused && b.next_run_at ? `${t("backup.next")}: ${relativeTime(b.next_run_at, locale)}` : b.schedule_paused ? t("backup.paused") : ""}
                      </div>
                    </div>
                  ) : (
                    <span className="text-xs text-muted">{t("backup.noBackups")}</span>
                  )}
                  <Badge health={h}>{b?.last_status === "failed" ? t("backup.failed") : h === "warn" ? t("backup.stale") : t("backup.ok")}</Badge>
                  {isOpen ? <ChevronUp className="size-4 text-muted" /> : <ChevronDown className="size-4 text-muted" />}
                </button>
                {isOpen && <History team={team} service={s.id} />}
              </Card>
            );
          })}
        </div>
      )}
    </>
  );
}
