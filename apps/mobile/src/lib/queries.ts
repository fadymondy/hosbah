import type { EventName, NotificationPrefsResponse, NotificationsResponse, PowerAction } from "@hosbah/client";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { useApi } from "@/lib/api";
import { selectTeam, teamState, useSelectedTeamId, type CurrentTeam } from "@/lib/team";

export const FAST = 15_000;

export function useMe() {
  const { client } = useApi();
  return useQuery({ queryKey: ["me"], queryFn: () => client.me() });
}

/** The selected team with its role; `team` is null until /api/me answered. */
export function useCurrentTeam(): CurrentTeam & { loading: boolean; error: unknown } {
  const me = useMe();
  const id = useSelectedTeamId();
  const state = teamState(me.data?.teams ?? [], id);
  return { ...state, select: selectTeam, loading: me.isLoading, error: me.error };
}

function useTeamId(): string {
  return useCurrentTeam().team?.id ?? "";
}

export function useServers() {
  const { client } = useApi();
  const team = useTeamId();
  return useQuery({ queryKey: ["servers", team], queryFn: () => client.servers(team), enabled: !!team, refetchInterval: FAST });
}

export function useServer(id: string) {
  const { client } = useApi();
  const team = useTeamId();
  return useQuery({ queryKey: ["server", team, id], queryFn: () => client.server(team, id), enabled: !!team && !!id, refetchInterval: FAST });
}

export function useApps() {
  const { client } = useApi();
  const team = useTeamId();
  return useQuery({ queryKey: ["apps", team], queryFn: () => client.apps(team), enabled: !!team, refetchInterval: FAST });
}

export function useApp(id: string) {
  const { client } = useApi();
  const team = useTeamId();
  return useQuery({ queryKey: ["app", team, id], queryFn: () => client.app(team, id), enabled: !!team && !!id, refetchInterval: FAST });
}

export function useDeployments(appId: string) {
  const { client } = useApi();
  const team = useTeamId();
  return useQuery({ queryKey: ["deployments", team, appId], queryFn: () => client.deployments(team, appId, 15), enabled: !!team && !!appId, refetchInterval: FAST });
}

/** Polls every 3 s while the deployment is queued or running, then stops. */
export function useDeployment(appId: string, deploymentId: string) {
  const { client } = useApi();
  const team = useTeamId();
  return useQuery({
    queryKey: ["deployment", team, appId, deploymentId],
    queryFn: () => client.deployment(team, appId, deploymentId),
    enabled: !!team && !!appId && !!deploymentId,
    refetchInterval: (query) => {
      const s = query.state.data?.deployment.status;
      return s === "queued" || s === "running" ? 3_000 : false;
    },
  });
}

export function useDomains() {
  const { client } = useApi();
  const team = useTeamId();
  return useQuery({ queryKey: ["domains", team], queryFn: () => client.domains(team), enabled: !!team });
}

export function useCertificates() {
  const { client } = useApi();
  const team = useTeamId();
  return useQuery({ queryKey: ["certificates", team], queryFn: () => client.certificates(team), enabled: !!team });
}

export function useDataServices() {
  const { client } = useApi();
  const team = useTeamId();
  return useQuery({ queryKey: ["data-services", team], queryFn: () => client.dataServices(team), enabled: !!team });
}

export function useNotifications() {
  const { client } = useApi();
  const team = useTeamId();
  return useQuery({ queryKey: ["notifications", team], queryFn: () => client.notifications(team), enabled: !!team, refetchInterval: FAST });
}

export function useMarkRead() {
  const { client } = useApi();
  const team = useTeamId();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => client.markRead(team, id),
    onMutate: (id) => {
      qc.setQueryData<NotificationsResponse>(["notifications", team], (old) =>
        old
          ? {
              notifications: old.notifications.map((n) => (n.id === id && !n.read_at ? { ...n, read_at: new Date().toISOString() } : n)),
              unread: Math.max(0, old.unread - (old.notifications.find((n) => n.id === id && !n.read_at) ? 1 : 0)),
            }
          : old,
      );
    },
    onSettled: () => qc.invalidateQueries({ queryKey: ["notifications", team] }),
  });
}

export function useMarkAllRead() {
  const { client } = useApi();
  const team = useTeamId();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => client.markAllRead(team),
    onMutate: () => {
      qc.setQueryData<NotificationsResponse>(["notifications", team], (old) =>
        old ? { unread: 0, notifications: old.notifications.map((n) => (n.read_at ? n : { ...n, read_at: new Date().toISOString() })) } : old,
      );
    },
    onSettled: () => qc.invalidateQueries({ queryKey: ["notifications", team] }),
  });
}

export type AppAction = "redeploy" | "restart" | PowerAction;

/** Redeploy / restart / start / stop. Callers must have confirmed (dialog + biometric) before mutating. */
export function useAppAction(appId: string) {
  const { client } = useApi();
  const team = useTeamId();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (action: AppAction) => {
      switch (action) {
        case "redeploy":
          return client.redeploy(team, appId);
        case "restart":
          return client.restart(team, appId);
        default:
          return client.power(team, appId, action);
      }
    },
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: ["apps", team] });
      void qc.invalidateQueries({ queryKey: ["app", team, appId] });
      void qc.invalidateQueries({ queryKey: ["deployments", team, appId] });
    },
  });
}

export function useNotificationPrefs() {
  const { client } = useApi();
  return useQuery({ queryKey: ["prefs"], queryFn: () => client.prefs() });
}

export function usePutPref() {
  const { client } = useApi();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (change: Partial<Record<EventName, boolean>>) => client.putPrefs(change),
    onMutate: (change) => {
      qc.setQueryData<NotificationPrefsResponse>(["prefs"], (old) => (old ? { ...old, events: { ...old.events, ...change } } : old));
    },
    onSuccess: (data) => qc.setQueryData(["prefs"], data),
    onError: () => qc.invalidateQueries({ queryKey: ["prefs"] }),
  });
}

export function useDevices() {
  const { client } = useApi();
  return useQuery({ queryKey: ["devices"], queryFn: () => client.devices() });
}

export function useDeleteDevice() {
  const { client } = useApi();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => client.deleteDevice(id),
    onSettled: () => qc.invalidateQueries({ queryKey: ["devices"] }),
  });
}
