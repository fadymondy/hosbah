import { ChevronDown } from "lucide-react-native";
import { useState } from "react";
import { Modal, Pressable, View } from "react-native";

import { useColors, useT } from "@/lib/prefs";
import { useCurrentTeam } from "@/lib/queries";
import { AppText } from "@/ui/text";
import { radius, space } from "@/ui/theme";

/** The team switcher: shows the current team and, with more than one, opens a sheet to choose. */
export function TeamPicker() {
  const { team, teams, select } = useCurrentTeam();
  const c = useColors();
  const t = useT();
  const [open, setOpen] = useState(false);
  if (!team) return null;
  const many = teams.length > 1;
  return (
    <>
      <Pressable
        testID="team-picker"
        accessibilityRole="button"
        accessibilityLabel={`${t("settings.team")}: ${team.name}`}
        disabled={!many}
        onPress={() => setOpen(true)}
        style={{ flexDirection: "row", alignItems: "center", gap: space.xs, paddingVertical: 6, paddingHorizontal: space.md, borderRadius: radius.pill, backgroundColor: c.soft, borderWidth: 1, borderColor: c.line }}
      >
        <AppText variant="label" numberOfLines={1}>
          {team.name}
        </AppText>
        {many ? <ChevronDown size={16} color={c.muted} /> : null}
      </Pressable>
      <Modal visible={open} transparent animationType="fade" onRequestClose={() => setOpen(false)} statusBarTranslucent>
        <Pressable style={{ flex: 1, backgroundColor: "rgba(0,0,0,0.5)", justifyContent: "flex-end" }} onPress={() => setOpen(false)}>
          <View style={{ backgroundColor: c.card, borderTopLeftRadius: radius.lg, borderTopRightRadius: radius.lg, padding: space.lg, gap: space.sm }}>
            <AppText variant="heading">{t("settings.team")}</AppText>
            {teams.map((x) => (
              <Pressable
                key={x.id}
                testID={`team-${x.slug}`}
                accessibilityRole="button"
                onPress={() => {
                  select(x.id);
                  setOpen(false);
                }}
                style={{ paddingVertical: space.md, flexDirection: "row", justifyContent: "space-between", alignItems: "center" }}
              >
                <AppText variant="label" tone={x.id === team.id ? "brand" : "fg"}>
                  {x.name}
                </AppText>
                <AppText variant="caption">{t(`role.${x.role}`)}</AppText>
              </Pressable>
            ))}
          </View>
        </Pressable>
      </Modal>
    </>
  );
}
