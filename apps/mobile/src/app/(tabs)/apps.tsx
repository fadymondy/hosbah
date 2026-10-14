import { useRouter } from "expo-router";

import { useT } from "@/lib/prefs";
import { useApps, useCurrentTeam } from "@/lib/queries";
import { AppCard } from "@/ui/app-bits";
import { Screen } from "@/ui/screen";
import { QueryBoundary } from "@/ui/states";
import { TeamPicker } from "@/ui/team-picker";
import { NoTeam, useConsoleHost } from "@/ui/no-team";

export default function Apps() {
  const t = useT();
  const router = useRouter();
  const { team, loading, error } = useCurrentTeam();
  const apps = useApps();
  const host = useConsoleHost();
  return (
    <Screen title={t("tab.apps")} right={<TeamPicker />} refreshing={apps.isRefetching} onRefresh={() => void apps.refetch()} testID="screen-apps">
      {!loading && !error && !team ? <NoTeam /> : null}
      <QueryBoundary
        loading={loading || apps.isLoading}
        error={error ?? apps.error}
        hasData={!!apps.data}
        empty={apps.data?.length === 0}
        emptyText={`${t("apps.empty")}. ${t("apps.emptyHint", { console: host })}`}
        onRetry={() => void apps.refetch()}
      >
        {(apps.data ?? []).map((a) => (
          <AppCard key={a.id} app={a} onPress={() => router.push({ pathname: "/apps/[id]", params: { id: a.id } })} />
        ))}
      </QueryBoundary>
    </Screen>
  );
}
