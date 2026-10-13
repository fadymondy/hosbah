import { appHealth, relativeTime, type App, type Deployment } from "@hosbah/client";
import { Badge, Card, Empty, ErrorNote, PageHeader, Spinner } from "../components/ui";
import { useApp } from "../lib/app";
import { data } from "../lib/bridge";
import { useData } from "../lib/useData";

export const deployHealth = (s: Deployment["status"]) => (s === "failed" ? "down" : s === "succeeded" ? "ok" : s === "cancelled" ? "warn" : "ok");

export function Apps() {
  const { t, locale, route, go } = useApp();
  const team = route.team;
  const q = useData(() => data<App[]>("apps", team), [team], { every: 20_000, onMonitor: true });

  if (q.loading && !q.data) return <Spinner />;
  if (!q.data) return q.error ? <ErrorNote error={q.error} onRetry={q.reload} /> : null;

  return (
    <>
      <PageHeader title={t("tab.apps")} sub={`${q.data.length}`} />
      {q.error && <div className="mb-4"><ErrorNote error={q.error} onRetry={q.reload} /></div>}
      {q.data.length === 0 ? (
        <Empty>{t("apps.empty")}</Empty>
      ) : (
        <div className="space-y-2">
          {q.data.map((a) => {
            const d = a.latest_deployment;
            return (
              <button key={a.id} onClick={() => go({ team, section: "apps", id: a.id })} className="block w-full text-start">
                <Card className="flex items-center gap-4 px-4 py-3 transition hover:border-action/60">
                  <div className="min-w-0 flex-1">
                    <div className="truncate font-medium text-fg">{a.title || a.name}</div>
                    <div className="ltr truncate text-xs text-muted">
                      {a.name}
                      {a.server_name ? ` · ${a.server_name}` : ""}
                    </div>
                  </div>
                  <div className="hidden min-w-0 text-xs text-muted sm:block">
                    <div className="mb-0.5">{t("app.lastDeploy")}</div>
                    {d ? (
                      <div className="flex items-center gap-2">
                        <Badge health={deployHealth(d.status)}>{t(`deploy.${d.status}` as never)}</Badge>
                        <span>{relativeTime(d.finished_at ?? d.created_at, locale)}</span>
                      </div>
                    ) : (
                      <span>{t("app.noLastDeploy")}</span>
                    )}
                  </div>
                  <Badge health={appHealth(a)}>{t(`app.${a.status}` as never)}</Badge>
                </Card>
              </button>
            );
          })}
        </div>
      )}
    </>
  );
}
