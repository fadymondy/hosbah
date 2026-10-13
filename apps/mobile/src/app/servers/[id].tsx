import { duration } from "@hosbah/client";
import { useLocalSearchParams, useRouter } from "expo-router";
import { View } from "react-native";

import { usePrefs } from "@/lib/prefs";
import { useApps, useServer } from "@/lib/queries";
import { AppCard } from "@/ui/app-bits";
import { Card } from "@/ui/card";
import { Screen } from "@/ui/screen";
import { ServerCard } from "@/ui/server-card";
import { QueryBoundary } from "@/ui/states";
import { AppText } from "@/ui/text";
import { space } from "@/ui/theme";

function Fact({ k, v }: { k: string; v: string }) {
  return (
    <View style={{ flexDirection: "row", justifyContent: "space-between", gap: space.md }}>
      <AppText variant="caption">{k}</AppText>
      <AppText variant="mono" tone="fg" style={{ flexShrink: 1 }}>
        {v}
      </AppText>
    </View>
  );
}

export default function ServerDetail() {
  const { id } = useLocalSearchParams<{ id: string }>();
  const { t, locale } = usePrefs();
  const router = useRouter();
  const q = useServer(id);
  const apps = useApps();
  const s = q.data;
  const hosted = (apps.data ?? []).filter((a) => a.server_id === id);
  return (
    <Screen back title={s?.name ?? t("overview.servers")} refreshing={q.isRefetching} onRefresh={() => void q.refetch()} testID="screen-server-detail">
      <QueryBoundary loading={q.isLoading} error={q.error} hasData={!!s} onRetry={() => void q.refetch()}>
        {s ? (
          <>
            <ServerCard server={s} />
            <Card>
              <View style={{ gap: space.sm }}>
                {s.os ? <Fact k="OS" v={`${s.os} ${s.os_version ?? ""}`.trim()} /> : null}
                {s.arch ? <Fact k="Arch" v={s.arch} /> : null}
                {s.hostname ? <Fact k="Host" v={s.hostname} /> : null}
                {s.agent_version ? <Fact k="Agent" v={s.agent_version} /> : null}
                {s.metrics ? <Fact k={t("server.uptime")} v={duration(s.metrics.uptime_seconds, locale)} /> : null}
              </View>
            </Card>
            {hosted.length > 0 ? <AppText variant="heading">{t("tab.apps")}</AppText> : null}
            {hosted.map((a) => (
              <AppCard key={a.id} app={a} onPress={() => router.push({ pathname: "/apps/[id]", params: { id: a.id } })} />
            ))}
          </>
        ) : null}
      </QueryBoundary>
    </Screen>
  );
}
