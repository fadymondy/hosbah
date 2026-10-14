import { ApiError, type SignupMode } from "@hosbah/client";
import { useRouter } from "expo-router";
import { useEffect, useState } from "react";
import { View } from "react-native";

import { useApi } from "@/lib/api";
import { passwordErrorKey } from "@/lib/errors";
import { openLegal } from "@/lib/legal";
import { usePrefs } from "@/lib/prefs";
import { AuthScreen, FormError } from "@/ui/auth-screen";
import { Button } from "@/ui/button";
import { Field } from "@/ui/field";
import { AppText } from "@/ui/text";
import { space } from "@/ui/theme";

export default function SignUp() {
  const { t, locale } = usePrefs();
  const router = useRouter();
  const { client, signUp } = useApi();
  // Closed / invite-only servers say so up front instead of failing the form.
  const [signupMode, setSignupMode] = useState<SignupMode | null>(null);
  useEffect(() => {
    let live = true;
    void client.publicSettings().then((s) => live && setSignupMode(s.signup_mode), () => undefined);
    return () => {
      live = false;
    };
  }, [client]);

  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = async () => {
    setBusy(true);
    setError(null);
    try {
      const r = await signUp(email, password);
      if (r.verify !== undefined) router.replace({ pathname: "/verify-email", params: { email: r.verify } });
      else if (r.twoFactor) router.replace("/sign-in");
    } catch (e) {
      setError(e instanceof ApiError && e.status === 409 ? t("signup.taken") : t(passwordErrorKey(e)));
    } finally {
      setBusy(false);
    }
  };

  const closed = signupMode === "closed";
  return (
    <AuthScreen title={t("signup.title")} testID="sign-up-title">
      <View style={{ gap: space.md }}>
        {closed || signupMode === "invite_only" ? (
          <AppText tone="warnText" testID="sign-up-mode">
            {closed ? t("signup.closed") : t("signup.inviteOnly")}
          </AppText>
        ) : null}
        <Field label={t("login.email")} ltr value={email} onChangeText={setEmail} autoCapitalize="none" autoCorrect={false} keyboardType="email-address" autoComplete="email" textContentType="username" testID="sign-up-email" />
        <Field label={t("signup.password")} ltr value={password} onChangeText={setPassword} secureTextEntry autoCapitalize="none" autoComplete="new-password" textContentType="newPassword" onSubmitEditing={() => void submit()} testID="sign-up-password" />
        <AppText variant="caption">{t("signup.passwordHint")}</AppText>
        <FormError message={error} testID="sign-up-error" />
        <Button label={t("signup.submit")} onPress={() => void submit()} loading={busy} disabled={closed || !email.trim() || password.length < 8} testID="sign-up-submit" />
        <AppText variant="caption" style={{ textAlign: "center" }} testID="sign-up-agree">
          {t("signup.agreeBefore")}
          <AppText variant="caption" tone="brand" accessibilityRole="link" onPress={() => openLegal("terms", locale)} style={{ textDecorationLine: "underline" }} testID="sign-up-terms">
            {t("legal.terms")}
          </AppText>
          {t("signup.agreeAnd")}
          <AppText variant="caption" tone="brand" accessibilityRole="link" onPress={() => openLegal("privacy", locale)} style={{ textDecorationLine: "underline" }} testID="sign-up-privacy">
            {t("legal.privacy")}
          </AppText>
          {t("signup.agreeAfter")}
        </AppText>
        <Button kind="ghost" label={t("login.haveAccount")} onPress={() => (router.canGoBack() ? router.back() : router.replace("/sign-in"))} testID="sign-up-sign-in" />
      </View>
    </AuthScreen>
  );
}
