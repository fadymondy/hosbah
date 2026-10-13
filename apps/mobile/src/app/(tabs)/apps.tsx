import { useRouter } from "expo-router";

import { useT } from "@/lib/prefs";
import { useApps, useCurrentTeam } from "@/lib/queries";
import { AppCard } from "@/ui/app-bits";
import { Screen } from "@/ui/screen";
import { QueryBoundary } from "@/ui/states";
import { TeamPicker } from "@/ui/team-picker";

export default function Apps() {
  const t = useT();
  const router = useRouter();
  const { loading, error } = useCurrentTeam();
  const apps = useApps();
  return (
    <Screen title={t("tab.apps")} right={<TeamPicker />} refreshing={apps.isRefetching} onRefresh={() => void apps.refetch()} testID="screen-apps">
      <QueryBoundary
        loading={loading || apps.isLoading}
        error={error ?? apps.error}
        hasData={!!apps.data}
        empty={apps.data?.length === 0}
        emptyText={t("apps.empty")}
        onRetry={() => void apps.refetch()}
      >
        {(apps.data ?? []).map((a) => (
          <AppCard key={a.id} app={a} onPress={() => router.push({ pathname: "/apps/[id]", params: { id: a.id } })} />
        ))}
      </QueryBoundary>
    </Screen>
  );
}
