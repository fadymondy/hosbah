import { relativeTime, type AppNotification } from "@hosbah/client";
import { View } from "react-native";

import { destinationFromPush, dispatchLink } from "@/lib/links";
import { useColors, usePrefs } from "@/lib/prefs";
import { useCurrentTeam, useMarkAllRead, useMarkRead, useNotifications } from "@/lib/queries";
import { Button } from "@/ui/button";
import { Card } from "@/ui/card";
import { Screen } from "@/ui/screen";
import { QueryBoundary } from "@/ui/states";
import { TeamPicker } from "@/ui/team-picker";
import { AppText } from "@/ui/text";
import { space } from "@/ui/theme";

function Row({ n, team, onRead }: { n: AppNotification; team: string; onRead: (id: string) => void }) {
  const { t, locale } = usePrefs();
  const c = useColors();
  const unread = !n.read_at;
  const open = () => {
    if (unread) onRead(n.id);
    const d = destinationFromPush({ team, subject_type: n.subject_type ?? undefined, subject_id: n.subject_id ?? undefined });
    if (d) dispatchLink(d);
  };
  return (
    <Card onPress={open} testID={`alert-${n.id}`} style={{ padding: space.md, borderColor: unread ? c.brand : c.line }}>
      <View style={{ flexDirection: "row", gap: space.md, alignItems: "flex-start" }}>
        <View style={{ width: 8, height: 8, borderRadius: 4, marginTop: 8, backgroundColor: unread ? c.brand : "transparent" }} />
        <View style={{ flex: 1, gap: 2 }}>
          <AppText variant="label" tone={unread ? "fg" : "body"}>
            {n.title}
          </AppText>
          {n.body ? (
            <AppText variant="caption" numberOfLines={2}>
              {n.body}
            </AppText>
          ) : null}
          <AppText variant="caption">{relativeTime(n.created_at, locale)}</AppText>
        </View>
        {unread ? (
          <AppText variant="caption" tone="brand" onPress={() => onRead(n.id)} accessibilityRole="button" accessibilityLabel={t("alerts.markRead")} testID={`mark-read-${n.id}`}>
            {t("alerts.markRead")}
          </AppText>
        ) : null}
      </View>
    </Card>
  );
}

export default function Alerts() {
  const { t } = usePrefs();
  const { team, loading, error } = useCurrentTeam();
  const q = useNotifications();
  const markRead = useMarkRead();
  const markAll = useMarkAllRead();
  const list = q.data?.notifications ?? [];
  const unread = q.data?.unread ?? 0;

  return (
    <Screen
      title={t("alerts.title")}
      subtitle={unread > 0 ? t("alerts.unread", { count: unread }) : undefined}
      right={<TeamPicker />}
      refreshing={q.isRefetching}
      onRefresh={() => void q.refetch()}
      testID="screen-alerts"
    >
      <QueryBoundary
        loading={loading || q.isLoading}
        error={error ?? q.error}
        hasData={!!q.data}
        empty={list.length === 0}
        emptyText={t("alerts.empty")}
        onRetry={() => void q.refetch()}
      >
        {unread > 0 ? <Button kind="secondary" label={t("alerts.markAllRead")} onPress={() => markAll.mutate()} testID="mark-all-read" /> : null}
        {team ? list.map((n) => <Row key={n.id} n={n} team={team.id} onRead={(id) => markRead.mutate(id)} />) : null}
      </QueryBoundary>
    </Screen>
  );
}
