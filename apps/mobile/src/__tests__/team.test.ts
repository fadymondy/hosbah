import type { TeamSummary } from "@hosbah/client";

import { resolveTeam, teamState } from "@/lib/team";

const team = (id: string, role: TeamSummary["role"]) => ({ id, name: id, role }) as TeamSummary;
const teams = [team("a", "owner"), team("b", "viewer"), team("c", "deployer"), team("d", "admin")];

describe("team role gating", () => {
  it("falls back to the first team", () => {
    expect(resolveTeam(teams, "zzz")?.id).toBe("a");
    expect(resolveTeam([], null)).toBeNull();
  });
  it("viewers cannot operate; everyone else can", () => {
    expect(teamState(teams, "b").canOperate).toBe(false);
    expect(teamState(teams, "a").canOperate).toBe(true);
    expect(teamState(teams, "c").canOperate).toBe(true);
    expect(teamState(teams, "d").canOperate).toBe(true);
    expect(teamState([], null).canOperate).toBe(false);
  });
});
