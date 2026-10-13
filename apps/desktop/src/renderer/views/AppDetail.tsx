import { useEffect, useState } from "react";
import { ArrowLeft, ArrowRight, ChevronDown, ChevronUp, Play, RefreshCw, RotateCcw, Square } from "lucide-react";
import { appHealth, canOperate, duration, relativeTime, type AppDetail as AppDetailT, type Deployment, type DeploymentDetail } from "@hosbah/client";
import type { ActionRequest, AppAction } from "../../core/ipc";
import { Badge, Button, Card, Dialog, Empty, ErrorNote, PageHeader, Spinner } from "../components/ui";
import { useApp } from "../lib/app";
import { BridgeError, call, data } from "../lib/bridge";
import { useData } from "../lib/useData";
import { deployHealth } from "./Apps";

const ICON = { redeploy: RefreshCw, restart: RotateCcw, start: Play, stop: Square } as const;

function ConfirmAction({ team, app, name, action, onClose, onDone }: { team: string; app: string; name: string; action: AppAction; onClose: () => void; onDone: () => void }) {
  const { t, info } = useApp();
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const touch = !!info?.touchId;
  const ok = touch || typed.trim() === name;
  const label = t(`action.${action}` as never);

  async function run() {
    setBusy(true);
    setError(null);
    try {
      await call("action", { team, app, appName: name, action } satisfies ActionRequest);
      onDone();
    } catch (e) {
      const be = e as BridgeError;
      setError(be.code === "cancelled" ? t("action.denied") : `${t("action.failed", { action: label })}: ${be.message}`);
      setBusy(false);
    }
  }

  return (
    <Dialog
      title={t(`confirm.${action}` as never, { app: name })}
      onClose={onClose}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            {t("confirm.cancel")}
          </Button>
          <Button variant={action === "stop" ? "danger" : "primary"} disabled={!ok} busy={busy} onClick={run}>
            {busy ? t("confirm.working") : label}
          </Button>
        </>
      }
    >
      <p className="text-sm text-body">{t(`confirm.desc.${action}` as never)}</p>
      {touch ? (
        <p className="mt-3 text-sm text-muted">{t("confirm.touchId")}</p>
      ) : (
        <label className="mt-4 block">
          <span className="mb-1 block text-xs text-muted">{t("confirm.type", { name })}</span>
          <input
            className="ltr w-full rounded-lg border border-line bg-card px-3 py-2 text-sm text-fg"
            value={typed}
            onChange={(e) => setTyped(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && ok && !busy && void run()}
            autoComplete="off"
            aria-label={t("confirm.type", { name })}
          />
        </label>
      )}
      {error && (
        <p role="alert" className="mt-3 rounded-lg bg-danger/10 px-3 py-2 text-sm text-danger-text">
          {error}
        </p>
      )}
    </Dialog>
  );
}

function Logs({ team, app, dep }: { team: string; app: string; dep: Deployment }) {
  const { t } = useApp();
  const live = dep.status === "running" || dep.status === "queued";
  const q = useData(() => data<DeploymentDetail>("deployment", team, app, dep.id), [team, app, dep.id], live ? { every: 3000 } : {});
  if (q.loading && !q.data) return <Spinner />;
  const lines = (q.data?.logs ?? []).filter((l) => l.event === "log" && l.line);
  return (
    <div className="ltr selectable mt-2 max-h-64 overflow-auto rounded-lg bg-[#0b1020] p-3 font-mono text-xs leading-5 text-[#cbd5e1]">
      {lines.length === 0 ? <span className="text-[#8a97b8]">{t("deploy.noLogs")}</span> : lines.map((l) => <div key={l.seq} className={l.stream === "stderr" ? "text-[#f0a5b3]" : undefined}>{l.line}</div>)}
    </div>
  );
}

export function AppDetail() {
  const { t, locale, route, go, session, dir } = useApp();
  const { team, id } = route;
  const [action, setAction] = useState<AppAction | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [open, setOpen] = useState<string | null>(null);
  const q = useData(() => data<AppDetailT>("app", team, id ?? ""), [team, id], { every: 10_000, onMonitor: true });
  const deps = useData(() => data<Deployment[]>("deployments", team, id ?? "", 15), [team, id], { every: 10_000 });
  const role = session?.teams.find((x) => x.slug === team)?.role;
  const operate = canOperate(role);
  const Back = dir === "rtl" ? ArrowRight : ArrowLeft;

  useEffect(() => {
    if (!notice) return;
    const h = setTimeout(() => setNotice(null), 5000);
    return () => clearTimeout(h);
  }, [notice]);

  if (q.loading && !q.data) return <Spinner />;
  if (!q.data) return q.error ? <ErrorNote error={q.error} onRetry={q.reload} /> : null;
  const a = q.data.app;
  const stopped = a.status === "stopped";
  const acts: AppAction[] = stopped ? ["start", "redeploy"] : ["redeploy", "restart", "stop"];

  return (
    <>
      <PageHeader
        back={
          <button onClick={() => go({ team, section: "apps" })} className="mb-2 inline-flex items-center gap-1 text-sm text-muted hover:text-fg">
            <Back className="size-4" />
            {t("app.back")}
          </button>
        }
        title={
          <span className="flex items-center gap-3">
            {a.title || a.name}
            <Badge health={appHealth(a)}>{t(`app.${a.status}` as never)}</Badge>
          </span>
        }
        sub={<span className="ltr">{a.name}</span>}
      />

      {notice && (
        <div role="status" className="mb-4 rounded-lg bg-ok/15 px-4 py-2.5 text-sm text-ok-text">
          {notice}
        </div>
      )}

      <Card className="mb-5 p-4">
        <div className="mb-3 text-sm font-semibold text-fg">{t("app.actions")}</div>
        <div className="flex flex-wrap gap-2">
          {acts.map((x) => {
            const Icon = ICON[x];
            return (
              <Button key={x} data-action={x} variant={x === "stop" ? "soft" : x === "redeploy" ? "primary" : "soft"} disabled={!operate} onClick={() => setAction(x)}>
                <Icon className="size-4" />
                {t(`action.${x}` as never)}
              </Button>
            );
          })}
        </div>
        {!operate && <p className="mt-3 text-xs text-muted">{t("app.viewOnlyRole", { role: role ? t(`role.${role}` as never) : "-" })}</p>}
      </Card>

      <div className="mb-5 grid gap-3 sm:grid-cols-2">
        <Card className="space-y-2 p-4 text-sm">
          <Row k={t("app.server")} v={a.server_name ?? "-"} />
          <Row k={t("app.branch")} v={a.branch ?? "-"} ltr />
          <Row k={t("app.repo")} v={a.repo_url ?? "-"} ltr />
          <Row k={t("app.liveRelease")} v={q.data.live_release ? `v${q.data.live_release.version}` : "-"} ltr />
        </Card>
        <Card className="space-y-2 p-4 text-sm">
          <div className="text-xs text-muted">{t("app.domains")}</div>
          {q.data.domains.length === 0 ? (
            <span className="text-muted">{t("common.none")}</span>
          ) : (
            q.data.domains.map((d) => (
              <div key={d.id} className="ltr truncate text-fg">
                {d.hostname}
              </div>
            ))
          )}
          {q.data.processes.length > 0 && (
            <>
              <div className="pt-1 text-xs text-muted">{t("app.processes")}</div>
              <div className="ltr text-fg">{q.data.processes.map((p) => p.name).join(", ")}</div>
            </>
          )}
        </Card>
      </div>

      <h2 className="mb-3 text-sm font-semibold text-fg">{t("app.deployments")}</h2>
      {!deps.data ? (
        <Spinner />
      ) : deps.data.length === 0 ? (
        <Empty>{t("app.noDeployments")}</Empty>
      ) : (
        <div className="space-y-2">
          {deps.data.map((d) => {
            const isOpen = open === d.id;
            const took = d.started_at && d.finished_at ? (Date.parse(d.finished_at) - Date.parse(d.started_at)) / 1000 : null;
            return (
              <Card key={d.id} className="px-4 py-3">
                <button className="flex w-full items-center gap-3 text-start" onClick={() => setOpen(isOpen ? null : d.id)} aria-expanded={isOpen}>
                  <span className="ltr w-10 shrink-0 text-sm font-medium text-fg">{t("deploy.number", { number: d.number })}</span>
                  <Badge health={deployHealth(d.status)}>{t(`deploy.${d.status}` as never)}</Badge>
                  <span className="min-w-0 flex-1 truncate text-sm text-body">
                    <span className="text-muted">{t(`deploy.kind.${d.kind}` as never)}</span>
                    {d.commit_message ? ` · ${d.commit_message}` : ""}
                  </span>
                  <span className="shrink-0 text-xs text-muted">
                    {took !== null && <span className="ltr me-2">{duration(took, locale)}</span>}
                    {relativeTime(d.created_at, locale)}
                  </span>
                  {isOpen ? <ChevronUp className="size-4 text-muted" /> : <ChevronDown className="size-4 text-muted" />}
                </button>
                {d.error && <p className="mt-1 text-xs text-danger-text">{d.error}</p>}
                {isOpen && <Logs team={team} app={a.id} dep={d} />}
              </Card>
            );
          })}
        </div>
      )}

      {action && (
        <ConfirmAction
          team={team}
          app={a.id}
          name={a.name}
          action={action}
          onClose={() => setAction(null)}
          onDone={() => {
            setNotice(t("action.queued", { action: t(`action.${action}` as never) }));
            setAction(null);
            q.reload();
            deps.reload();
          }}
        />
      )}
    </>
  );
}

function Row({ k, v, ltr }: { k: string; v: string; ltr?: boolean }) {
  return (
    <div className="flex items-baseline justify-between gap-3">
      <span className="shrink-0 text-xs text-muted">{k}</span>
      <span className={`min-w-0 truncate text-fg ${ltr ? "ltr" : ""}`}>{v}</span>
    </div>
  );
}
