import {
  backupHealth,
  certificateHealth,
  relativeTime,
  type Certificate,
  type DataService,
  type Health,
  type TeamDomain,
} from "@hosbah/client";
import { View } from "react-native";

import { usePrefs, type TFn } from "@/lib/prefs";
import { Card } from "@/ui/card";
import { StatusPill } from "@/ui/status";
import { AppText } from "@/ui/text";
import { space } from "@/ui/theme";

/** "Expires in 12 days", "Expired", "Cannot read the certificate": the wording for a row. */
export function certLabel(c: Pick<Certificate, "ok" | "san_match" | "days_left">, t: TFn): string {
  if (!c.ok) return t("cert.unreadable");
  if (c.days_left === undefined) return t("cert.unreadable");
  if (c.days_left < 0) return t("cert.expired");
  if (!c.san_match) return t("cert.mismatch");
  return t("cert.expiresIn", { days: c.days_left });
}

export function backupLabel(s: DataService, t: TFn, locale: "ar" | "en"): { health: Health; label: string; detail?: string } {
  const b = s.backups;
  const health = backupHealth(b);
  if (!b || (!b.last_at && !b.schedule)) return { health, label: t("backup.never") };
  if (b.last_status === "failed") return { health, label: t("backup.failed"), ...(b.last_error ? { detail: b.last_error } : {}) };
  if (b.last_restore_status === "fail") return { health, label: t("backup.restoreFailed") };
  if (b.last_status === "running" || b.last_status === "pending") return { health, label: t("backup.running") };
  if (health === "warn") return { health, label: t("backup.stale") };
  const when = b.last_ok_at ?? b.last_at;
  return { health, label: t("backup.ok"), ...(when ? { detail: t("backup.last", { when: relativeTime(when, locale) }) } : {}) };
}

export function CertRow({ domain, cert, appName }: { domain: string; cert: Certificate | undefined; appName?: string | undefined }) {
  const { t } = usePrefs();
  const health: Health = cert ? certificateHealth(cert) : "warn";
  return (
    <Card testID={`domain-${domain}`} style={{ padding: space.md }}>
      <View style={{ gap: space.sm }}>
        <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between", gap: space.sm }}>
          <AppText variant="mono" tone="fg" style={{ flex: 1, fontSize: 13 }} numberOfLines={1}>
            {domain}
          </AppText>
          <StatusPill testID={`cert-${domain}`} health={health} label={cert ? certLabel(cert, t) : t("cert.unreadable")} />
        </View>
        {appName ? <AppText variant="caption">{t("domain.attachedTo", { app: appName })}</AppText> : null}
      </View>
    </Card>
  );
}

export function BackupRow({ service }: { service: DataService }) {
  const { t, locale } = usePrefs();
  const r = backupLabel(service, t, locale);
  return (
    <Card testID={`service-${service.name}`} style={{ padding: space.md }}>
      <View style={{ gap: space.sm }}>
        <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between", gap: space.sm }}>
          <View style={{ flex: 1 }}>
            <AppText variant="label" numberOfLines={1}>
              {service.name}
            </AppText>
            <AppText variant="caption">{`${service.kind} ${service.version}`}</AppText>
          </View>
          <StatusPill health={r.health} label={r.label} />
        </View>
        {r.detail ? <AppText variant="caption">{r.detail}</AppText> : null}
      </View>
    </Card>
  );
}

/** Domains joined with their certificate (by hostname), then certificates with no matching domain. Worst first. */
export function joinDomains(domains: TeamDomain[], certs: Certificate[]) {
  const byHost = new Map(certs.map((c) => [c.hostname, c]));
  const rows = domains.map((d) => ({ host: d.hostname, appName: d.app_name as string | undefined, cert: byHost.get(d.hostname) }));
  const known = new Set(domains.map((d) => d.hostname));
  for (const c of certs) if (!known.has(c.hostname)) rows.push({ host: c.hostname, appName: undefined, cert: c });
  const rank = (r: (typeof rows)[number]) => (r.cert ? (r.cert.ok ? (r.cert.days_left ?? 9999) : -1) : 10_000);
  return rows.sort((a, b) => rank(a) - rank(b));
}
