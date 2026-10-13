import { appHealth, duration, relativeTime, type App, type Deployment, type Health } from "@hosbah/client";
import { View } from "react-native";

import { usePrefs, type TFn } from "@/lib/prefs";
import { Card } from "@/ui/card";
import { StatusPill } from "@/ui/status";
import { AppText } from "@/ui/text";
import { space } from "@/ui/theme";

export function deployHealth(d: Pick<Deployment, "status">): Health {
  return d.status === "succeeded" ? "ok" : d.status === "failed" ? "down" : "warn";
}

export function appStatusLabel(a: Pick<App, "status" | "imported_state">, t: TFn): string {
  return a.imported_state === "missing" ? t("app.missing") : t(`app.${a.status}`);
}

export function appTitle(a: Pick<App, "name" | "title">): string {
  return a.title || a.name;
}

/** "Succeeded 2 hours ago" style line for the newest deployment. */
export function deployLine(d: Deployment, locale: "ar" | "en", t: TFn): string {
  const when = relativeTime(d.finished_at ?? d.started_at ?? d.created_at, locale);
  return `${t(`deploy.${d.status}`)} · ${when}`;
}

export function AppCard({ app, onPress }: { app: App; onPress: () => void }) {
  const { t, locale } = usePrefs();
  const d = app.latest_deployment;
  return (
    <Card onPress={onPress} testID={`app-${app.name}`} accessibilityLabel={appTitle(app)}>
      <View style={{ gap: space.sm }}>
        <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between", gap: space.sm }}>
          <View style={{ flex: 1 }}>
            <AppText variant="heading" numberOfLines={1}>
              {appTitle(app)}
            </AppText>
            {app.domains?.[0] ? (
              <AppText variant="mono" tone="muted" numberOfLines={1}>
                {app.domains[0].hostname}
              </AppText>
            ) : null}
          </View>
          <StatusPill health={appHealth(app)} label={appStatusLabel(app, t)} />
        </View>
        {d ? (
          <AppText variant="caption" numberOfLines={2}>
            {`#${d.number} · ${deployLine(d, locale, t)}${d.commit_message ? ` · ${d.commit_message}` : ""}`}
          </AppText>
        ) : (
          <AppText variant="caption">{t("app.noDeployments")}</AppText>
        )}
      </View>
    </Card>
  );
}

export function DeploymentRow({ d, onPress }: { d: Deployment; onPress: () => void }) {
  const { t, locale } = usePrefs();
  const took = d.started_at && d.finished_at ? Math.max(0, (Date.parse(d.finished_at) - Date.parse(d.started_at)) / 1000) : null;
  return (
    <Card onPress={onPress} testID={`deployment-${d.number}`} style={{ padding: space.md }}>
      <View style={{ gap: space.xs }}>
        <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between", gap: space.sm }}>
          <AppText variant="label">{`#${d.number}${d.kind !== "deploy" ? ` · ${d.kind}` : ""}`}</AppText>
          <StatusPill health={deployHealth(d)} label={t(`deploy.${d.status}`)} />
        </View>
        {d.commit_message ? (
          <AppText variant="caption" numberOfLines={2}>
            {d.commit_message}
          </AppText>
        ) : null}
        <AppText variant="caption">
          {relativeTime(d.created_at, locale)}
          {took !== null ? ` · ${t("deploy.duration", { duration: duration(took, locale) })}` : ""}
        </AppText>
      </View>
    </Card>
  );
}
