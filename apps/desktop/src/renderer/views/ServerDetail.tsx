import { ArrowLeft, ArrowRight } from "lucide-react";
import { appHealth, duration, formatBytes, relativeTime, usagePercent, type App, type MetricsSeries, type Server } from "@hosbah/client";
import { Badge, Card, Empty, ErrorNote, Meter, PageHeader, Spinner } from "../components/ui";
import { useApp } from "../lib/app";
import { data } from "../lib/bridge";
import { useData } from "../lib/useData";
import { serverBadge } from "./Overview";

function Spark({ values, label }: { values: number[]; label: string }) {
  const w = 220;
  const h = 48;
  const pts = values.length < 2 ? [] : values.map((v, i) => `${((i / (values.length - 1)) * w).toFixed(1)},${(h - (Math.min(100, Math.max(0, v)) / 100) * h).toFixed(1)}`);
  return (
    <div>
      <div className="mb-1 text-xs text-muted">{label}</div>
      <svg viewBox={`0 0 ${w} ${h}`} className="ltr h-12 w-full" role="img" aria-label={label} preserveAspectRatio="none">
        <polyline points={pts.join(" ")} fill="none" stroke="var(--action)" strokeWidth="2" strokeLinejoin="round" vectorEffect="non-scaling-stroke" />
      </svg>
    </div>
  );
}

export function ServerDetail() {
  const { t, locale, route, go, dir } = useApp();
  const { team, id } = route;
  const q = useData(() => data<Server>("server", team, id ?? ""), [team, id], { every: 15_000 });
  const apps = useData(() => data<App[]>("apps", team), [team, id], { every: 30_000 });
  const series = useData(() => data<MetricsSeries>("serverMetrics", team, id ?? "", "1h"), [team, id], { every: 60_000 });
  const Back = dir === "rtl" ? ArrowRight : ArrowLeft;

  if (q.loading && !q.data) return <Spinner />;
  if (!q.data) return q.error ? <ErrorNote error={q.error} onRetry={q.reload} /> : null;
  const s = q.data;
  const b = serverBadge(s, t);
  const m = s.metrics;
  const mine = (apps.data ?? []).filter((a) => a.server_id === s.id);
  const pts = series.data?.points ?? [];

  return (
    <>
      <PageHeader
        back={
          <button onClick={() => go({ team, section: "overview" })} className="mb-2 inline-flex items-center gap-1 text-sm text-muted hover:text-fg">
            <Back className="size-4" />
            {t("server.back")}
          </button>
        }
        title={
          <span className="flex items-center gap-3">
            {s.name}
            <Badge health={b.health}>{b.label}</Badge>
          </span>
        }
        sub={s.last_seen_at ? t("server.lastSeen", { when: relativeTime(s.last_seen_at, locale) }) : undefined}
      />

      {m && (
        <Card className="mb-5 grid gap-5 p-4 sm:grid-cols-3">
          <Meter label={t("server.cpu")} value={m.cpu_percent} />
          <Meter label={t("server.memory")} value={usagePercent(m.mem_used_bytes, m.mem_total_bytes)} sub={`${formatBytes(m.mem_used_bytes)} / ${formatBytes(m.mem_total_bytes)}`} />
          <Meter label={t("server.disk")} value={usagePercent(m.disk_used_bytes, m.disk_total_bytes)} sub={`${formatBytes(m.disk_used_bytes)} / ${formatBytes(m.disk_total_bytes)}`} />
        </Card>
      )}

      {pts.length > 1 && (
        <Card className="mb-5 p-4">
          <div className="mb-2 text-sm font-semibold text-fg">{t("server.history")}</div>
          <div className="grid gap-5 sm:grid-cols-3">
            <Spark label={t("server.cpu")} values={pts.map((p) => p.cpu)} />
            <Spark label={t("server.memory")} values={pts.map((p) => usagePercent(p.mem_used, p.mem_total))} />
            <Spark label={t("server.disk")} values={pts.map((p) => usagePercent(p.disk_used, p.disk_total))} />
          </div>
        </Card>
      )}

      <Card className="mb-5 grid gap-2 p-4 text-sm sm:grid-cols-2">
        <KV k={t("server.ip")} v={s.ip_address ?? "-"} />
        <KV k={t("server.os")} v={[s.os, s.os_version, s.arch].filter(Boolean).join(" ") || "-"} />
        <KV k={t("server.agent")} v={s.agent_version ?? "-"} />
        <KV k={t("server.uptime")} v={m ? duration(m.uptime_seconds, locale) : "-"} />
      </Card>

      <h2 className="mb-3 text-sm font-semibold text-fg">{t("server.apps")}</h2>
      {mine.length === 0 ? (
        <Empty>{t("server.noApps")}</Empty>
      ) : (
        <div className="space-y-2">
          {mine.map((a) => (
            <button key={a.id} className="block w-full text-start" onClick={() => go({ team, section: "apps", id: a.id })}>
              <Card className="flex items-center justify-between px-4 py-3 hover:border-action/60">
                <span className="ltr text-fg">{a.name}</span>
                <Badge health={appHealth(a)}>{t(`app.${a.status}` as never)}</Badge>
              </Card>
            </button>
          ))}
        </div>
      )}
    </>
  );
}

function KV({ k, v }: { k: string; v: string }) {
  return (
    <div className="flex items-baseline justify-between gap-3">
      <span className="text-xs text-muted">{k}</span>
      <span className="ltr text-fg">{v}</span>
    </div>
  );
}
