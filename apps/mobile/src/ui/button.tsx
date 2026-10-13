import { ActivityIndicator, Pressable, type StyleProp, type ViewStyle } from "react-native";

import { AppText } from "@/ui/text";
import { radius, space, useStyles } from "@/ui/theme";

interface Props {
  label: string;
  onPress: () => void;
  kind?: "primary" | "secondary" | "danger" | "ghost";
  icon?: React.ReactNode;
  loading?: boolean;
  disabled?: boolean;
  style?: StyleProp<ViewStyle>;
  testID?: string;
}

export function Button({ label, onPress, kind = "primary", icon, loading, disabled, style, testID }: Props) {
  const s = useStyles((c) => ({
    base: { minHeight: 46, borderRadius: radius.md, paddingHorizontal: space.lg, alignItems: "center", justifyContent: "center", flexDirection: "row", gap: space.sm },
    primary: { backgroundColor: c.action },
    secondary: { backgroundColor: c.soft, borderWidth: 1, borderColor: c.line },
    danger: { backgroundColor: c.danger },
    ghost: { backgroundColor: "transparent" },
    off: { opacity: 0.5 },
    pressed: { opacity: 0.85 },
  }));
  const tone = kind === "primary" || kind === "danger" ? "onAction" : kind === "ghost" ? "brand" : "fg";
  const off = disabled || loading;
  return (
    <Pressable
      testID={testID}
      accessibilityRole="button"
      accessibilityLabel={label}
      accessibilityState={{ disabled: !!off, busy: !!loading }}
      disabled={off}
      onPress={onPress}
      style={({ pressed }) => [s.base, s[kind], off && s.off, pressed && s.pressed, style]}
    >
      {loading ? <ActivityIndicator color={kind === "primary" || kind === "danger" ? "#fff" : undefined} /> : (icon ?? null)}
      <AppText variant="label" tone={tone}>
        {label}
      </AppText>
    </Pressable>
  );
}
