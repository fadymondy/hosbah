import { formatBytes, relativeTime, serverHealth, usagePercent, type Server } from "@hosbah/client";
import { View } from "react-native";

import { usePrefs } from "@/lib/prefs";
import { Card } from "@/ui/card";
import { StatusPill } from "@/ui/status";
import { AppText } from "@/ui/text";
import { space } from "@/ui/theme";
import { UsageBar } from "@/ui/usage-bar";

export function serverStatusLabel(s: Server, t: ReturnType<typeof usePrefs>["t"]): string {
  if (s.status !== "enrolled") return t(`server.${s.status}`);
  return s.online ? t("server.online") : t("server.offline");
}

/** One server: name, online state, and CPU / memory / disk bars when it is reporting. */
export function ServerCard({ server, onPress }: { server: Server; onPress?: () => void }) {
  const { t, locale } = usePrefs();
  const m = server.metrics;
  const live = server.online && !!m;
  return (
    <Card {...(onPress ? { onPress } : {})} testID={`server-${server.name}`} accessibilityLabel={server.name}>
      <View style={{ gap: space.md }}>
        <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between", gap: space.sm }}>
          <View style={{ flex: 1 }}>
            <AppText variant="heading" numberOfLines={1}>
              {server.name}
            </AppText>
            {server.ip_address ? (
              <AppText variant="mono" tone="muted">
                {server.ip_address}
              </AppText>
            ) : null}
          </View>
          <StatusPill health={serverHealth(server)} label={serverStatusLabel(server, t)} />
        </View>
        {live && m ? (
          <View style={{ gap: space.sm }}>
            <UsageBar label={t("server.cpu")} percent={m.cpu_percent} />
            <UsageBar
              label={t("server.memory")}
              percent={usagePercent(m.mem_used_bytes, m.mem_total_bytes)}
              detail={`${formatBytes(m.mem_used_bytes)} / ${formatBytes(m.mem_total_bytes)}`}
            />
            <UsageBar
              label={t("server.disk")}
              percent={usagePercent(m.disk_used_bytes, m.disk_total_bytes)}
              detail={`${formatBytes(m.disk_used_bytes)} / ${formatBytes(m.disk_total_bytes)}`}
            />
          </View>
        ) : server.last_seen_at ? (
          <AppText variant="caption">{t("server.lastSeen", { when: relativeTime(server.last_seen_at, locale) })}</AppText>
        ) : null}
      </View>
    </Card>
  );
}
