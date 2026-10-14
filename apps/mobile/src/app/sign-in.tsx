import { ApiError, normalizeApiUrl } from "@hosbah/client";
import { useRouter } from "expo-router";
import { useState } from "react";
import { View } from "react-native";

import { useApi, type SignInResult } from "@/lib/api";
import { useAuthOptions } from "@/lib/auth-options";
import { errorKey } from "@/lib/errors";
import { useT } from "@/lib/prefs";
import { AuthScreen, FormError } from "@/ui/auth-screen";
import { Button } from "@/ui/button";
import { Field } from "@/ui/field";
import { ProviderButtons } from "@/ui/provider-buttons";
import { AppText } from "@/ui/text";
import { space } from "@/ui/theme";

export default function SignIn() {
  const t = useT();
  const router = useRouter();
  const { signIn, completeTwoFactor, signInWithProvider, signInWithApple, serverUrl, setServerUrl } = useApi();
  const options = useAuthOptions();

  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [server, setServer] = useState(serverUrl);
  const [challenge, setChallenge] = useState<string | null>(null);
  const [code, setCode] = useState("");
  const [recovery, setRecovery] = useState(false);
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const run = async (what: string, fn: () => Promise<void>) => {
    setBusy(what);
    setError(null);
    try {
      await fn();
    } catch (e) {
      // A password sign-in to an account that never confirmed its address: go and confirm it.
      if (e instanceof ApiError && e.code === "email_unverified") {
        router.push({ pathname: "/verify-email", params: { email: email.trim(), resend: "1" } });
        return;
      }
      setError(t(errorKey(e)));
    } finally {
      setBusy(null);
    }
  };

  /** Applies a changed server address before any sign-in, so it goes to the server shown. */
  const applyServer = async () => {
    let url: string;
    try {
      url = normalizeApiUrl(server);
    } catch {
      throw new Error("bad url");
    }
    if (url !== serverUrl) await setServerUrl(url);
  };

  const after = (r: SignInResult | null) => {
    if (r?.twoFactor) setChallenge(r.twoFactor);
  };

  const submit = () =>
    run("password", async () => {
      await applyServer();
      after(await signIn(email, password));
    });

  const provider = (id: string) =>
    run(id, async () => {
      await applyServer();
      after(id === "apple-native" ? await signInWithApple() : await signInWithProvider(id));
    });

  const verify = () => run("2fa", () => completeTwoFactor(challenge ?? "", recovery ? { recovery_code: code.trim() } : { code: code.trim() }));

  return (
    <AuthScreen title={t("login.title")} testID="sign-in-title">
      {challenge === null ? (
        <View style={{ gap: space.md }}>
          <ProviderButtons options={options} busy={busy} onPress={(id) => void provider(id)} />
          <Field label={t("login.email")} ltr value={email} onChangeText={setEmail} autoCapitalize="none" autoCorrect={false} keyboardType="email-address" autoComplete="email" textContentType="username" testID="sign-in-email" />
          <Field label={t("login.password")} ltr value={password} onChangeText={setPassword} secureTextEntry autoCapitalize="none" autoComplete="current-password" textContentType="password" onSubmitEditing={submit} testID="sign-in-password" />
          <Field label={t("settings.serverUrl")} ltr value={server} onChangeText={setServer} autoCapitalize="none" autoCorrect={false} keyboardType="url" testID="sign-in-server" />
          <FormError message={error} testID="sign-in-error" />
          <Button label={t("login.submit")} onPress={submit} loading={busy === "password"} disabled={!!busy || !email.trim() || !password} testID="sign-in-submit" />
          <Button kind="ghost" label={t("login.forgot")} onPress={() => router.push({ pathname: "/forgot-password", params: { email: email.trim() } })} testID="sign-in-forgot" />
          {options.signupMode !== "closed" ? <Button kind="secondary" label={t("login.noAccount")} onPress={() => router.push("/sign-up")} testID="sign-in-sign-up" /> : null}
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
          <FormError message={error} testID="sign-in-error" />
          <Button label={t("login.2faSubmit")} onPress={verify} loading={busy === "2fa"} disabled={!code.trim()} testID="sign-in-verify" />
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
    </AuthScreen>
  );
}
