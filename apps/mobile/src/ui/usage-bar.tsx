import { WARN_PERCENT } from "@hosbah/client";
import { View } from "react-native";

import { useColors } from "@/lib/prefs";
import { AppText } from "@/ui/text";
import { radius, space } from "@/ui/theme";

/** CPU / memory / disk. Amber from 75 %, red from the shared warn threshold (90 %). */
export function UsageBar({ label, percent, detail, testID }: { label: string; percent: number; detail?: string; testID?: string }) {
  const c = useColors();
  const p = Math.max(0, Math.min(100, Math.round(percent)));
  const fill = p >= WARN_PERCENT ? c.danger : p >= 75 ? c.warn : c.brand;
  return (
    <View testID={testID} style={{ gap: 4 }} accessible accessibilityLabel={`${label} ${p}%${detail ? ", " + detail : ""}`}>
      <View style={{ flexDirection: "row", justifyContent: "space-between", gap: space.sm }}>
        <AppText variant="caption">{label}</AppText>
        <AppText variant="caption" tone={p >= WARN_PERCENT ? "dangerText" : "fg"}>
          {p}%{detail ? ` · ${detail}` : ""}
        </AppText>
      </View>
      <View style={{ height: 6, borderRadius: radius.pill, backgroundColor: c.soft, overflow: "hidden" }}>
        <View style={{ width: `${p}%`, height: 6, borderRadius: radius.pill, backgroundColor: fill }} />
      </View>
    </View>
  );
}
