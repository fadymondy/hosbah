import { TextInput, View, type TextInputProps } from "react-native";

import { fontFor, useColors, usePrefs } from "@/lib/prefs";
import { AppText } from "@/ui/text";
import { radius, space } from "@/ui/theme";

interface Props extends Omit<TextInputProps, "style"> {
  label: string;
  /** Emails, URLs and codes are left-to-right even in an Arabic UI. */
  ltr?: boolean;
}

export function Field({ label, ltr, ...input }: Props) {
  const c = useColors();
  const { locale } = usePrefs();
  return (
    <View style={{ gap: space.xs }}>
      <AppText variant="label">{label}</AppText>
      <TextInput
        accessibilityLabel={label}
        placeholderTextColor={c.muted}
        selectionColor={c.brand}
        {...input}
        style={{
          minHeight: 48,
          borderRadius: radius.md,
          borderWidth: 1,
          borderColor: c.line,
          backgroundColor: c.card,
          color: c.fg,
          paddingHorizontal: space.md,
          fontSize: 15,
          fontFamily: fontFor(ltr ? "en" : locale, "regular"),
          ...(ltr ? { textAlign: "left" as const, writingDirection: "ltr" as const } : {}),
        }}
      />
    </View>
  );
}
