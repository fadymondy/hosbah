"use client";

import Link from "next/link";
import { useCallback, useEffect, useMemo, useState } from "react";
import { AppWindowIcon, ExternalLinkIcon, GitCommitHorizontalIcon, RocketIcon } from "lucide-react";

import { PageBody } from "@/components/app-shell";
import { useErrorText } from "@/components/form-error";
import { useTimeAgo } from "@/components/home/sparkline";
import { appTone, FilterPills, ListBar, SortSelect, StatusDot } from "@/components/lists/list-bar";
import { ServerMark } from "@/components/server-mark";
import { ErrorBand, LoadingRows } from "@/components/states";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@fadymondy/nasaq/web";
import { apiFetch } from "@/lib/api";
import { isImported, shortSha, type App } from "@/lib/apps";
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
  const ago = useTimeAgo(locale);
  const base = `/${locale}/t/${team.slug}`;

  const [apps, setApps] = useState<App[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [q, setQ] = useState("");
  const [status, setStatus] = useState<StatusFilter>("");
  const [server, setServer] = useState("");
  const [sort, setSort] = useState<Sort>("recent");

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
      if (!is(a, status)) return false;
      if (server && a.server_id !== server) return false;
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
    const lastAt = (a: App) => a.latest_deployment?.created_at ?? a.last_seen_at ?? a.created_at;
    return list.sort((x, y) =>
      sort === "name"
        ? (x.title || x.name).localeCompare(y.title || y.name)
        : sort === "status"
          ? (ORDER[appState(x)] ?? 5) - (ORDER[appState(y)] ?? 5)
          : lastAt(y).localeCompare(lastAt(x)),
    );
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [apps, q, status, server, sort]);

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
    >
      <ErrorBand message={error} />
      {apps === null ? (
        <LoadingRows rows={5} />
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
        <>
          <ListBar q={q} onQ={setQ} placeholder={t("lists.searchApps")}>
            <FilterPills
              label={t("lists.status")}
              value={status}
              onChange={setStatus}
              options={[
                ["", t("home.filterAll"), count("")],
                ["running", t("apps.statuses.running"), count("running")],
                ["failed", t("apps.statuses.failed"), count("failed")],
                ["deploying", t("apps.statuses.deploying"), count("deploying")],
                ["stopped", t("apps.statuses.stopped"), count("stopped")],
              ]}
            />
            {servers.length > 1 ? (
              <SortSelect
                label={t("nav.servers")}
                value={server}
                onChange={setServer}
                options={[["", t("apps2.allServers")], ...servers]}
              />
            ) : null}
            <SortSelect
              label={t("apps2.sort")}
              value={sort}
              onChange={setSort}
              options={[
                ["recent", t("apps2.sortRecent")],
                ["name", t("apps2.sortName")],
                ["status", t("apps2.sortStatus")],
              ]}
            />
          </ListBar>

          {shown.length === 0 ? (
            <p className="border-b border-line px-6 py-8 text-center text-sm text-grid-muted">
              {t("apps2.noMatch")}
            </p>
          ) : (
            <>
              {/* Wide screens: a table. */}
              <div className="hidden md:block">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead className="ps-6">{t("apps2.app")}</TableHead>
                      <TableHead>{t("lists.status")}</TableHead>
                      <TableHead>{t("nav.domains")}</TableHead>
                      <TableHead>{t("apps2.server")}</TableHead>
                      <TableHead>{t("apps2.commit")}</TableHead>
                      <TableHead className="pe-6 text-end">{t("apps2.lastDeploy")}</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {shown.map((a) => {
                      const href = `${base}/apps/${encodeURIComponent(a.name)}`;
                      const st = appState(a);
                      const d = a.latest_deployment;
                      const domain = a.domains?.[0];
                      return (
                        <TableRow key={a.id} className="group">
                          <TableCell className="ps-6">
                            <Link href={href} className="flex min-w-0 items-center gap-3">
                              <ServerMark
                                server={{
                                  color: a.color,
                                  icon: a.icon ?? "globe",
                                  logo_url: a.logo_url,
                                }}
                                size={8}
                              />
                              <span className="min-w-0">
                                <span
                                  dir="auto"
                                  className="block truncate font-medium text-grid-fg group-hover:underline"
                                >
                                  {a.title || a.name}
                                </span>
                                <span dir="ltr" className="block truncate text-xs text-grid-muted">
                                  {a.title
                                    ? a.name
                                    : isImported(a)
                                      ? t("apps2.imported")
                                      : a.branch || "—"}
                                </span>
                              </span>
                            </Link>
                          </TableCell>
                          <TableCell>
                            <StatusDot
                              tone={appTone(st)}
                              label={t(`apps.statuses.${st}`, undefined, st)}
                            />
                            {d?.status === "failed" && st !== "failed" ? (
                              <span className="block text-xs text-grid-danger">
                                {t("apps2.lastFailed")}
                              </span>
                            ) : null}
                          </TableCell>
                          <TableCell>
                            {domain ? (
                              <a
                                href={`https://${domain.hostname}`}
                                target="_blank"
                                rel="noreferrer"
                                dir="ltr"
                                className="inline-flex max-w-[16rem] items-center gap-1 text-sm text-grid-body hover:text-grid-fg hover:underline"
                              >
                                <span className="truncate">{domain.hostname}</span>
                                <ExternalLinkIcon className="size-3 shrink-0" />
                              </a>
                            ) : (
                              <span className="text-xs text-grid-muted">—</span>
                            )}
                            {(a.domains?.length ?? 0) > 1 ? (
                              <span className="ms-1 text-xs text-grid-muted">
                                +{(a.domains?.length ?? 1) - 1}
                              </span>
                            ) : null}
                          </TableCell>
                          <TableCell>
                            <Link
                              href={`${base}/servers/${a.server_id}`}
                              dir="ltr"
                              className="text-sm text-grid-body hover:text-grid-fg hover:underline"
                            >
                              {a.server_name || "—"}
                            </Link>
                          </TableCell>
                          <TableCell>
                            {d?.commit_sha ? (
                              <span className="flex max-w-[18rem] min-w-0 items-center gap-1.5 text-xs text-grid-muted">
                                <GitCommitHorizontalIcon className="size-3.5 shrink-0" />
                                <code dir="ltr">{shortSha(d.commit_sha)}</code>
                                <span dir="auto" className="truncate">
                                  {d.commit_message}
                                </span>
                              </span>
                            ) : (
                              <span className="text-xs text-grid-muted">—</span>
                            )}
                          </TableCell>
                          <TableCell className="pe-6 text-end text-xs text-grid-muted">
                            {d ? ago(d.created_at) : "—"}
                          </TableCell>
                        </TableRow>
                      );
                    })}
                  </TableBody>
                </Table>
              </div>

              {/* Small screens: status and the way in first. */}
              <ul className="divide-y divide-line border-b border-line md:hidden">
                {shown.map((a) => {
                  const st = appState(a);
                  const domain = a.domains?.[0];
                  return (
                    <li key={a.id}>
                      <Link
                        href={`${base}/apps/${encodeURIComponent(a.name)}`}
                        className="flex items-center gap-3 px-4 py-3"
                      >
                        <ServerMark
                          server={{ color: a.color, icon: a.icon ?? "globe", logo_url: a.logo_url }}
                          size={8}
                        />
                        <span className="min-w-0 flex-1">
                          <span dir="auto" className="block truncate font-medium text-grid-fg">
                            {a.title || a.name}
                          </span>
                          <span dir="ltr" className="block truncate text-xs text-grid-muted">
                            {domain?.hostname ?? a.server_name}
                          </span>
                        </span>
                        <StatusDot
                          tone={appTone(st)}
                          label={t(`apps.statuses.${st}`, undefined, st)}
                        />
                      </Link>
                    </li>
                  );
                })}
              </ul>
              <p className="px-6 py-3 text-xs text-grid-muted">
                {t("lists.count", { shown: shown.length, total: apps.length })}
              </p>
            </>
          )}
        </>
      )}
    </PageBody>
  );
}
