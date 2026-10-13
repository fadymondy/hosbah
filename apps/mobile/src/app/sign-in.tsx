import { normalizeApiUrl } from "@hosbah/client";
import { useState } from "react";
import { Image, KeyboardAvoidingView, Platform, ScrollView, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { useApi } from "@/lib/api";
import { errorKey } from "@/lib/errors";
import { useColors, usePrefs, useT } from "@/lib/prefs";
import { Button } from "@/ui/button";
import { Field } from "@/ui/field";
import { AppText } from "@/ui/text";
import { space } from "@/ui/theme";

export default function SignIn() {
  const t = useT();
  const c = useColors();
  const { locale, setLocale } = usePrefs();
  const insets = useSafeAreaInsets();
  const { signIn, completeTwoFactor, serverUrl, setServerUrl } = useApi();

  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [server, setServer] = useState(serverUrl);
  const [challenge, setChallenge] = useState<string | null>(null);
  const [code, setCode] = useState("");
  const [recovery, setRecovery] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const run = async (fn: () => Promise<void>) => {
    setBusy(true);
    setError(null);
    try {
      await fn();
    } catch (e) {
      setError(t(errorKey(e)));
    } finally {
      setBusy(false);
    }
  };

  const submit = () =>
    run(async () => {
      let url: string;
      try {
        url = normalizeApiUrl(server);
      } catch {
        throw new Error("bad url");
      }
      if (url !== serverUrl) await setServerUrl(url);
      const r = await signIn(email, password);
      if (r.twoFactor) setChallenge(r.twoFactor);
    });

  const verify = () => run(() => completeTwoFactor(challenge ?? "", recovery ? { recovery_code: code.trim() } : { code: code.trim() }));

  return (
    <KeyboardAvoidingView style={{ flex: 1, backgroundColor: c.bg }} behavior={Platform.OS === "ios" ? "padding" : undefined}>
      <ScrollView
        contentContainerStyle={{ padding: space.xl, paddingTop: insets.top + space.xxl, paddingBottom: insets.bottom + space.xl, gap: space.lg }}
        keyboardShouldPersistTaps="handled"
      >
        <View style={{ alignItems: "center", gap: space.md }}>
          <Image source={require("../../assets/brand/hosbah-icon.png")} style={{ width: 72, height: 72, borderRadius: 18 }} accessibilityIgnoresInvertColors />
          <AppText variant="title" testID="sign-in-title">
            {t("login.title")}
          </AppText>
        </View>

        {challenge === null ? (
          <View style={{ gap: space.md }}>
            <Field label={t("login.email")} ltr value={email} onChangeText={setEmail} autoCapitalize="none" autoCorrect={false} keyboardType="email-address" autoComplete="email" textContentType="username" testID="sign-in-email" />
            <Field label={t("login.password")} ltr value={password} onChangeText={setPassword} secureTextEntry autoCapitalize="none" autoComplete="current-password" textContentType="password" onSubmitEditing={submit} testID="sign-in-password" />
            <Field label={t("settings.serverUrl")} ltr value={server} onChangeText={setServer} autoCapitalize="none" autoCorrect={false} keyboardType="url" testID="sign-in-server" />
            {error ? (
              <AppText tone="dangerText" testID="sign-in-error" accessibilityLiveRegion="polite">
                {error}
              </AppText>
            ) : null}
            <Button label={t("login.submit")} onPress={submit} loading={busy} disabled={!email.trim() || !password} testID="sign-in-submit" />
            <AppText variant="caption" style={{ textAlign: "center" }}>
              {t("login.socialNote")}
            </AppText>
          </View>
        ) : (
          <View style={{ gap: space.md }}>
            <AppText variant="heading">{t("login.2faTitle")}</AppText>
            <Field
              label={recovery ? t("login.2faRecovery") : t("login.2faCode")}
              ltr
              value={code}
              onChangeText={setCode}
              autoCapitalize="none"
              autoCorrect={false}
              keyboardType={recovery ? "default" : "number-pad"}
              autoComplete="one-time-code"
              onSubmitEditing={verify}
              testID="sign-in-code"
            />
            {error ? (
              <AppText tone="dangerText" testID="sign-in-error" accessibilityLiveRegion="polite">
                {error}
              </AppText>
            ) : null}
            <Button label={t("login.2faSubmit")} onPress={verify} loading={busy} disabled={!code.trim()} testID="sign-in-verify" />
            <Button
              kind="ghost"
              label={t("login.2faRecovery")}
              onPress={() => {
                setRecovery((v) => !v);
                setCode("");
              }}
            />
          </View>
        )}

        <Button
          kind="ghost"
          testID="sign-in-language"
          label={locale === "ar" ? t("settings.language.en") : t("settings.language.ar")}
          onPress={() => void setLocale(locale === "ar" ? "en" : "ar")}
        />
      </ScrollView>
    </KeyboardAvoidingView>
  );
}
