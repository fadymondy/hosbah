import type { AuthProvider, DeleteAccountProof } from "@hosbah/client";
import { useEffect, useState } from "react";
import { Alert, Platform, View } from "react-native";

import { useApi } from "@/lib/api";
import { errorKey } from "@/lib/errors";
import { usePrefs } from "@/lib/prefs";
import { appleAvailable, appleCredential, googleCredential, SocialCancelled, socialButtons } from "@/lib/social";
import { Button } from "@/ui/button";
import { Card } from "@/ui/card";
import { Field } from "@/ui/field";
import { Screen } from "@/ui/screen";
import { SocialButtons } from "@/ui/social-buttons";
import { AppText } from "@/ui/text";
import { space } from "@/ui/theme";
import { useToast } from "@/ui/toast";

interface Identities {
  identities: { provider: string }[];
  has_password: boolean;
}

/**
 * Settings → Delete account (App Store guideline 5.1.1(v)). DELETE /api/me needs one proof: the password,
 * a code mailed to the account's address, or a fresh native Google / Apple sign-in to an account linked
 * here (Apple "Hide My Email" addresses may not receive the server's mail). Server rules: teams only you
 * are in go with the account (they must have no servers left); a team with other members needs another
 * owner first.
 */
export default function DeleteAccountScreen() {
  const { t, locale } = usePrefs();
  const toast = useToast();
  const { client, deleteAccount } = useApi();
  const [info, setInfo] = useState<Identities | null>(null);
  const [email, setEmail] = useState("");
  const [providers, setProviders] = useState<AuthProvider[]>([]);
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [codeSent, setCodeSent] = useState(false);
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    void (async () => {
      try {
        const [ids, me, list, apple] = await Promise.all([
          client.request<Identities>("GET", "/api/me/identities"),
          client.me(),
          client.authProviders(),
          appleAvailable(),
        ]);
        setInfo(ids);
        setEmail(me.user.email);
        const linked = new Set(ids.identities.map((i) => i.provider));
        // Only providers this account is linked to, signed in natively (the proof must be fresh and local).
        setProviders(socialButtons(list, Platform.OS, apple).filter((p) => linked.has(p.id) && p.id !== "github" && !(p.id === "apple" && Platform.OS !== "ios")));
      } catch (e) {
        setError(t(errorKey(e)));
        setInfo({ identities: [], has_password: false });
      }
    })();
  }, [client, t]);

  const run = async (key: string, proof: () => Promise<DeleteAccountProof>) => {
    setBusy(key);
    setError(null);
    try {
      const p = await proof();
      await deleteAccount(p);
      toast(t("delete.done"));
    } catch (e) {
      if (!(e instanceof SocialCancelled)) setError(t(errorKey(e)));
    } finally {
      setBusy(null);
    }
  };

  // One more explicit "yes" before anything is sent.
  const confirm = (key: string, proof: () => Promise<DeleteAccountProof>) =>
    Alert.alert(t("delete.title"), t("delete.body"), [
      { text: t("confirm.cancel"), style: "cancel" },
      { text: t("delete.confirm"), style: "destructive", onPress: () => void run(key, proof) },
    ]);

  const sendCode = async () => {
    setBusy("send");
    setError(null);
    try {
      await client.requestDeleteCode(locale);
      setCodeSent(true);
    } catch (e) {
      setError(t(errorKey(e)));
    } finally {
      setBusy(null);
    }
  };

  const withProvider = (p: AuthProvider) =>
    confirm(p.id, async () =>
      p.id === "apple" ? { provider: "apple", ...(await appleCredential()) } : { provider: "google", ...(await googleCredential(p.client_id ?? "")) },
    );

  return (
    <Screen title={t("delete.title")} back testID="screen-delete-account">
      <Card style={{ padding: space.md }}>
        <AppText tone="dangerText">{t("delete.body")}</AppText>
      </Card>

      {error ? (
        <AppText tone="dangerText" testID="delete-error" accessibilityLiveRegion="polite">
          {error}
        </AppText>
      ) : null}

      {info?.has_password ? (
        <View style={{ gap: space.sm }}>
          <Field label={t("delete.passwordLabel")} ltr value={password} onChangeText={setPassword} secureTextEntry autoCapitalize="none" autoComplete="current-password" testID="delete-password" />
          <Button kind="danger" label={t("delete.confirm")} disabled={!password || !!busy} loading={busy === "password"} onPress={() => confirm("password", async () => ({ password }))} testID="delete-with-password" />
        </View>
      ) : null}

      {providers.length ? <SocialButtons providers={providers} busy={busy} onPress={withProvider} labelKey="delete.withProvider" /> : null}

      {info ? (
        <View style={{ gap: space.sm }}>
          {codeSent ? (
            <>
              <AppText variant="caption">{t("delete.codeSent", { email })}</AppText>
              <Field label={t("delete.codeLabel")} ltr value={code} onChangeText={setCode} keyboardType="number-pad" autoComplete="one-time-code" testID="delete-code" />
              <Button kind="danger" label={t("delete.confirm")} disabled={code.trim().length < 6 || !!busy} loading={busy === "code"} onPress={() => confirm("code", async () => ({ code: code.trim() }))} testID="delete-with-code" />
            </>
          ) : (
            <Button kind="secondary" label={t("delete.sendCode")} loading={busy === "send"} disabled={!!busy} onPress={() => void sendCode()} testID="delete-send-code" />
          )}
        </View>
      ) : null}
    </Screen>
  );
}
