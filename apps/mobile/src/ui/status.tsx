import type { Health } from "@hosbah/client";
import { View } from "react-native";

import { useColors } from "@/lib/prefs";
import { AppText } from "@/ui/text";
import { radius, space } from "@/ui/theme";

export function healthColors(c: ReturnType<typeof useColors>, h: Health) {
  switch (h) {
    case "ok":
      return { dot: c.ok, text: c.okText };
    case "warn":
      return { dot: c.warn, text: c.warnText };
    default:
      return { dot: c.danger, text: c.dangerText };
  }
}

export function StatusDot({ health, size = 10 }: { health: Health; size?: number }) {
  const c = useColors();
  return <View style={{ width: size, height: size, borderRadius: size / 2, backgroundColor: healthColors(c, health).dot }} />;
}

/** A coloured dot and a label; the label carries the meaning, so colour is never the only signal. */
export function StatusPill({ health, label, testID }: { health: Health; label: string; testID?: string }) {
  const c = useColors();
  const col = healthColors(c, health);
  return (
    <View
      testID={testID}
      style={{
        flexDirection: "row",
        alignItems: "center",
        gap: space.xs + 2,
        alignSelf: "flex-start",
        paddingVertical: 3,
        paddingHorizontal: space.sm + 2,
        borderRadius: radius.pill,
        backgroundColor: c.soft,
        borderWidth: 1,
        borderColor: c.line,
      }}
    >
      <StatusDot health={health} size={8} />
      <AppText variant="caption" style={{ color: col.text }}>
        {label}
      </AppText>
    </View>
  );
}
