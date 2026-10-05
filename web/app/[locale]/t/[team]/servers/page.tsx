"use client";

import Link from "next/link";
import { useCallback, useEffect, useMemo, useState } from "react";
import { PlusIcon, ServerIcon } from "lucide-react";

import { PageBody } from "@/components/app-shell";
import { DashboardServerCard } from "@/components/home/dashboard-cards";
import { type Spark, type TunnelSummary } from "@/components/home/server-card";
import { ServerCardSkeleton } from "@/components/home/skeletons";
import { Button } from "@/components/ui/button";
import { CardListToolbar, useInfinite } from "@/components/lists/card-list";
import { EmptyState } from "@fadymondy/nasaq/web";
import { apiFetch } from "@/lib/api";
import { serverActivity, type App } from "@/lib/apps";
import { useDocumentTitle, useI18n } from "@/lib/i18n";
import type { Server } from "@/lib/servers";
import { useTeam } from "@/lib/team";

type TunnelRow = { server_id: string; kind: string; status: string; address: string | null };

/** Calls an endpoint that newer APIs have; null (not an error) when this API does not have it yet. */
async function optional<T>(path: string): Promise<T | null> {
  try {
    return await apiFetch<T>(path);
  } catch {
    return null;
  }
}

/**
 * The team's servers: every server as a card (status, address, tunnel, hardware, live usage and
 * the last hour as charts, what shipped to it last), with search and filters. Refreshes every 30 s
 * while open.
 */
export default function ServersPage() {
  const { t, locale } = useI18n();
  const { team, manage } = useTeam();
  useDocumentTitle(`${t("nav.servers")} — ${team.name}`);
  const base = `/${locale}/t/${team.slug}`;
  const api = `/api/teams/${team.slug}`;

  const [servers, setServers] = useState<Server[] | null>(null);
  const [apps, setApps] = useState<App[]>([]);
  const [tunnels, setTunnels] = useState<TunnelRow[]>([]);
  const [spark, setSpark] = useState<Record<string, Spark>>({});

  const load = useCallback(async () => {
    const [s, a, tn, sp] = await Promise.all([
      optional<{ servers: Server[] }>(`${api}/servers`),
      optional<{ apps: App[] }>(`${api}/apps`),
      optional<{ tunnels: TunnelRow[] }>(`${api}/tunnels`),
      optional<{ servers: Record<string, Spark> }>(`${api}/servers/metrics?range=1h`),
    ]);
    setServers(s?.servers ?? []);
    setApps(a?.apps ?? []);
    setTunnels(tn?.tunnels ?? []);
    setSpark(sp?.servers ?? {});
  }, [api]);

  useEffect(() => {
    void load();
    const id = setInterval(() => {
      if (document.visibilityState === "visible") void load();
    }, 5_000);
    return () => clearInterval(id);
  }, [load]);

  // Per server: its tunnel (an active one first) and the most recent deploy of any app on it.
  const tunnelOf = useMemo(() => {
    const m: Record<string, TunnelSummary> = {};
    for (const tn of tunnels) {
      const cur = m[tn.server_id];
      if (!cur || (tn.status === "active" && cur.status !== "active")) m[tn.server_id] = tn;
    }
    return m;
  }, [tunnels]);

  // Nodes and their enrolled guest servers (MH-574 A1): a guest with a parent that is also one of
  // the team's servers nests under its node instead of appearing as an unrelated row.
  const byId = useMemo(() => {
    const m: Record<string, Server> = {};
    for (const s of servers ?? []) m[s.id] = s;
    return m;
  }, [servers]);
  const childrenOf = useMemo(() => {
    const m: Record<string, Server[]> = {};
    for (const s of servers ?? []) {
      if (s.parent_server_id && byId[s.parent_server_id]) {
        (m[s.parent_server_id] ??= []).push(s);
      }
    }
    return m;
  }, [servers, byId]);
  const isNestedGuest = (s: Server) => Boolean(s.parent_server_id && byId[s.parent_server_id]);

  // Hosbah has no server power control yet: the card's power menu says so instead of failing silently.
  const noPower = async () => ({ error: t("home3.noPower") });

  // Search, facets (state, provider, tunnel) and sort; the list grows 20 at a time as you scroll.
  const [q, setQ] = useState("");
  const [chosen, setChosen] = useState<Record<string, string[]>>({});
  const [sort, setSort] = useState("recent");
  const matches = useCallback(
    (s: Server) => {
      const needle = q.trim().toLowerCase();
      const state = chosen.state ?? [];
      if (state.length && !state.includes(s.online ? "online" : "offline")) return false;
      const provider = chosen.provider ?? [];
      if (provider.length && !provider.includes(s.provider === "proxmox" ? "proxmox" : "byo"))
        return false;
      const tunnel = chosen.tunnel ?? [];
      if (tunnel.length && !tunnel.includes(tunnelOf[s.id] ? "yes" : "no")) return false;
      if (!needle) return true;
      return [s.name, s.ip_address, s.hostname, s.os, s.facts?.os_pretty].some((v) =>
        v?.toLowerCase().includes(needle),
      );
    },
    [q, chosen, tunnelOf],
  );
  const top = useMemo(() => (servers ?? []).filter((s) => !isNestedGuest(s)), [servers, byId]);
  const filtered = useMemo(() => {
    // Top-level rows only: a nested guest never shows up as its own row, only under its node — but
    // its node still shows if only the guest matches the filters.
    const active = serverActivity(servers ?? [], apps);
    const seen = (s: Server) => active.get(s.id) ?? 0;
    return top
      .filter((s) => matches(s) || (childrenOf[s.id] ?? []).some(matches))
      .sort((a, b) =>
        sort === "name"
          ? a.name.localeCompare(b.name)
          : sort === "online"
            ? Number(b.online) - Number(a.online) || a.name.localeCompare(b.name)
            : seen(b) - seen(a) || a.name.localeCompare(b.name),
      );
  }, [top, matches, childrenOf, sort, apps]);
  const { visible, more, sentinel } = useInfinite(filtered, JSON.stringify([q, chosen, sort]));
  const count = (f: (s: Server) => boolean) => top.filter(f).length;

  return (
    <PageBody
      title={t("nav.servers")}
      description={
        servers
          ? t("home.summary", {
              servers: servers.filter((s) => !isNestedGuest(s)).length,
              online: servers.filter((s) => !isNestedGuest(s) && s.online).length,
              apps: apps.length,
            })
          : undefined
      }
      actions={
        <>
          {manage ? (
            <Button nativeButton={false} render={<Link href={`${base}/servers/new`} />}>
              <ServerIcon />
              {t("servers.add")}
            </Button>
          ) : null}
        </>
      }
      bleed
      divider={false}
    >
      {servers === null ? (
        <div className="grid items-start gap-4 px-6 pb-6 lg:grid-cols-2">
          {[0, 1, 2, 3].map((i) => (
            <ServerCardSkeleton key={i} />
          ))}
        </div>
      ) : servers.length === 0 ? (
        <EmptyState
          className="border-y"
          icon={ServerIcon}
          title={t("overview.serversTitle")}
          description={manage ? t("overview.serversHint") : t("common.readOnly")}
          actions={
            manage ? (
              <Button nativeButton={false} render={<Link href={`${base}/servers/new`} />}>
                <PlusIcon />
                {t("servers.add")}
              </Button>
            ) : null
          }
        />
      ) : (
        <div className="flex flex-col gap-4 px-6 pb-6">
          <CardListToolbar
            q={q}
            onQ={setQ}
            placeholder={t("home.search")}
            chosen={chosen}
            onChosen={setChosen}
            facets={[
              {
                id: "state",
                title: t("lists.status"),
                options: [
                  { value: "online", label: t("home.online"), count: count((s) => s.online) },
                  { value: "offline", label: t("home.offline"), count: count((s) => !s.online) },
                ],
              },
              {
                id: "provider",
                title: t("lists.provider"),
                options: [
                  {
                    value: "proxmox",
                    label: "Proxmox",
                    count: count((s) => s.provider === "proxmox"),
                  },
                  {
                    value: "byo",
                    label: t("home.ownServers"),
                    count: count((s) => s.provider !== "proxmox"),
                  },
                ],
              },
              {
                id: "tunnel",
                title: t("lists.tunnel"),
                options: [
                  {
                    value: "yes",
                    label: t("home.withTunnel"),
                    count: count((s) => !!tunnelOf[s.id]),
                  },
                  { value: "no", label: t("lists.noTunnel"), count: count((s) => !tunnelOf[s.id]) },
                ],
              },
            ]}
            sort={sort}
            onSort={setSort}
            sorts={[
              ["recent", t("lists.sortActivity")],
              ["name", t("apps2.sortName")],
              ["online", t("lists.sortOnline")],
            ]}
          />
          {filtered.length === 0 ? (
            <p className="rounded-2xl border border-dashed border-border px-4 py-8 text-center text-body-sm text-muted-foreground">
              {t("home.noMatch")}
            </p>
          ) : (
            <div className="grid items-start gap-4 lg:grid-cols-2">
              {visible.map((s) => (
                <DashboardServerCard
                  key={s.id}
                  server={s}
                  base={base}
                  cpu={spark[s.id]?.cpu}
                  onPower={noPower}
                />
              ))}
            </div>
          )}
          {more ? <div ref={sentinel} className="h-8" aria-hidden /> : null}
          <p className="text-caption text-muted-foreground">
            {t("lists.count", { shown: visible.length, total: top.length })}
          </p>
        </div>
      )}
    </PageBody>
  );
}
