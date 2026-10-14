import { ApiError, EVENT_NAMES, relativeTime, type Device } from "@hosbah/client";
import { useEffect, useState } from "react";
import { Alert, Switch, View } from "react-native";

import { useApi } from "@/lib/api";
import { APP_VERSION } from "@/lib/config";
import { errorKey, testPushErrorKey } from "@/lib/errors";
import { useColors, usePrefs } from "@/lib/prefs";
import { enablePush, pushStatus, type PushState } from "@/lib/push";
import { useCurrentTeam, useDeleteDevice, useDevices, useNotificationPrefs, usePutPref } from "@/lib/queries";
import { Button } from "@/ui/button";
import { Card } from "@/ui/card";
import { Field } from "@/ui/field";
import { Screen } from "@/ui/screen";
import { Segmented } from "@/ui/segmented";
import { AppText } from "@/ui/text";
import { space } from "@/ui/theme";
import { useToast } from "@/ui/toast";

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <View style={{ gap: space.sm }}>
      <AppText variant="heading">{title}</AppText>
      {children}
    </View>
  );
}

function DeviceRow({ d, current, onRemove }: { d: Device; current: boolean; onRemove: () => void }) {
  const { t, locale } = usePrefs();
  return (
    <Card style={{ padding: space.md }} testID={`device-${d.id}`}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: space.md }}>
        <View style={{ flex: 1 }}>
          <AppText variant="label" numberOfLines={1}>
            {d.name}
          </AppText>
          <AppText variant="caption">
            {`${d.platform} · ${d.app} ${d.app_version}${d.last_seen_at ? ` · ${relativeTime(d.last_seen_at, locale)}` : ""}`}
          </AppText>
        </View>
        {current ? (
          <AppText variant="caption" tone="brand">
            {t("settings.thisDevice")}
          </AppText>
        ) : (
          <Button kind="ghost" label={t("settings.removeDevice")} onPress={onRemove} testID={`remove-${d.id}`} />
        )}
      </View>
    </Card>
  );
}

/**
 * Account deletion, in the app (App Store guideline 5.1.1(v)). Proof is the password, or for an account
 * made with Google, GitHub or Apple, the account's email typed in. A team only this person owns that
 * still runs servers or apps blocks it (409), named in the message.
 */
function DeleteAccount() {
  const { t, locale } = usePrefs();
  const toast = useToast();
  const { client, deleteAccount } = useApi();
  const [open, setOpen] = useState(false);
  const [hasPassword, setHasPassword] = useState(true);
  const [proof, setProof] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!open) return;
    void client
      .signInMethods()
      .then((m) => setHasPassword(m.has_password))
      .catch(() => {});
  }, [client, open]);

  const run = async () => {
    setBusy(true);
    setError(null);
    try {
      await deleteAccount(hasPassword ? { password: proof } : { confirm: proof.trim() });
      toast(t("settings.deleted"));
    } catch (e) {
      if (e instanceof ApiError && e.code === "team_has_resources") {
        const teams = ((e.body as { teams?: { name: string }[] } | undefined)?.teams ?? []).map((x) => x.name).join(locale === "ar" ? "، " : ", ");
        setError(t("error.team_has_resources", { teams }));
      } else {
        setError(t(errorKey(e)));
      }
    } finally {
      setBusy(false);
    }
  };

  const confirm = () =>
    Alert.alert(t("settings.deleteAccount"), t("settings.deleteAccountBody"), [
      { text: t("confirm.cancel"), style: "cancel" },
      { text: t("settings.deleteConfirm"), style: "destructive", onPress: () => void run() },
    ]);

  if (!open) return <Button kind="secondary" label={t("settings.deleteAccount")} onPress={() => setOpen(true)} testID="delete-account" />;
  return (
    <Card style={{ padding: space.md, gap: space.md }} testID="delete-account-form">
      <AppText>{t("settings.deleteAccountBody")}</AppText>
      <Field
        label={hasPassword ? t("settings.deletePassword") : t("settings.deleteConfirmEmail")}
        ltr
        value={proof}
        onChangeText={setProof}
        secureTextEntry={hasPassword}
        autoCapitalize="none"
        autoCorrect={false}
        keyboardType={hasPassword ? "default" : "email-address"}
        testID="delete-account-proof"
      />
      {error ? (
        <AppText tone="dangerText" testID="delete-account-error" accessibilityLiveRegion="polite">
          {error}
        </AppText>
      ) : null}
      <Button kind="danger" label={t("settings.deleteConfirm")} onPress={confirm} loading={busy} disabled={!proof.trim()} testID="delete-account-confirm" />
      <Button
        kind="ghost"
        label={t("confirm.cancel")}
        onPress={() => {
          setOpen(false);
          setProof("");
          setError(null);
        }}
      />
    </Card>
  );
}

export default function SettingsScreen() {
  const { t, locale, setLocale, themeSetting, setTheme } = usePrefs();
  const c = useColors();
  const toast = useToast();
  const { client, serverUrl, setServerUrl, signOut } = useApi();
  const { team } = useCurrentTeam();
  const prefs = useNotificationPrefs();
  const putPref = usePutPref();
  const devices = useDevices();
  const del = useDeleteDevice();

  const [push, setPush] = useState<PushState>("unavailable");
  const [testing, setTesting] = useState(false);
  const [server, setServer] = useState(serverUrl);

  useEffect(() => {
    void pushStatus().then(setPush);
  }, []);
  useEffect(() => setServer(serverUrl), [serverUrl]);

  const pushAvailable = prefs.data?.push_available ?? true;

  const sendTest = async () => {
    setTesting(true);
    try {
      await client.testPush();
      toast(t("settings.testPushSent"));
    } catch (e) {
      toast(t(testPushErrorKey(e)), "error");
    } finally {
      setTesting(false);
    }
  };

  const turnOn = async () => {
    try {
      setPush(await enablePush(client, locale));
    } catch (e) {
      toast(t(errorKey(e)), "error");
    }
  };

  const saveServer = () => {
    Alert.alert(t("settings.serverUrl"), t("settings.serverChangeWarning"), [
      { text: t("confirm.cancel"), style: "cancel", onPress: () => setServer(serverUrl) },
      {
        text: t("confirm.yes"),
        style: "destructive",
        onPress: () => void setServerUrl(server).catch((e) => toast(t(errorKey(e)), "error")),
      },
    ]);
  };

  const removeDevice = (d: Device) =>
    Alert.alert(t("settings.removeDevice"), d.name, [
      { text: t("confirm.cancel"), style: "cancel" },
      { text: t("settings.removeDevice"), style: "destructive", onPress: () => del.mutate(d.id) },
    ]);

  const pushNote =
    push === "unavailable"
      ? t("settings.pushUnavailable")
      : push === "denied"
        ? t("settings.pushDenied")
        : !pushAvailable
          ? t("error.push_disabled")
          : null;

  return (
    <Screen title={t("tab.settings")} subtitle={team ? `${team.name} · ${t(`role.${team.role}`)}` : undefined} testID="screen-settings">
      <Section title={t("settings.language")}>
        <Segmented
          value={locale}
          onChange={(l) => void setLocale(l)}
          options={[
            { value: "ar", label: t("settings.language.ar"), testID: "lang-ar" },
            { value: "en", label: t("settings.language.en"), testID: "lang-en" },
          ]}
        />
      </Section>

      <Section title={t("settings.theme")}>
        <Segmented
          value={themeSetting}
          onChange={(v) => void setTheme(v)}
          options={[
            { value: "system", label: t("settings.theme.system"), testID: "theme-system" },
            { value: "light", label: t("settings.theme.light"), testID: "theme-light" },
            { value: "dark", label: t("settings.theme.dark"), testID: "theme-dark" },
          ]}
        />
      </Section>

      <Section title={t("settings.notifications")}>
        {push !== "on" && push !== "unavailable" ? <Button kind="secondary" label={t("settings.enablePush")} onPress={() => void turnOn()} testID="enable-push" /> : null}
        {pushNote ? (
          <AppText variant="caption" tone="warnText" testID="push-note">
            {pushNote}
          </AppText>
        ) : null}
        <Card style={{ padding: 0 }}>
          {EVENT_NAMES.map((e, i) => (
            <View
              key={e}
              style={{
                flexDirection: "row",
                alignItems: "center",
                gap: space.md,
                padding: space.md,
                borderTopWidth: i === 0 ? 0 : 1,
                borderTopColor: c.line,
              }}
            >
              <View style={{ flex: 1 }}>
                <AppText variant="label">{t(`event.${e}`)}</AppText>
                <AppText variant="caption">{t(`event.${e}.desc`)}</AppText>
              </View>
              <Switch
                testID={`pref-${e}`}
                accessibilityLabel={t(`event.${e}`)}
                value={prefs.data?.events[e] ?? false}
                disabled={!prefs.data}
                onValueChange={(v) => putPref.mutate({ [e]: v })}
                trackColor={{ true: c.action, false: c.line }}
              />
            </View>
          ))}
        </Card>
        <Button kind="secondary" label={t("settings.testPush")} onPress={() => void sendTest()} loading={testing} testID="test-push" />
      </Section>

      <Section title={t("settings.serverUrl")}>
        <Field label={t("settings.serverUrl")} ltr value={server} onChangeText={setServer} autoCapitalize="none" autoCorrect={false} keyboardType="url" testID="settings-server" />
        {server.trim() !== serverUrl ? <Button kind="secondary" label={t("common.save")} onPress={saveServer} testID="settings-server-save" /> : null}
      </Section>

      <Section title={t("settings.devices")}>
        {(devices.data ?? []).map((d) => (
          <DeviceRow key={d.id} d={d} current={d.id === client.session?.deviceId} onRemove={() => removeDevice(d)} />
        ))}
      </Section>

      <Section title={t("settings.account")}>
        <Button kind="danger" label={t("settings.signOut")} onPress={() => void signOut()} testID="sign-out" />
        <DeleteAccount />
      </Section>
      <AppText variant="caption" style={{ textAlign: "center" }}>
        {t("settings.version", { version: APP_VERSION })}
      </AppText>
    </Screen>
  );
}
