import { appHealth, overallStatus } from "@hosbah/client";
import { useRouter } from "expo-router";
import { View } from "react-native";

import { usePrefs } from "@/lib/prefs";
import { useApps, useCertificates, useCurrentTeam, useDataServices, useServers } from "@/lib/queries";
import { Card } from "@/ui/card";
import { Screen } from "@/ui/screen";
import { ServerCard } from "@/ui/server-card";
import { QueryBoundary } from "@/ui/states";
import { AppText } from "@/ui/text";
import { space } from "@/ui/theme";
import { TeamPicker } from "@/ui/team-picker";
import { NoTeam, useConsoleHost } from "@/ui/no-team";

function Stat({ value, label, tone, testID }: { value: number; label: string; tone?: "okText" | "warnText" | "dangerText"; testID?: string }) {
  return (
    <Card style={{ flex: 1, padding: space.md, alignItems: "center" }} testID={testID}>
      <AppText variant="title" {...(tone ? { tone } : {})}>
        {value}
      </AppText>
      <AppText variant="caption" style={{ textAlign: "center" }}>
        {label}
      </AppText>
    </Card>
  );
}

export default function Overview() {
  const { t } = usePrefs();
  const router = useRouter();
  const { team, loading: teamLoading, error: teamError } = useCurrentTeam();
  const servers = useServers();
  const apps = useApps();
  const certs = useCertificates();
  const services = useDataServices();
  const host = useConsoleHost();

  const refresh = () => {
    void servers.refetch();
    void apps.refetch();
    void certs.refetch();
    void services.refetch();
  };

  const appList = apps.data ?? [];
  const up = appList.filter((a) => appHealth(a) === "ok").length;
  const down = appList.filter((a) => appHealth(a) === "down").length;
  const attention = appList.length - up - down;
  const overall = overallStatus({
    servers: servers.data?.servers ?? [],
    apps: appList,
    certificates: certs.data ?? [],
    services: services.data ?? [],
  });
  const overallTone = overall === "green" ? "okText" : overall === "amber" ? "warnText" : "dangerText";

  return (
    <Screen
      title={t("overview.title")}
      right={<TeamPicker />}
      refreshing={servers.isRefetching || apps.isRefetching}
      onRefresh={refresh}
      testID="screen-overview"
    >
      {!teamLoading && !teamError && !team ? <NoTeam /> : null}
      <QueryBoundary
        loading={teamLoading || servers.isLoading}
        error={teamError ?? servers.error}
        hasData={!!servers.data}
        onRetry={refresh}
      >
        {team && servers.data ? (
          <>
            <Card>
              <AppText variant="heading" tone={overallTone} testID="overall-status">
                {t(`overall.${overall}`)}
              </AppText>
            </Card>
            <View style={{ flexDirection: "row", gap: space.sm }}>
              <Stat value={up} label={t("overview.appsUp")} tone="okText" testID="stat-up" />
              <Stat value={attention} label={t("overview.appsAttention")} tone="warnText" testID="stat-warn" />
              <Stat value={down} label={t("overview.appsDown")} tone="dangerText" testID="stat-down" />
            </View>
            <AppText variant="heading">{t("overview.servers")}</AppText>
            {servers.data.servers.length === 0 ? (
              <AppText tone="muted" testID="no-servers">
                {`${t("overview.noServers")}. ${t("overview.noServersHint", { console: host })}`}
              </AppText>
            ) : (
              servers.data.servers.map((s) => <ServerCard key={s.id} server={s} onPress={() => router.push({ pathname: "/servers/[id]", params: { id: s.id } })} />)
            )}
          </>
        ) : null}
      </QueryBoundary>
    </Screen>
  );
}
