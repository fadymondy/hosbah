"use client";

import Link from "next/link";
import { useCallback, useEffect, useMemo, useState } from "react";
import { AppWindowIcon, RocketIcon } from "lucide-react";

import { PageBody } from "@/components/app-shell";
import { useErrorText } from "@/components/form-error";
import { CardListToolbar, useInfinite } from "@/components/lists/card-list";
import { DashboardAppCard, type LoadPoint } from "@/components/home/dashboard-cards";
import { AppCardSkeleton } from "@/components/home/skeletons";
import { ErrorBand } from "@/components/states";
import { Button } from "@/components/ui/button";
import { apiFetch } from "@/lib/api";
import { appActivity, isImported, type App } from "@/lib/apps";
import { useDocumentTitle, useI18n } from "@/lib/i18n";
import { useTeam } from "@/lib/team";

type StatusFilter = "" | "running" | "failed" | "deploying" | "stopped";
type Sort = "recent" | "name" | "status";

/** What state an app is in, in the shared vocabulary (an imported workload reports its own). */
export function appState(a: App): string {
  if (isImported(a)) return a.imported_state ?? "stopped";
  if (
    a.latest_deployment &&
    (a.latest_deployment.status === "queued" || a.latest_deployment.status === "running")
  )
    return "deploying";
  return a.status;
}

const ORDER: Record<string, number> = {
  failed: 0,
  missing: 0,
  deploying: 1,
  running: 2,
  new: 3,
  stopped: 4,
};

/**
 * Every app of the team, whichever server it runs on: status, primary domain, server, what is
 * deployed and when. Search, filters and sorting; a row opens the app. Refreshes every 30 s.
 */
export default function AppsPage() {
  const { t, locale } = useI18n();
  const { team } = useTeam();
  useDocumentTitle(`${t("nav.apps")} — ${team.name}`);
  const errorText = useErrorText();
  const base = `/${locale}/t/${team.slug}`;

  const [apps, setApps] = useState<App[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [q, setQ] = useState("");
  const [sort, setSort] = useState<Sort>("recent");
  const [chosen, setChosen] = useState<Record<string, string[]>>({});
  const [loads, setLoads] = useState<Record<string, LoadPoint[]>>({});

  const load = useCallback(async () => {
    try {
      const r = await apiFetch<{ apps: App[] }>(`/api/teams/${team.slug}/apps`);
      setApps(r.apps ?? []);
      setError(null);
    } catch (err) {
      setError(errorText(err));
      setApps((x) => x ?? []);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [team.slug]);

  useEffect(() => {
    void load();
    const id = setInterval(load, 30_000);
    return () => clearInterval(id);
  }, [load]);

  // Each app's container load, live like the dashboard: every 5 s while the tab is visible.
  useEffect(() => {
    const read = () =>
      apiFetch<{ apps: Record<string, LoadPoint[]> }>(`/api/teams/${team.slug}/apps/metrics`)
        .then((r) => setLoads(r.apps ?? {}))
        .catch(() => {});
    void read();
    const id = setInterval(() => {
      if (document.visibilityState === "visible") void read();
    }, 5_000);
    return () => clearInterval(id);
  }, [team.slug]);

  // ?q= (from a link) prefills the search box.
  useEffect(() => {
    const want = new URLSearchParams(window.location.search).get("q");
    if (want) setQ(want);
  }, []);

  const servers = useMemo(() => {
    const m = new Map<string, string>();
    for (const a of apps ?? []) m.set(a.server_id, a.server_name || a.server_id);
    return [...m.entries()].sort((x, y) => x[1].localeCompare(y[1]));
  }, [apps]);

  const is = (a: App, s: StatusFilter) =>
    !s || appState(a) === s || (s === "failed" && appState(a) === "missing");
  const count = (s: StatusFilter) => (apps ?? []).filter((a) => is(a, s)).length;

  const shown = useMemo(() => {
    const needle = q.trim().toLowerCase();
    const list = (apps ?? []).filter((a) => {
      const st = chosen.status ?? [];
      if (st.length && !st.some((x) => is(a, x as StatusFilter))) return false;
      const sv = chosen.server ?? [];
      if (sv.length && !sv.includes(a.server_id)) return false;
      if (!needle) return true;
      return [
        a.name,
        a.title,
        a.server_name,
        a.repo_url,
        a.branch,
        a.latest_deployment?.commit_message,
        ...(a.domains ?? []).map((d) => d.hostname),
      ]
        .filter(Boolean)
        .some((v) => (v as string).toLowerCase().includes(needle));
    });
    return list.sort((x, y) =>
      sort === "name"
        ? (x.title || x.name).localeCompare(y.title || y.name)
        : sort === "status"
          ? (ORDER[appState(x)] ?? 5) - (ORDER[appState(y)] ?? 5)
          : appActivity(y) - appActivity(x) || (x.title || x.name).localeCompare(y.title || y.name),
    );
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [apps, q, chosen, sort]);
  const { visible, more, sentinel } = useInfinite(shown, JSON.stringify([q, chosen, sort]));

  const deployNew = (
    <Button nativeButton={false} render={<Link href={`${base}/apps/new`} />}>
      <RocketIcon />
      {t("home2.deploy")}
    </Button>
  );

  return (
    <PageBody
      title={t("nav.apps")}
      description={apps ? t("apps2.lede", { n: apps.length, servers: servers.length }) : undefined}
      actions={deployNew}
      bleed
      divider={false}
    >
      <ErrorBand message={error} />
      {apps === null ? (
        <div className="grid gap-4 px-6 pb-6 sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4">
          {[0, 1, 2, 3].map((i) => (
            <AppCardSkeleton key={i} />
          ))}
        </div>
      ) : apps.length === 0 ? (
        <div className="flex flex-col items-start gap-3 px-6 py-10">
          <AppWindowIcon className="size-6 text-grid-muted" />
          <p className="text-base font-medium text-grid-fg">{t("apps2.emptyTitle")}</p>
          <p className="max-w-lg text-sm text-grid-muted">{t("apps2.emptyBody")}</p>
          <div className="flex flex-wrap gap-2">
            {deployNew}
            <Button
              variant="outline"
              nativeButton={false}
              render={<Link href={`${base}/templates`} />}
            >
              {t("home2.fromTemplate")}
            </Button>
          </div>
        </div>
      ) : (
        <div className="flex flex-col gap-4 px-6 pb-6">
          <CardListToolbar
            q={q}
            onQ={setQ}
            placeholder={t("lists.searchApps")}
            chosen={chosen}
            onChosen={setChosen}
            facets={[
              {
                id: "status",
                title: t("lists.status"),
                options: (["running", "failed", "deploying", "stopped"] as const).map((v) => ({
                  value: v,
                  label: t(`apps.statuses.${v}`),
                  count: count(v),
                })),
              },
              ...(servers.length > 1
                ? [
                    {
                      id: "server",
                      title: t("nav.servers"),
                      options: servers.map(([value, label]) => ({ value, label })),
                    },
                  ]
                : []),
            ]}
            sort={sort}
            onSort={(v) => setSort(v as Sort)}
            sorts={[
              ["recent", t("lists.sortActivity")],
              ["name", t("apps2.sortName")],
              ["status", t("apps2.sortStatus")],
            ]}
          />
          {shown.length === 0 ? (
            <p className="rounded-2xl border border-dashed border-border px-4 py-8 text-center text-body-sm text-muted-foreground">
              {t("apps2.noMatch")}
            </p>
          ) : (
            <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4">
              {visible.map((a) => (
                <DashboardAppCard key={a.id} app={a} base={base} load={loads[a.id]} />
              ))}
            </div>
          )}
          {more ? <div ref={sentinel} className="h-8" aria-hidden /> : null}
          <p className="text-caption text-muted-foreground">
            {t("lists.count", { shown: visible.length, total: apps.length })}
          </p>
        </div>
      )}
    </PageBody>
  );
}
