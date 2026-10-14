import * as AppleAuthentication from "expo-apple-authentication";
import { View } from "react-native";

import type { AuthOptions } from "@/lib/auth-options";
import { usePrefs, useT } from "@/lib/prefs";
import { Button } from "@/ui/button";
import { AppText } from "@/ui/text";
import { radius, space } from "@/ui/theme";

/** "Continue with Apple / Google / GitHub", then an "or" rule above the email form. Nothing when none is configured. */
export function ProviderButtons({ options, busy, onPress }: { options: AuthOptions; busy: string | null; onPress: (id: string) => void }) {
  const t = useT();
  const { theme, colors } = usePrefs();
  if (options.buttons.length === 0) return null;
  return (
    <View style={{ gap: space.sm }}>
      {options.buttons.map((b) =>
        b.id === "apple-native" ? (
          // Apple's own button, as its guidelines require for the native flow (it follows the device language).
          <View key={b.id} pointerEvents={busy ? "none" : "auto"} style={{ opacity: busy ? 0.5 : 1 }}>
            <AppleAuthentication.AppleAuthenticationButton
              testID="sign-in-apple"
              buttonType={AppleAuthentication.AppleAuthenticationButtonType.CONTINUE}
              buttonStyle={theme === "dark" ? AppleAuthentication.AppleAuthenticationButtonStyle.WHITE : AppleAuthentication.AppleAuthenticationButtonStyle.BLACK}
              cornerRadius={radius.md}
              style={{ height: 46, width: "100%" }}
              onPress={() => onPress(b.id)}
            />
          </View>
        ) : (
          <Button
            key={b.id}
            kind="secondary"
            label={t("login.continueWith", { name: b.name })}
            loading={busy === b.id}
            disabled={!!busy}
            onPress={() => onPress(b.id)}
            testID={`sign-in-${b.id}`}
          />
        ),
      )}
      <View style={{ flexDirection: "row", alignItems: "center", gap: space.md, marginTop: space.sm }}>
        <View style={{ flex: 1, height: 1, backgroundColor: colors.line }} />
        <AppText variant="caption">{t("login.or")}</AppText>
        <View style={{ flex: 1, height: 1, backgroundColor: colors.line }} />
      </View>
    </View>
  );
}
