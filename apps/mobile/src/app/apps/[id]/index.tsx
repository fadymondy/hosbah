import { appHealth, relativeTime } from "@hosbah/client";
import { useLocalSearchParams, useRouter } from "expo-router";
import { View } from "react-native";

import { usePrefs } from "@/lib/prefs";
import { useApp, useDeployments } from "@/lib/queries";
import { AppActions } from "@/ui/app-actions";
import { appStatusLabel, appTitle, DeploymentRow } from "@/ui/app-bits";
import { Card } from "@/ui/card";
import { Screen } from "@/ui/screen";
import { QueryBoundary } from "@/ui/states";
import { StatusPill } from "@/ui/status";
import { AppText } from "@/ui/text";
import { space } from "@/ui/theme";

export default function AppDetailScreen() {
  const { id } = useLocalSearchParams<{ id: string }>();
  const { t, locale } = usePrefs();
  const router = useRouter();
  const app = useApp(id);
  const deployments = useDeployments(id);
  const a = app.data?.app;
  const refresh = () => {
    void app.refetch();
    void deployments.refetch();
  };

  return (
    <Screen
      back
      title={a ? appTitle(a) : t("tab.apps")}
      refreshing={app.isRefetching || deployments.isRefetching}
      onRefresh={refresh}
      testID="screen-app-detail"
    >
      <QueryBoundary loading={app.isLoading} error={app.error} hasData={!!a} onRetry={refresh}>
        {a ? (
          <>
            <Card>
              <View style={{ gap: space.md }}>
                <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
                  <StatusPill testID="app-status" health={appHealth(a)} label={appStatusLabel(a, t)} />
                  {a.server_name ? <AppText variant="caption">{a.server_name}</AppText> : null}
                </View>
                {app.data?.domains.map((d) => (
                  <AppText key={d.id} variant="mono" tone="brand">
                    {d.hostname}
                  </AppText>
                ))}
                {app.data?.live_release ? (
                  <AppText variant="caption">
                    {`${t("app.liveRelease")}: v${app.data.live_release.version}${
                      app.data.live_release.activated_at ? ` · ${relativeTime(app.data.live_release.activated_at, locale)}` : ""
                    }`}
                  </AppText>
                ) : null}
              </View>
            </Card>

            <AppActions appId={a.id} appName={appTitle(a)} status={a.status} />

            <AppText variant="heading">{t("app.deployments")}</AppText>
            {deployments.data && deployments.data.length === 0 ? <AppText tone="muted">{t("app.noDeployments")}</AppText> : null}
            {(deployments.data ?? []).map((d) => (
              <DeploymentRow key={d.id} d={d} onPress={() => router.push({ pathname: "/apps/[id]/deployments/[did]", params: { id: a.id, did: d.id } })} />
            ))}
          </>
        ) : null}
      </QueryBoundary>
    </Screen>
  );
}
