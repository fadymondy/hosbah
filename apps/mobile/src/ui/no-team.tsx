import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { View } from "react-native";

import { useApi } from "@/lib/api";
import { errorKey } from "@/lib/errors";
import { openConsole } from "@/lib/legal";
import { usePrefs } from "@/lib/prefs";
import { selectTeam } from "@/lib/team";
import { Button } from "@/ui/button";
import { Card } from "@/ui/card";
import { Field } from "@/ui/field";
import { AppText } from "@/ui/text";
import { space } from "@/ui/theme";
import { useToast } from "@/ui/toast";

/** The web console's host, for copy ("app.hosbah.com"). */
export function useConsoleHost(): string {
  const { serverUrl } = useApi();
  return serverUrl.replace(/^https?:\/\//, "").replace(/\/+$/, "");
}

/**
 * A signed-in account that belongs to no team (a fresh sign-up, or the last team was left): every tab
 * would otherwise be blank. Explains why, lets the person create a team (POST /api/teams, they become
 * its owner) and points at the web console, where invitations are accepted.
 */
export function NoTeam() {
  const { t, locale } = usePrefs();
  const { client, serverUrl } = useApi();
  const qc = useQueryClient();
  const toast = useToast();
  const host = useConsoleHost();
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const create = async () => {
    setBusy(true);
    setError(null);
    try {
      const team = await client.createTeam(name);
      selectTeam(team.id);
      await qc.invalidateQueries({ queryKey: ["me"] });
      toast(t("noTeam.created"));
    } catch (e) {
      setError(t(errorKey(e)));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card testID="no-team" style={{ gap: space.md }}>
      <AppText variant="heading">{t("noTeam.title")}</AppText>
      <AppText>{t("noTeam.body", { console: host })}</AppText>
      <Field label={t("noTeam.name")} value={name} onChangeText={setName} maxLength={80} onSubmitEditing={() => void create()} testID="no-team-name" />
      {error ? (
        <AppText tone="dangerText" testID="no-team-error">
          {error}
        </AppText>
      ) : null}
      <Button label={t("noTeam.create")} onPress={() => void create()} loading={busy} disabled={!name.trim()} testID="no-team-create" />
      <Button
        kind="secondary"
        label={t("noTeam.openConsole", { console: host })}
        onPress={() => openConsole(serverUrl, locale)}
        testID="no-team-console"
      />
      <Button kind="ghost" label={t("noTeam.recheck")} onPress={() => void qc.invalidateQueries({ queryKey: ["me"] })} testID="no-team-recheck" />
    </Card>
  );
}
