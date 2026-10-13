import { useState } from "react";
import clsx from "clsx";
import { AlertTriangle, Bell, CheckCircle2, ExternalLink, ServerOff, ShieldAlert } from "lucide-react";
import { relativeTime, type App, type AppNotification, type NotificationsResponse } from "@hosbah/client";
import { alertLevel } from "../../core/trayStatus";
import { linkToHash, routeForNotification } from "../../core/route";
import { Button, Card, Empty, ErrorNote, PageHeader, Spinner } from "../components/ui";
import { useApp } from "../lib/app";
import { call, data } from "../lib/bridge";
import { useData } from "../lib/useData";

const tone = { red: "bg-danger/15 text-danger-text", amber: "bg-warn/20 text-warn-text", green: "bg-ok/15 text-ok-text" } as const;

function KindIcon({ kind }: { kind: string }) {
  const Icon = kind === "server.offline" ? ServerOff : kind === "cert.expiring" ? ShieldAlert : kind.endsWith(".failed") || kind === "app.down" || kind === "resource.high" ? AlertTriangle : kind.endsWith(".succeeded") || kind === "app.up" ? CheckCircle2 : Bell;
  return (
    <span className={clsx("grid size-9 shrink-0 place-items-center rounded-full", tone[alertLevel(kind)])}>
      <Icon className="size-4" />
    </span>
  );
}

export function Alerts() {
  const { t, locale, route } = useApp();
  const team = route.team;
  const [busy, setBusy] = useState(false);
  const q = useData(
    async () => {
      const [list, apps] = await Promise.all([data<NotificationsResponse>("notifications", team), data<App[]>("apps", team).catch(() => [] as App[])]);
      return { list, apps };
    },
    [team],
    { every: 30_000, onMonitor: true },
  );
  if (q.loading && !q.data) return <Spinner />;
  if (!q.data) return q.error ? <ErrorNote error={q.error} onRetry={q.reload} /> : null;
  const { list, apps } = q.data;

  const open = (n: AppNotification) => {
    const hash = linkToHash(routeForNotification(team, n, apps.map((a) => ({ id: a.id, name: a.name }))));
    window.location.hash = hash;
  };
  const mark = async (id: string) => {
    setBusy(true);
    try {
      await call("alerts:markRead", team, id);
      q.reload();
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <PageHeader
        title={t("alerts.title")}
        sub={list.unread > 0 ? t("alerts.unread", { count: list.unread }) : t("alerts.allRead")}
        actions={
          list.unread > 0 ? (
            <Button busy={busy} onClick={() => void mark("all")}>
              {t("alerts.markAllRead")}
            </Button>
          ) : undefined
        }
      />
      {list.notifications.length === 0 ? (
        <Empty>{t("alerts.empty")}</Empty>
      ) : (
        <div className="space-y-2">
          {list.notifications.map((n) => {
            const unread = !n.read_at;
            return (
              <Card key={n.id} className={clsx("flex items-start gap-3 px-4 py-3", unread && "border-action/40")}>
                <KindIcon kind={n.kind} />
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    {unread && <span aria-label={t("alerts.unread", { count: 1 })} className="size-2 shrink-0 rounded-full bg-action" />}
                    <span className={clsx("truncate text-sm", unread ? "font-semibold text-fg" : "text-body")}>{n.title}</span>
                  </div>
                  {n.body && <p className="mt-0.5 line-clamp-2 text-xs text-muted">{n.body}</p>}
                  <div className="mt-1 text-[11px] text-muted">{relativeTime(n.created_at, locale)}</div>
                </div>
                <div className="flex shrink-0 gap-1">
                  <Button variant="ghost" onClick={() => open(n)} aria-label={t("alerts.open")} title={t("alerts.open")}>
                    <ExternalLink className="size-4" />
                  </Button>
                  {unread && (
                    <Button variant="ghost" disabled={busy} onClick={() => void mark(n.id)}>
                      {t("alerts.markRead")}
                    </Button>
                  )}
                </div>
              </Card>
            );
          })}
        </div>
      )}
    </>
  );
}
