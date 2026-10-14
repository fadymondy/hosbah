import { useLocalSearchParams, useRouter } from "expo-router";
import { useState } from "react";
import { View } from "react-native";

import { useApi } from "@/lib/api";
import { passwordErrorKey } from "@/lib/errors";
import { usePrefs } from "@/lib/prefs";
import { AuthScreen, FormError } from "@/ui/auth-screen";
import { Button } from "@/ui/button";
import { Field } from "@/ui/field";
import { AppText } from "@/ui/text";
import { space } from "@/ui/theme";

/** Forgot password: the address gets a 6-digit code, which sets a new password (and signs every session out). */
export default function ForgotPassword() {
  const { t, locale } = usePrefs();
  const router = useRouter();
  const { client } = useApi();
  const params = useLocalSearchParams<{ email?: string }>();

  const [step, setStep] = useState<"email" | "code" | "done">("email");
  const [email, setEmail] = useState(params.email ?? "");
  const [code, setCode] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const run = async (fn: () => Promise<void>) => {
    setBusy(true);
    setError(null);
    try {
      await fn();
    } catch (e) {
      setError(t(passwordErrorKey(e)));
    } finally {
      setBusy(false);
    }
  };

  const send = () =>
    run(async () => {
      await client.forgotPassword(email.trim(), locale);
      setStep("code");
    });

  const reset = () =>
    run(async () => {
      await client.resetPassword(email.trim(), code.trim(), password);
      setStep("done");
    });

  const back = () => (router.canGoBack() ? router.back() : router.replace("/sign-in"));

  return (
    <AuthScreen title={t("forgot.title")} testID="forgot-title">
      <View style={{ gap: space.md }}>
        {step === "email" ? (
          <>
            <AppText>{t("forgot.body")}</AppText>
            <Field label={t("login.email")} ltr value={email} onChangeText={setEmail} autoCapitalize="none" autoCorrect={false} keyboardType="email-address" autoComplete="email" onSubmitEditing={() => void send()} testID="forgot-email" />
            <FormError message={error} testID="forgot-error" />
            <Button label={t("forgot.send")} onPress={() => void send()} loading={busy} disabled={!email.trim()} testID="forgot-send" />
          </>
        ) : step === "code" ? (
          <>
            <AppText>{t("forgot.sent", { email: email.trim() })}</AppText>
            <Field label={t("forgot.code")} ltr value={code} onChangeText={setCode} keyboardType="number-pad" autoComplete="one-time-code" textContentType="oneTimeCode" maxLength={6} testID="forgot-code" />
            <Field label={t("forgot.newPassword")} ltr value={password} onChangeText={setPassword} secureTextEntry autoCapitalize="none" autoComplete="new-password" textContentType="newPassword" onSubmitEditing={() => void reset()} testID="forgot-password" />
            <AppText variant="caption">{t("signup.passwordHint")}</AppText>
            <FormError message={error} testID="forgot-error" />
            <Button label={t("forgot.submit")} onPress={() => void reset()} loading={busy} disabled={code.trim().length < 6 || password.length < 8} testID="forgot-submit" />
            <Button kind="secondary" label={t("verify.resend")} onPress={() => void send()} disabled={busy} testID="forgot-resend" />
          </>
        ) : (
          <AppText tone="okText" testID="forgot-done">
            {t("forgot.done")}
          </AppText>
        )}
        <Button kind="ghost" label={t("login.back")} onPress={back} testID="forgot-back" />
      </View>
    </AuthScreen>
  );
}
