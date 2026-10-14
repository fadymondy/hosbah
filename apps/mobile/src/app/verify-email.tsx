import { useLocalSearchParams, useRouter } from "expo-router";
import { useEffect, useRef, useState } from "react";
import { View } from "react-native";

import { useApi } from "@/lib/api";
import { errorKey } from "@/lib/errors";
import { usePrefs } from "@/lib/prefs";
import { AuthScreen, FormError } from "@/ui/auth-screen";
import { Button } from "@/ui/button";
import { Field } from "@/ui/field";
import { AppText } from "@/ui/text";
import { space } from "@/ui/theme";

/** The 6-digit code mailed at sign-up (or on a sign-in to an unconfirmed account) activates the account and signs in. */
export default function VerifyEmail() {
  const { t, locale } = usePrefs();
  const router = useRouter();
  const { client, verifyEmail } = useApi();
  const params = useLocalSearchParams<{ email?: string; resend?: string }>();
  const email = params.email ?? "";

  const [code, setCode] = useState("");
  const [busy, setBusy] = useState<"verify" | "resend" | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [note, setNote] = useState<string | null>(null);

  const resend = async () => {
    setBusy("resend");
    setError(null);
    try {
      await client.resendVerification(email, locale);
      setNote(t("verify.resent"));
    } catch (e) {
      setError(t(errorKey(e)));
    } finally {
      setBusy(null);
    }
  };

  // Arrived from a sign-in that was refused as unconfirmed: the earlier code may be long gone.
  const resentOnce = useRef(false);
  useEffect(() => {
    if (params.resend === "1" && email && !resentOnce.current) {
      resentOnce.current = true;
      void resend();
    }
  }, [params.resend, email]);

  const submit = async () => {
    setBusy("verify");
    setError(null);
    try {
      const r = await verifyEmail(email, code);
      // Two-factor already on: finish on the sign-in screen with the password and the code.
      if (r.twoFactor) router.replace("/sign-in");
    } catch (e) {
      setError(t(errorKey(e)));
    } finally {
      setBusy(null);
    }
  };

  return (
    <AuthScreen title={t("verify.title")} testID="verify-title">
      <View style={{ gap: space.md }}>
        <AppText>{t("verify.body", { email })}</AppText>
        <Field
          label={t("verify.code")}
          ltr
          value={code}
          onChangeText={setCode}
          keyboardType="number-pad"
          autoComplete="one-time-code"
          textContentType="oneTimeCode"
          maxLength={6}
          onSubmitEditing={() => void submit()}
          testID="verify-code"
        />
        <FormError message={error} testID="verify-error" />
        {note ? <AppText variant="caption">{note}</AppText> : null}
        <Button label={t("verify.submit")} onPress={() => void submit()} loading={busy === "verify"} disabled={!!busy || code.trim().length < 6} testID="verify-submit" />
        <Button kind="secondary" label={t("verify.resend")} onPress={() => void resend()} loading={busy === "resend"} disabled={!!busy || !email} testID="verify-resend" />
        <Button kind="ghost" label={t("login.back")} onPress={() => router.replace("/sign-in")} testID="verify-back" />
      </View>
    </AuthScreen>
  );
}
