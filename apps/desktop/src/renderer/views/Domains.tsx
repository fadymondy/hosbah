import { certificateHealth, type Certificate, type TeamDomain } from "@hosbah/client";
import { Badge, Card, Empty, ErrorNote, PageHeader, Spinner } from "../components/ui";
import { useApp } from "../lib/app";
import { data } from "../lib/bridge";
import { useData } from "../lib/useData";

const day = (iso: string | undefined, locale: string) => (iso ? new Date(iso).toLocaleDateString(locale === "ar" ? "ar-u-nu-latn" : "en-GB", { year: "numeric", month: "short", day: "numeric" }) : "-");

export function Domains() {
  const { t, locale, route } = useApp();
  const team = route.team;
  const q = useData(
    async () => {
      const [domains, certs] = await Promise.all([data<TeamDomain[]>("domains", team), data<Certificate[]>("certificates", team).catch(() => [] as Certificate[])]);
      return { domains, certs };
    },
    [team],
    { every: 60_000 },
  );
  if (q.loading && !q.data) return <Spinner />;
  if (!q.data) return q.error ? <ErrorNote error={q.error} onRetry={q.reload} /> : null;
  const { domains, certs } = q.data;
  const byHost = new Map(certs.map((c) => [c.hostname, c]));
  // Certificates for hosts that are not an app domain (proxy hosts) still deserve a row.
  const extra = certs.filter((c) => !domains.some((d) => d.hostname === c.hostname));

  const row = (host: string, app: string | null, c: Certificate | undefined, key: string) => {
    const h = c ? certificateHealth(c) : "warn";
    return (
      <Card key={key} className="flex flex-wrap items-center gap-4 px-4 py-3">
        <div className="min-w-0 flex-1">
          <div className="ltr truncate font-medium text-fg">{host}</div>
          <div className="text-xs text-muted">{app ? t("domain.attachedTo", { app }) : t("domain.certificate")}</div>
        </div>
        <div className="text-xs text-muted">
          {c ? (
            <>
              <div>
                {t("domain.expires")}: <span className="ltr text-body">{day(c.not_after, locale)}</span>
              </div>
              {c.issuer && <div className="ltr truncate">{c.issuer}</div>}
            </>
          ) : (
            t("domain.noCert")
          )}
        </div>
        <Badge health={h}>{!c ? t("domain.noCert") : !c.ok ? t("cert.unreadable") : !c.san_match ? t("cert.mismatch") : c.days_left !== undefined ? (c.days_left <= 0 ? t("cert.expired") : t("domain.daysLeft", { days: c.days_left })) : t("cert.ok")}</Badge>
      </Card>
    );
  };

  return (
    <>
      <PageHeader title={t("domain.title")} sub={`${domains.length + extra.length}`} />
      {domains.length + extra.length === 0 ? (
        <Empty>{t("domain.empty")}</Empty>
      ) : (
        <div className="space-y-2">
          {domains.map((d) => row(d.hostname, d.app_name, byHost.get(d.hostname), d.id))}
          {extra.map((c) => row(c.hostname, null, c, c.id))}
        </div>
      )}
    </>
  );
}
