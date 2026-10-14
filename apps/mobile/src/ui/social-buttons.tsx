import type { AuthProvider } from "@hosbah/client";
import * as AppleAuthentication from "expo-apple-authentication";
import { Platform, View } from "react-native";
import Svg, { Path } from "react-native-svg";

import { useColors, usePrefs, useT } from "@/lib/prefs";
import { Button } from "@/ui/button";
import { AppText } from "@/ui/text";
import { radius, space } from "@/ui/theme";

/** The providers' own marks, drawn inline (no images). GitHub's and Apple's follow the text colour. */
export function ProviderMark({ id, color }: { id: string; color: string }) {
  if (id === "google") {
    return (
      <Svg width={18} height={18} viewBox="0 0 48 48" accessibilityElementsHidden importantForAccessibility="no">
        <Path fill="#EA4335" d="M24 9.5c3.54 0 6.71 1.22 9.21 3.6l6.85-6.85C35.9 2.38 30.47 0 24 0 14.62 0 6.51 5.38 2.56 13.22l7.98 6.19C12.43 13.72 17.74 9.5 24 9.5z" />
        <Path fill="#4285F4" d="M46.98 24.55c0-1.57-.15-3.09-.38-4.55H24v9.02h12.94c-.58 2.96-2.26 5.48-4.78 7.18l7.73 6c4.51-4.18 7.09-10.36 7.09-17.65z" />
        <Path fill="#FBBC05" d="M10.53 28.59c-.48-1.45-.76-2.99-.76-4.59s.27-3.14.76-4.59l-7.98-6.19C.92 16.46 0 20.12 0 24c0 3.88.92 7.54 2.56 10.78l7.97-6.19z" />
        <Path fill="#34A853" d="M24 48c6.48 0 11.93-2.13 15.89-5.81l-7.73-6c-2.15 1.45-4.92 2.3-8.16 2.3-6.26 0-11.57-4.22-13.47-9.91l-7.98 6.19C6.51 42.62 14.62 48 24 48z" />
      </Svg>
    );
  }
  if (id === "github") {
    return (
      <Svg width={18} height={18} viewBox="0 0 16 16" accessibilityElementsHidden importantForAccessibility="no">
        <Path
          fill={color}
          d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0 0 16 8c0-4.42-3.58-8-8-8z"
        />
      </Svg>
    );
  }
  if (id === "apple") {
    return (
      <Svg width={18} height={18} viewBox="0 0 384 512" accessibilityElementsHidden importantForAccessibility="no">
        <Path
          fill={color}
          d="M318.7 268.7c-.2-36.7 16.4-64.4 50-84.8-18.8-26.9-47.2-41.7-84.7-44.6-35.5-2.8-74.3 20.7-88.5 20.7-15 0-49.4-19.7-76.4-19.7C63.3 141.2 4 184.8 4 273.5q0 39.3 14.4 81.2c12.8 36.7 59 126.7 107.2 125.2 25.2-.6 43-17.9 75.8-17.9 31.8 0 48.3 17.9 76.4 17.9 48.6-.7 90.4-82.5 102.6-119.3-65.2-30.7-61.7-90-61.7-91.9zm-56.6-164.2c27.3-32.4 24.8-61.9 24-72.5-24.1 1.4-52 16.4-67.9 34.9-17.5 19.8-27.8 44.3-25.6 71.9 26.1 2 49.9-11.4 69.5-34.3z"
        />
      </Svg>
    );
  }
  return null;
}

interface Props {
  providers: AuthProvider[];
  busy: string | null;
  onPress: (p: AuthProvider) => void;
  /** The label pattern, e.g. login.continueWith or delete.withProvider. */
  labelKey?: "login.continueWith" | "delete.withProvider";
}

/**
 * Apple first and the same size as the others (App Store guideline 4.8). On iOS it is Apple's own
 * AppleAuthenticationButton (Human Interface Guidelines: black on light, white on dark); on Android, where
 * Apple signs in through the browser, a button of the same shape with Apple's mark.
 */
export function SocialButtons({ providers, busy, onPress, labelKey = "login.continueWith" }: Props) {
  const t = useT();
  const c = useColors();
  const { theme } = usePrefs();
  return (
    <View style={{ gap: space.sm }} testID="social-buttons">
      {providers.map((p) =>
        p.id === "apple" && Platform.OS === "ios" && labelKey === "login.continueWith" ? (
          <AppleAuthentication.AppleAuthenticationButton
            key={p.id}
            testID="social-apple"
            buttonType={AppleAuthentication.AppleAuthenticationButtonType.CONTINUE}
            buttonStyle={theme === "dark" ? AppleAuthentication.AppleAuthenticationButtonStyle.WHITE : AppleAuthentication.AppleAuthenticationButtonStyle.BLACK}
            cornerRadius={radius.md}
            style={{ width: "100%", height: 46, opacity: busy ? 0.5 : 1 }}
            onPress={() => {
              if (!busy) onPress(p);
            }}
          />
        ) : (
          <Button
            key={p.id}
            testID={`social-${p.id}`}
            kind="secondary"
            label={t(labelKey, { provider: p.name })}
            icon={<ProviderMark id={p.id} color={c.fg} />}
            loading={busy === p.id}
            disabled={!!busy && busy !== p.id}
            onPress={() => onPress(p)}
          />
        ),
      )}
    </View>
  );
}

export function OrRule() {
  const t = useT();
  const c = useColors();
  return (
    <View style={{ flexDirection: "row", alignItems: "center", gap: space.md }} accessibilityRole="none">
      <View style={{ flex: 1, height: 1, backgroundColor: c.line }} />
      <AppText variant="caption">{t("login.or")}</AppText>
      <View style={{ flex: 1, height: 1, backgroundColor: c.line }} />
    </View>
  );
}
