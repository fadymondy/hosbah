import { useLocalSearchParams } from "expo-router";
import { View } from "react-native";

import { useColors, usePrefs } from "@/lib/prefs";
import { useDeployment } from "@/lib/queries";
import { deployHealth } from "@/ui/app-bits";
import { Card } from "@/ui/card";
import { Screen } from "@/ui/screen";
import { QueryBoundary } from "@/ui/states";
import { StatusPill } from "@/ui/status";
import { AppText } from "@/ui/text";
import { radius, space } from "@/ui/theme";

export default function DeploymentLogs() {
  const { id, did } = useLocalSearchParams<{ id: string; did: string }>();
  const { t } = usePrefs();
  const c = useColors();
  const q = useDeployment(id, did);
  const d = q.data?.deployment;
  const live = d?.status === "queued" || d?.status === "running";
  const lines = (q.data?.logs ?? []).filter((l) => l.event === "log" && l.line !== undefined);

  return (
    <Screen
      back
      title={d ? `#${d.number}` : t("deploy.logs")}
      subtitle={d?.commit_message ?? undefined}
      refreshing={q.isRefetching}
      onRefresh={() => void q.refetch()}
      testID="screen-deployment-logs"
    >
      <QueryBoundary loading={q.isLoading} error={q.error} hasData={!!q.data} onRetry={() => void q.refetch()}>
        {d ? (
          <>
            <View style={{ flexDirection: "row", gap: space.sm, alignItems: "center" }}>
              <StatusPill health={deployHealth(d)} label={t(`deploy.${d.status}`)} />
              {live ? <AppText variant="caption">{t("deploy.live")}</AppText> : null}
            </View>
            {d.error ? (
              <AppText variant="caption" tone="dangerText">
                {d.error}
              </AppText>
            ) : null}
            <Card style={{ padding: space.md, borderRadius: radius.md, backgroundColor: c.elevated }} testID="log-view">
              {lines.length === 0 ? (
                <AppText variant="caption">{t("deploy.noLogs")}</AppText>
              ) : (
                lines.map((l) => (
                  <AppText
                    key={l.seq}
                    variant="mono"
                    tone={l.stream === "stderr" ? "dangerText" : l.stream === "system" ? "brand" : "body"}
                    style={{ writingDirection: "ltr", textAlign: "left" }}
                  >
                    {l.line}
                  </AppText>
                ))
              )}
            </Card>
          </>
        ) : null}
      </QueryBoundary>
    </Screen>
  );
}
