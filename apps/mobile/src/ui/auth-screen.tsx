import { Image, KeyboardAvoidingView, Platform, ScrollView, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import type { ReactNode } from "react";

import { useColors, usePrefs, useT } from "@/lib/prefs";
import { Button } from "@/ui/button";
import { AppText } from "@/ui/text";
import { space } from "@/ui/theme";

/** The signed-out screens' frame: logo, title, the form, and the language switch at the bottom. */
export function AuthScreen({ title, testID, children }: { title: string; testID?: string; children: ReactNode }) {
  const t = useT();
  const c = useColors();
  const { locale, setLocale } = usePrefs();
  const insets = useSafeAreaInsets();
  return (
    <KeyboardAvoidingView style={{ flex: 1, backgroundColor: c.bg }} behavior={Platform.OS === "ios" ? "padding" : undefined}>
      <ScrollView
        contentContainerStyle={{ padding: space.xl, paddingTop: insets.top + space.xxl, paddingBottom: insets.bottom + space.xl, gap: space.lg }}
        keyboardShouldPersistTaps="handled"
      >
        <View style={{ alignItems: "center", gap: space.md }}>
          <Image source={require("../../assets/brand/hosbah-icon.png")} style={{ width: 72, height: 72, borderRadius: 18 }} accessibilityIgnoresInvertColors />
          <AppText variant="title" testID={testID} style={{ textAlign: "center" }}>
            {title}
          </AppText>
        </View>
        {children}
        <Button
          kind="ghost"
          testID="auth-language"
          label={locale === "ar" ? t("settings.language.en") : t("settings.language.ar")}
          onPress={() => void setLocale(locale === "ar" ? "en" : "ar")}
        />
      </ScrollView>
    </KeyboardAvoidingView>
  );
}

export function FormError({ message, testID = "auth-error" }: { message: string | null; testID?: string }) {
  if (!message) return null;
  return (
    <AppText tone="dangerText" testID={testID} accessibilityLiveRegion="polite">
      {message}
    </AppText>
  );
}
