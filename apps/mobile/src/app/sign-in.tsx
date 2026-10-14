import { ApiError, normalizeApiUrl, type AuthProvider, type SignupMode } from "@hosbah/client";
import { useRouter } from "expo-router";
import { useEffect, useState } from "react";
import { Image, KeyboardAvoidingView, Platform, ScrollView, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { useApi } from "@/lib/api";
import { errorKey } from "@/lib/errors";
import { appleAvailable, SocialCancelled, socialButtons } from "@/lib/social";
import { useColors, usePrefs, useT } from "@/lib/prefs";
import { Button } from "@/ui/button";
import { Field } from "@/ui/field";
import { OrRule, SocialButtons } from "@/ui/social-buttons";
import { AppText } from "@/ui/text";
import { space } from "@/ui/theme";

export default function SignIn() {
  const t = useT();
  const c = useColors();
  const { locale, setLocale } = usePrefs();
  const insets = useSafeAreaInsets();
  const router = useRouter();
  const { client, signIn, signInWith, completeTwoFactor, serverUrl, setServerUrl } = useApi();

  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [server, setServer] = useState(serverUrl);
  const [challenge, setChallenge] = useState<string | null>(null);
  const [code, setCode] = useState("");
  const [recovery, setRecovery] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // The social buttons this server offers on this platform (GET /api/auth/providers); a server
  // without the credentials shows none.
  const [providers, setProviders] = useState<AuthProvider[]>([]);
  const [socialBusy, setSocialBusy] = useState<string | null>(null);
  // "Create an account" is hidden on a server that does not take sign-ups.
  const [signupMode, setSignupMode] = useState<SignupMode | null>(null);

  useEffect(() => {
    let live = true;
    void (async () => {
      const [list, apple] = await Promise.all([client.authProviders(), appleAvailable()]);
      if (live) setProviders(socialButtons(list, Platform.OS, apple));
    })();
    void client.publicSettings().then((s) => live && setSignupMode(s.signup_mode), () => undefined);
    return () => {
      live = false;
    };
  }, [client]);

  // The server field is committed when editing ends, so the buttons match the server they will use.
  const commitServer = () => {
    try {
      const url = normalizeApiUrl(server);
      if (url !== serverUrl) void setServerUrl(url);
    } catch {
      // submit() reports a bad address
    }
  };

  const social = async (p: AuthProvider) => {
    setSocialBusy(p.id);
    setError(null);
    try {
      const r = await signInWith(p);
      if (r.twoFactor) setChallenge(r.twoFactor);
    } catch (e) {
      if (!(e instanceof SocialCancelled)) setError(t(errorKey(e)));
    } finally {
      setSocialBusy(null);
    }
  };

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
      try {
        const r = await signIn(email, password);
        if (r.twoFactor) setChallenge(r.twoFactor);
      } catch (e) {
        // The account exists but its address was never confirmed: send a fresh code and ask for it.
        if (e instanceof ApiError && e.code === "email_unverified") {
          router.push({ pathname: "/verify-email", params: { email: email.trim(), resend: "1" } });
          return;
        }
        throw e;
      }
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
            {providers.length ? (
              <>
                <SocialButtons providers={providers} busy={socialBusy ?? (busy ? "password" : null)} onPress={(p) => void social(p)} />
                <OrRule />
              </>
            ) : null}
            <Field label={t("login.email")} ltr value={email} onChangeText={setEmail} autoCapitalize="none" autoCorrect={false} keyboardType="email-address" autoComplete="email" textContentType="username" testID="sign-in-email" />
            <Field label={t("login.password")} ltr value={password} onChangeText={setPassword} secureTextEntry autoCapitalize="none" autoComplete="current-password" textContentType="password" onSubmitEditing={submit} testID="sign-in-password" />
            <Field label={t("settings.serverUrl")} ltr value={server} onChangeText={setServer} autoCapitalize="none" autoCorrect={false} keyboardType="url" onEndEditing={commitServer} testID="sign-in-server" />
            {error ? (
              <AppText tone="dangerText" testID="sign-in-error" accessibilityLiveRegion="polite">
                {error}
              </AppText>
            ) : null}
            <Button label={t("login.submit")} onPress={submit} loading={busy} disabled={!email.trim() || !password || !!socialBusy} testID="sign-in-submit" />
            <Button kind="ghost" label={t("login.forgot")} onPress={() => router.push({ pathname: "/forgot-password", params: { email: email.trim() } })} testID="sign-in-forgot" />
            {signupMode !== "closed" ? <Button kind="secondary" label={t("login.noAccount")} onPress={() => router.push("/sign-up")} testID="sign-in-sign-up" /> : null}
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
