import { Pressable, View } from "react-native";

import { AppText } from "@/ui/text";
import { radius, space, useStyles } from "@/ui/theme";

interface Props<T extends string> {
  value: T;
  options: { value: T; label: string; testID?: string }[];
  onChange: (v: T) => void;
}

export function Segmented<T extends string>({ value, options, onChange }: Props<T>) {
  const s = useStyles((c) => ({
    wrap: { flexDirection: "row", backgroundColor: c.soft, borderRadius: radius.md, padding: 3, gap: 3 },
    item: { flex: 1, minHeight: 38, alignItems: "center", justifyContent: "center", borderRadius: radius.md - 3, paddingHorizontal: space.sm },
    on: { backgroundColor: c.card, borderWidth: 1, borderColor: c.line },
  }));
  return (
    <View style={s.wrap} accessibilityRole="tablist">
      {options.map((o) => {
        const on = o.value === value;
        return (
          <Pressable
            key={o.value}
            testID={o.testID}
            accessibilityRole="tab"
            accessibilityState={{ selected: on }}
            onPress={() => onChange(o.value)}
            style={[s.item, on && s.on]}
          >
            <AppText variant="label" tone={on ? "fg" : "muted"}>
              {o.label}
            </AppText>
          </Pressable>
        );
      })}
    </View>
  );
}
