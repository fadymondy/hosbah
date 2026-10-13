import { canOperate, type Role, type TeamSummary } from "@hosbah/client";
import { useEffect, useSyncExternalStore } from "react";

import { getStored, KEYS, setStored } from "@/lib/storage";

// The selected team is a tiny external store: the push handler and deep links must be able to switch it
// from outside React, and every screen reads it.

let selected: string | null = null;
const listeners = new Set<() => void>();
let hydrated: Promise<void> | null = null;

function emit() {
  for (const l of listeners) l();
}

export function getSelectedTeam(): string | null {
  return selected;
}

export function selectTeam(id: string | null): void {
  if (selected === id) return;
  selected = id;
  emit();
  if (id) void setStored(KEYS.team, id);
}

/** Loads the stored selection once (before the first screen needs it). */
export function hydrateTeam(): Promise<void> {
  hydrated ??= getStored(KEYS.team).then((v) => {
    if (v && !selected) {
      selected = v;
      emit();
    }
  });
  return hydrated;
}

function subscribe(l: () => void): () => void {
  listeners.add(l);
  return () => listeners.delete(l);
}

export interface CurrentTeam {
  team: TeamSummary | null;
  teams: TeamSummary[];
  role: Role | null;
  /** Redeploy / restart / start / stop are shown only when true (viewers get read-only screens). */
  canOperate: boolean;
  select: (id: string) => void;
}

/** Resolves the selected team against the teams the user belongs to, falling back to the first. */
export function resolveTeam(teams: TeamSummary[], id: string | null): TeamSummary | null {
  return teams.find((t) => t.id === id) ?? teams[0] ?? null;
}

export function useSelectedTeamId(): string | null {
  useEffect(() => {
    void hydrateTeam();
  }, []);
  return useSyncExternalStore(subscribe, getSelectedTeam, getSelectedTeam);
}

export function teamState(teams: TeamSummary[], id: string | null): Omit<CurrentTeam, "select"> {
  const team = resolveTeam(teams, id);
  return { team, teams, role: team?.role ?? null, canOperate: canOperate(team?.role) };
}
